package main

import (
	"context"
	"fmt"
	"github.com/robfig/cron/v3"
	"reflect"
	"syncmaid/internal/taskconfig"
	"time"
)

type watchPhase uint8

const (
	watchArmed watchPhase = iota
	watchQuiet
	watchPending
	watchEditing
	watchFailed
)

type watchServicePhase uint8

const (
	watchesServing watchServicePhase = iota
	watchesPaused
	watchesStopped
)

// One main-thread ticket per saved task: at most one timer and one pending run.
type watchTicket struct {
	phase    watchPhase
	revision uint64
}

func (t *watchTicket) change() uint64 { t.revision++; t.phase = watchQuiet; return t.revision }
func (t *watchTicket) ready(revision uint64) bool {
	if t.phase != watchQuiet || t.revision != revision {
		return false
	}
	t.phase = watchPending
	return true
}
func (t *watchTicket) invalidate(phase watchPhase) { t.revision++; t.phase = phase }

type watchedTask struct {
	schedule cron.Schedule
	next     time.Time
	task     taskconfig.Task
	ticket   watchTicket
	cancel   context.CancelFunc
	timer    *time.Timer
	status   localizedMessage
}

func (a *application) liveWatch(w *watchedTask) bool {
	return a.watchPhase == watchesServing && a.watches[w.task.ID] == w && w.ticket.phase != watchEditing
}
func (a *application) cancelWatch(w *watchedTask) {
	w.ticket.invalidate(watchEditing)
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.cancel != nil {
		w.cancel()
	}
}
func (a *application) pauseEditingWatch(id taskconfig.ID) {
	if w := a.watches[id]; w != nil {
		a.cancelWatch(w)
		w.status = messagef("编辑中，保存或放弃后重新订阅")
	}
}
func (a *application) stopWatches(phase watchServicePhase) {
	if a.watchPhase == watchesStopped {
		phase = watchesStopped
	}
	a.watchPhase = phase
	for _, w := range a.watches {
		a.cancelWatch(w)
	}
	a.watches = nil
}
func (a *application) resumeWatches() {
	if a.watchPhase == watchesStopped {
		return
	}
	a.watchPhase = watchesServing
	a.syncWatches()
}
func (a *application) syncWatches() {
	if a.watchPhase != watchesServing {
		return
	}
	if a.taskError != nil || a.tasks == nil {
		for _, w := range a.watches {
			a.cancelWatch(w)
		}
		a.watches = nil
		return
	}
	if a.watches == nil {
		a.watches = map[taskconfig.ID]*watchedTask{}
	}
	keep := map[taskconfig.ID]bool{}
	for _, t := range a.tasks.tasks {
		if len(t.Destinations) == 0 || (t.Trigger.Kind != "watch" && t.Trigger.Kind != "scheduled") {
			continue
		}
		keep[t.ID] = true
		old := a.watches[t.ID]
		if old != nil && reflect.DeepEqual(old.task, t) && old.ticket.phase != watchEditing {
			continue
		}
		if old != nil {
			a.cancelWatch(old)
		}
		w := &watchedTask{task: t, status: messagef("正在建立源监视")}
		a.watches[t.ID] = w
		if _, e := multipleRequestFromSaved(t); e != nil {
			w.ticket.phase = watchFailed
			w.status = messagef("触发失败: %s", e.Error())
			continue
		}
		if t.Trigger.Kind == "scheduled" {
			schedule, _, e := parseScheduled(*t.Trigger.CronExpression, time.Now())
			if e != nil {
				w.ticket.phase = watchFailed
				w.status = messagef("触发失败: %s", e.Error())
				continue
			}
			w.schedule = schedule
			a.armScheduled(w, time.Now())
			continue
		}
		if a.window != nil {
			ctx, cancel := context.WithCancel(context.Background())
			w.cancel = cancel
			post := func(action func()) {
				done := make(chan struct{})
				a.window.Update(func() { defer close(done); action() })
				select {
				case <-ctx.Done():
				case <-done:
				}
			}
			a.watchWorkers.Add(1)
			go func() {
				defer a.watchWorkers.Done()
				serveSourceWatch(ctx, t.SourcePath, func(e error) {
					post(func() {
						if !a.liveWatch(w) {
							return
						}
						if e != nil {
							w.ticket.invalidate(watchFailed)
							if w.timer != nil {
								w.timer.Stop()
							}
							w.status = messagef("监视失败，30秒后重试: %s", e.Error())
						} else {
							w.status = messagef("监视已建立")
							a.watchChanged(w)
						}
					})
				}, func() {
					post(func() {
						if a.liveWatch(w) {
							a.watchChanged(w)
						}
					})
				})
			}()
		}
	}
	for id, w := range a.watches {
		if !keep[id] {
			a.cancelWatch(w)
			delete(a.watches, id)
		}
	}
	if a.manual.dirty {
		a.pauseEditingWatch(a.manual.id)
	}
	a.drainWatches()
}
func (a *application) watchChanged(w *watchedTask) {
	if !a.liveWatch(w) {
		return
	}
	revision := w.ticket.change()
	if w.timer != nil {
		w.timer.Stop()
	}
	quiet, _ := w.task.Trigger.SettleWindow()
	w.status = messagef("等待静默 %.0f 秒", quiet.Seconds())
	w.timer = time.AfterFunc(quiet, func() {
		if a.window != nil {
			a.window.Update(func() { a.watchReady(w, revision) })
		}
	})
}
func (a *application) watchReady(w *watchedTask, revision uint64) {
	if !a.liveWatch(w) || !w.ticket.ready(revision) {
		return
	}
	w.status = messagef("等待执行（同任务请求已合并）")
	a.drainWatches()
}
func (a *application) drainWatches() {
	if a.sourcePreview.job != nil {
		return
	}
	if a.batch.phase == batchServing {
		a.advanceBatch()
		return
	}
	if a.watchPhase != watchesServing || a.manual.quitPending || a.manual.busy() || a.tasks == nil || a.taskError != nil || a.window == nil {
		return
	}
	for _, t := range a.tasks.tasks {
		w := a.watches[t.ID]
		if w == nil || w.ticket.phase != watchPending {
			continue
		}
		w.ticket.invalidate(watchArmed)
		if w.schedule != nil {
			a.armScheduled(w, time.Now())
		} else {
			w.status = messagef("正在自动运行")
		}
		q, e := a.tasks.request(t)
		if e != nil {
			w.status = messagef("触发失败: %s", e.Error())
			continue
		}
		a.startTaskRun(t.ID, t.Name, q, true)
		return
	}
}

func validateApplicationTrigger(t taskconfig.Trigger) error {
	if t.HasUnknownFields() {
		return fmt.Errorf("不支持此触发配置")
	}
	if t.Kind == "manual" && t.SettleSeconds == nil && t.CronExpression == nil {
		return nil
	}
	if t.Kind == "watch" && t.SettleSeconds != nil && t.CronExpression == nil {
		return nil
	}
	if t.Kind == "scheduled" && t.CronExpression != nil && t.SettleSeconds == nil {
		return nil
	}
	return fmt.Errorf("不支持或不完整的触发配置")
}
