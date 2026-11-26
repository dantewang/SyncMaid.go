package main

import (
	"errors"
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
	"testing"
	"time"
)

func TestScheduledParsingAndNext(t *testing.T) {
	loc := time.FixedZone("fixture+8", 8*3600)
	after := time.Date(2026, 8, 9, 2, 6, 0, 0, loc)
	for _, c := range []struct {
		expression string
		want       time.Time
	}{
		{"5/15 * * * *", time.Date(2026, 8, 9, 2, 20, 0, 0, loc)},
		{"0 2 * * 1", time.Date(2026, 8, 10, 2, 0, 0, 0, loc)},
		{"0 2 * * *", time.Date(2026, 8, 10, 2, 0, 0, 0, loc)},
		{"*/5 * * * *", time.Date(2026, 8, 9, 2, 10, 0, 0, loc)},
	} {
		_, next, e := appcore.ParseScheduled(c.expression, after)
		if e != nil || !next.Equal(c.want) || next.Location() != loc {
			t.Fatal(c, next, e)
		}
	}
	at := time.Date(2026, 8, 9, 2, 0, 0, 0, loc)
	_, next, e := appcore.ParseScheduled("0 2 * * *", at)
	if e != nil || !next.After(at) || next.Day() != 10 {
		t.Fatal(next, e)
	}
	for _, expression := range []string{"", "0 2 * *", "99 * * * *", "0 * * * * *", "0 0 * * * 2027", "@hourly", "CRON_TZ=UTC * * * * *", "TZ=UTC * * * *", "0 0 ? * *", "0 0 * * 7"} {
		if _, _, e := appcore.ParseScheduled(expression, after); e == nil {
			t.Fatal("unsupported cron accepted", expression)
		}
	}
	if _, _, e := appcore.ParseScheduled("0 0 30 2 *", after); !errors.Is(e, appcore.ErrNoScheduledFuture) {
		t.Fatal(e)
	}
}
func TestScheduledCalendarBoundaries(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Skip("timezone fixture unavailable", e)
	}
	_, next, e := appcore.ParseScheduled("30 2 * * *", time.Date(2026, 3, 7, 3, 0, 0, 0, loc))
	if e != nil || next.Day() != 9 || next.Hour() != 2 || next.Minute() != 30 {
		t.Fatal("DST missing hour", next, e)
	}
	_, next, e = appcore.ParseScheduled("0 0 29 2 *", time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))
	if e != nil || next.Year() != 2028 || next.Day() != 29 {
		t.Fatal(next, e)
	}
	// v3.0.1 searches at most five years. Do not promise arbitrary future dates.
	if _, _, e = appcore.ParseScheduled("0 0 29 2 *", time.Date(2096, 3, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(e, appcore.ErrNoScheduledFuture) {
		t.Fatal(e)
	}
}
func TestScheduledDueCoalescingLateAndStale(t *testing.T) {
	now := time.Date(2026, 8, 9, 2, 6, 0, 0, time.Local)
	schedule, _, e := appcore.ParseScheduled("* * * * *", now)
	if e != nil {
		t.Fatal(e)
	}
	w := &watchedTask{task: taskconfig.Task{ID: taskconfig.NewID()}, schedule: schedule}
	a := application{watches: map[taskconfig.ID]*watchedTask{w.task.ID: w}, run: activeRun{phase: runRunning, id: 1, taskID: "other"}}
	defer a.stopWatches(watchesStopped)
	a.armScheduled(w, now)
	revision := w.ticket.revision
	due := w.next
	a.scheduledReady(w, revision, due.Add(time.Millisecond))
	for i := 0; i < 100; i++ {
		a.scheduledReady(w, revision, due.Add(time.Millisecond))
	}
	if w.ticket.phase != watchPending || len(a.watches) != 1 {
		t.Fatal("unbounded or lost pending")
	}
	a.armScheduled(w, now)
	old := w.ticket.revision
	oldDue := w.next
	resumed := now.Add(2 * time.Hour)
	a.scheduledReady(w, old, resumed)
	if w.ticket.phase != watchQuiet || !w.next.After(resumed) {
		t.Fatal("historical work replayed", w.next, w.ticket)
	}
	a.scheduledReady(w, old, oldDue)
	if w.ticket.phase != watchQuiet {
		t.Fatal("stale timer delivered")
	}
	a.cards.editor = true
	a.manual.id = w.task.ID
	a.scheduledReady(w, w.ticket.revision, w.next)
	if a.liveWatch(w) || w.ticket.phase == watchPending {
		t.Fatal("edited schedule revived")
	}
	a.cards.editor = false
	a.stopWatches(watchesPaused)
	a.scheduledReady(w, old, due)
	if a.watches != nil {
		t.Fatal("stop revived schedule")
	}
	a.stopWatches(watchesStopped)
	a.resumeWatches()
	if a.watchPhase != watchesStopped {
		t.Fatal("exit revived")
	}
}
func TestScheduledSaveRestoreAndInput(t *testing.T) {
	data := t.TempDir()
	l, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	expression := "5/15 * * * *"
	q.Trigger = taskconfig.Trigger{Kind: "scheduled", CronExpression: &expression}
	l, id, e := saveMultiple(l, "", "scheduled", q)
	if e != nil {
		t.Fatal(e)
	}
	l, e = appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	// Load the real persisted bytes with the existing store, not a new schema.
	tasks, e := l.Snapshot.Tasks()
	if e != nil {
		t.Fatal(e)
	}
	if tasks[0].Trigger.Kind != "scheduled" || *tasks[0].Trigger.CronExpression != expression {
		t.Fatal(tasks)
	}
	a := application{tasks: l}
	a.syncWatches()
	defer a.stopWatches(watchesStopped)
	w := a.watches[id]
	if w == nil || w.schedule == nil || w.next.IsZero() {
		t.Fatal("schedule not enrolled")
	}
	a.selectManual(id)
	a.manual.cronText = "0 0 30 2 *"
	a.cronChanged()
	if a.manual.triggerError != "" || a.manual.cronNext != appcore.ErrNoScheduledFuture.Error() || a.liveWatch(w) {
		t.Fatal("never-occurring schedule not described or old timer active")
	}
	a.manual.cronText = "*/5 * * * *"
	a.cronChanged()
	if a.manual.triggerError != "" || a.manual.cronNext == "" {
		t.Fatal(a.manual)
	}
	a.setTrigger("manual")
	q = a.manual.draftRequest()
	l, _, e = saveMultiple(l, id, "manual", q)
	if e != nil {
		t.Fatal(e)
	}
	a.tasks = l
	a.manual.dirty = false
	a.syncWatches()
	if a.watches[id] != nil {
		t.Fatal("old Scheduled kept")
	}
	a.setTrigger("watch")
	if a.manual.trigger.CronExpression != nil || a.manual.trigger.SettleSeconds == nil {
		t.Fatal("watch retained cron payload")
	}
	bad := "0 0 30 2 *"
	q.Trigger = taskconfig.Trigger{Kind: "scheduled", CronExpression: &bad}
	l, _, e = saveMultiple(l, id, "never occurs", q)
	if e != nil {
		t.Fatal(e)
	}
	a.tasks = l
	a.manual.dirty = false
	a.syncWatches()
	if w := a.watches[id]; w == nil || w.timer != nil || w.ticket.phase != watchFailed {
		t.Fatal("never-occurring schedule armed a timer")
	}
}
