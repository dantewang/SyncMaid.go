package main

import (
	"context"
	"errors"
	"github.com/egoist/mygo"
	"github.com/robfig/cron/v3"
	"io"
	"reflect"
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
	"time"
)

type watchPhase uint8

const (
	watchArmed watchPhase = iota
	watchQuiet
	watchPending
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

// editingTaskID derives the task currently being edited or confirmed for deletion
// from UI state, avoiding imperative pause/resume flags that can leak on cancel.
func (a *application) editingTaskID() taskconfig.ID {
	if a.manual.id == "" {
		return ""
	}
	if a.cards.editor || a.manual.dirty || a.manual.phase == manualChoosing {
		return a.manual.id
	}
	return ""
}

func (a *application) liveWatch(w *watchedTask) bool {
	return a.watchPhase == watchesServing && a.watches[w.task.ID] == w && a.editingTaskID() != w.task.ID
}
func (a *application) cancelWatch(w *watchedTask) {
	w.ticket.invalidate(watchArmed)
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.cancel != nil {
		w.cancel()
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
	for _, t := range a.tasks.Tasks {
		if len(t.Destinations) == 0 || (t.Trigger.Kind != "watch" && t.Trigger.Kind != "scheduled") {
			continue
		}
		keep[t.ID] = true
		old := a.watches[t.ID]
		if old != nil && reflect.DeepEqual(old.task, t) {
			if old.schedule != nil && a.editingTaskID() != t.ID && !old.next.After(time.Now()) {
				a.armScheduled(old, time.Now())
			}
			continue
		}
		if old != nil {
			a.cancelWatch(old)
		}
		w := &watchedTask{task: t, status: messagef("正在建立源监视")}
		a.watches[t.ID] = w
		if _, e := appcore.MultipleRequestFromSaved(t); e != nil {
			w.ticket.phase = watchFailed
			w.status = messagef("触发失败: %s", e.Error())
			continue
		}
		if t.Trigger.Kind == "scheduled" {
			schedule, _, e := appcore.ParseScheduled(*t.Trigger.CronExpression, time.Now())
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
				appcore.ServeSourceWatch(ctx, t.SourcePath, func(e error) {
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
				}, serveNativeWatch)
			}()
		}
	}
	for id, w := range a.watches {
		if !keep[id] {
			a.cancelWatch(w)
			delete(a.watches, id)
		}
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
	if a.watchPhase != watchesServing || a.quitPending || a.isRunning() || a.configBusy() || a.tasks == nil || a.taskError != nil || a.window == nil {
		return
	}
	editingID := a.editingTaskID()
	for _, t := range a.tasks.Tasks {
		if t.ID == editingID {
			continue
		}
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
		q, e := a.tasks.Request(t)
		if e != nil {
			w.status = messagef("触发失败: %s", e.Error())
			continue
		}
		a.startTaskRun(t.ID, t.Name, q, true)
		return
	}
}

// Native notifications are hints only. Every run still scans and validates all
// actual objects. A failed enrollment retries slowly, never runs an empty list.
func serveNativeWatch(ctx context.Context, path string, status func(error), changed func()) {
	for ctx.Err() == nil {
		w, e := mygo.WatchFiles(ctx, path, mygo.FileWatchOptions{Recursive: true, HintsOnly: true, Debounce: 50 * time.Millisecond, MaxDelay: 500 * time.Millisecond, MaxEntries: 200000})
		if e == nil {
			status(nil)
			for ctx.Err() == nil {
				events, err := w.Next(ctx)
				if err != nil {
					e = err
					break
				}
				if len(events) > 0 {
					changed()
				}
			}
			_ = w.Close()
			<-w.Done()
		}
		if ctx.Err() != nil {
			return
		}
		if e == nil || errors.Is(e, io.EOF) {
			e = errors.New("源监视已结束或源目录不可用")
		}
		status(e)
		retry := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
	}
}
