package main

import (
	"errors"
	"fmt"
	"strings"
	"syncmaid/internal/taskconfig"
	"time"

	"github.com/robfig/cron/v3"
)

var errNoScheduledFuture = errors.New("此表达式没有可计算的未来执行时间，请修改计划")
var scheduledParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Five fields only; no descriptor, seconds, year or timezone override. The
// library's local-zone schedule uses the location of the time passed to Next.
func parseScheduled(expression string, after time.Time) (cron.Schedule, time.Time, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 || strings.Contains(expression, "?") || strings.HasPrefix(fields[0], "TZ=") || strings.HasPrefix(fields[0], "CRON_TZ=") {
		return nil, time.Time{}, errors.New("cron需要五字段：分 时 日 月 星期；本地时区，不支持秒、年份、?或时区前缀")
	}
	schedule, e := scheduledParser.Parse(strings.Join(fields, " "))
	if e != nil {
		return nil, time.Time{}, fmt.Errorf("cron: %w", e)
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return nil, time.Time{}, errNoScheduledFuture
	}
	return schedule, next, nil
}

// Normal timer/dispatch jitter is tolerated. A notification delayed by sleep
// for more than five seconds is discarded, never replayed as historical work.
const scheduledLateness = 5 * time.Second

func scheduledOnTime(now, due time.Time) bool {
	return !now.Before(due) && now.Sub(due) <= scheduledLateness
}

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
		w.status = messagef(errNoScheduledFuture.Error())
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
	if !scheduledOnTime(now, w.next) {
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
	_, next, e := parseScheduled(s.cronText, time.Now())
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
