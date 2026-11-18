package main

import (
	"errors"
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
	"time"
)

func (a *application) armScheduled(w *watchedTask, now time.Time) {
	if !a.liveWatch(w) || w.schedule == nil {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	next := w.schedule.Next(now)
	if next.IsZero() {
		w.ticket.invalidate(watchFailed)
		w.status = messagef(appcore.ErrNoScheduledFuture.Error())
		w.next = time.Time{}
		w.timer = nil
		return
	}
	w.next = next
	revision := w.ticket.change()
	w.status = messagef("下次运行: %s", next.Format("2006-01-02 15:04:05 MST"))
	w.timer = time.AfterFunc(next.Sub(now), func() {
		if a.window != nil {
			a.window.Update(func() { a.scheduledReady(w, revision, time.Now()) })
		}
	})
}
func (a *application) scheduledReady(w *watchedTask, revision uint64, now time.Time) {
	if !a.liveWatch(w) || w.ticket.revision != revision || w.ticket.phase != watchQuiet {
		return
	}
	if !appcore.ScheduledOnTime(now, w.next) {
		a.armScheduled(w, now)
		return
	}
	if !w.ticket.ready(revision) {
		return
	}
	w.status = messagef("定时到期，等待执行（已合并）")
	a.drainWatches()
}

func (s *manualSession) refreshCron() {
	s.cronNext = ""
	s.triggerError = ""
	_, next, e := appcore.ParseScheduled(s.cronText, time.Now())
	if errors.Is(e, appcore.ErrNoScheduledFuture) {
		s.cronNext = appcore.ErrNoScheduledFuture.Error()
		return
	}
	if e != nil {
		s.triggerError = e.Error()
		return
	}
	s.cronNext = next.Format("2006-01-02 15:04:05 MST")
}
func (a *application) cronChanged() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.refreshCron()
	if s.triggerError == "" {
		expression := s.cronText
		s.trigger = taskconfig.Trigger{Kind: "scheduled", CronExpression: &expression}
	}
	s.edited()
}
