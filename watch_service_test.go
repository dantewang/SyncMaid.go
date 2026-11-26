package main

import (
	"context"
	"errors"
	"path/filepath"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
)

func watchTrigger(seconds int32) taskconfig.Trigger {
	return taskconfig.Trigger{Kind: "watch", SettleSeconds: &seconds}
}
func TestWatchDebounceTokensAndBoundedPending(t *testing.T) {
	w := &watchedTask{task: taskconfig.Task{ID: taskconfig.NewID(), Trigger: watchTrigger(10)}}
	a := application{watches: map[taskconfig.ID]*watchedTask{w.task.ID: w}, run: activeRun{phase: runRunning, id: 1, taskID: "other"}}
	defer a.stopWatches(watchesStopped)
	a.watchChanged(w)
	old := w.ticket.revision
	for i := 0; i < 100; i++ {
		a.watchChanged(w)
	}
	a.watchReady(w, old)
	if w.ticket.phase != watchQuiet {
		t.Fatal("stale timer delivered")
	}
	a.watchReady(w, w.ticket.revision)
	if w.ticket.phase != watchPending || len(a.watches) != 1 {
		t.Fatal("burst not coalesced")
	}
	a.watchReady(w, w.ticket.revision)
	if w.ticket.phase != watchPending {
		t.Fatal("duplicate ready changed pending")
	}
	a.pauseEditingWatch(w.task.ID)
	a.watchReady(w, w.ticket.revision)
	a.watchChanged(w)
	if w.ticket.phase != watchEditing {
		t.Fatal("late event revived edited subscription")
	}
}
func TestWatchOldSubscriptionStopAndExit(t *testing.T) {
	id := taskconfig.NewID()
	old := &watchedTask{task: taskconfig.Task{ID: id, Trigger: watchTrigger(10)}}
	fresh := &watchedTask{task: old.task}
	a := application{watches: map[taskconfig.ID]*watchedTask{id: fresh}}
	a.watchChanged(old)
	if old.timer != nil {
		t.Fatal("stale subscription scheduled")
	}
	a.watchChanged(fresh)
	token := fresh.ticket.revision
	a.stopWatches(watchesPaused)
	a.watchReady(fresh, token)
	if a.watches != nil || a.watchPhase != watchesPaused {
		t.Fatal("stop revived")
	}
	a.stopWatches(watchesStopped)
	a.resumeWatches()
	a.stopWatches(watchesPaused)
	if a.watchPhase != watchesStopped {
		t.Fatal("exit lost terminal state")
	}
}
func TestWatchAllTasksReconcileAndManualRemoval(t *testing.T) {
	data := t.TempDir()
	l, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	q.Trigger = watchTrigger(10)
	l, first, e := saveMultiple(l, "", "first", q)
	if e != nil {
		t.Fatal(e)
	}
	other, _ := multiFixture(t)
	other.Trigger = watchTrigger(1)
	l, second, e := saveMultiple(l, "", "second", other)
	if e != nil {
		t.Fatal(e)
	}
	a := application{tasks: l}
	a.syncWatches()
	defer a.stopWatches(watchesStopped)
	if len(a.watches) != 2 {
		t.Fatal("only selected task enrolled")
	}
	old := a.watches[first]
	a.selectManual(first)
	a.manual.source = "unsaved draft"
	a.manual.edited()
	if a.liveWatch(old) || a.manual.source != "unsaved draft" {
		t.Fatal("editing watch still active")
	}
	a.manual.dirty = false
	a.selectManual(second)
	q.Trigger = taskconfig.Trigger{Kind: "manual"}
	l, _, e = saveMultiple(l, first, "first", q)
	if e != nil {
		t.Fatal(e)
	}
	a.tasks = l
	a.syncWatches()
	if len(a.watches) != 1 || a.watches[first] != nil {
		t.Fatal("Manual kept old subscription")
	}
	a.watchChanged(old)
	if a.watches[first] != nil {
		t.Fatal("stale event restored removed watch")
	}
	l, e = appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	record, _ := l.Find(second)
	if record.Trigger.Kind != "watch" || *record.Trigger.SettleSeconds != 1 {
		t.Fatal(record.Trigger)
	}
}
func TestWatchTriggerInputAndUnsupportedSyncPolicy(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a := application{tasks: l}
	a.newManual()
	a.setTrigger("watch")
	if a.manual.trigger.SettleSeconds == nil || *a.manual.trigger.SettleSeconds != 10 {
		t.Fatal("wrong default")
	}
	for _, input := range []string{"0", "601", "abc"} {
		a.manual.quietText = input
		a.quietChanged()
		if a.manual.triggerError == "" {
			t.Fatal(input)
		}
	}
	for _, input := range []string{"1", "600"} {
		a.manual.quietText = input
		a.quietChanged()
		if a.manual.triggerError != "" {
			t.Fatal(input)
		}
	}
	a.setTrigger("manual")
	if a.manual.trigger.SettleSeconds != nil {
		t.Fatal("manual retained watch payload")
	}
	q, _ := multiFixture(t)
	l, id, e := saveMultiple(l, "", "legacy manual", q)
	if e != nil {
		t.Fatal(e)
	}
	record, _ := l.Find(id)
	if record.Trigger.Kind != "manual" {
		t.Fatal(record)
	}
	record.Destinations[0].CollisionPolicy = taskconfig.Suffix
	if _, e = appcore.MultipleRequestFromSaved(record); e == nil {
		t.Fatal("Sync suffix silently ignored")
	}
	record.Destinations[0].CollisionPolicy = taskconfig.Skip
	record.Trigger.Kind = "future"
	if _, e = appcore.MultipleRequestFromSaved(record); e == nil {
		t.Fatal("unknown trigger silently manual")
	}
}
func TestWatchMoveSelfEventsNeedOnlyOneQuietFollowup(t *testing.T) {
	q, p := moveFixture(t)
	manualWrite(t, filepath.Join(p.Source, "once.txt"), "move once")
	ticket := watchTicket{}
	_, e := runMultiple(context.Background(), q, func(r syncengine.DestinationResult) {
		if r.Progress.Moved > 0 {
			ticket.change()
		}
	})
	if e != nil || !ticket.ready(ticket.revision) {
		t.Fatal(ticket, e)
	}
	ticket.invalidate(watchArmed)
	results, e := runMultiple(context.Background(), q, func(r syncengine.DestinationResult) {
		if r.Progress.Moved > 0 {
			ticket.change()
		}
	})
	if e != nil || ticket.phase != watchArmed {
		t.Fatal("self event loop", results, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = runMultiple(ctx, q, nil)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
