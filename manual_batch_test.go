package main

import (
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestBatchOrderGateFailureAndSingleOwner(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	var ids []taskconfig.ID
	for _, name := range []string{"First", "Unsupported", "Mirror"} {
		q := manualFixture(t)
		var id taskconfig.ID
		l, id, e = saveTask(l, "", name, q)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	l.Tasks[2].Destinations[0].Strategy = taskconfig.Mirror
	l.Tasks[1].Trigger = taskconfig.Trigger{Kind: "future"}
	a := &application{tasks: l}
	a.batch.begin(ids)
	// The click owns an independent order, even if the caller reuses its slice.
	ids[0] = "changed"
	var ran []taskconfig.ID
	start := func(id taskconfig.ID, _ string, q syncengine.ManualTaskRequest, automatic bool) {
		if automatic {
			t.Fatal("batch used automatic execution")
		}
		ran = append(ran, id)
		a.manual.phase = manualRunning
		if len(ran) == 2 && q.Destinations[0].Strategy != taskconfig.Mirror {
			t.Fatal("Mirror confirmation contract lost")
		}
	}
	a.advanceBatchUsing(start)
	if len(ran) != 1 || ran[0] != l.Tasks[0].ID {
		t.Fatal(ran)
	}
	a.advanceBatchUsing(start)
	if len(ran) != 1 {
		t.Fatal("parallel dispatch")
	}
	if a.cardActionAllowed() {
		t.Fatal("ordinary actions bypassed batch owner")
	}
	a.manual.phase = manualFinished
	a.advanceBatchUsing(start)
	if len(ran) != 2 || ran[1] != l.Tasks[2].ID || a.results[l.Tasks[1].ID].message.key == "" {
		t.Fatal("failure swallowed or order changed", ran)
	}
	a.manual.phase = manualFinished
	a.advanceBatchUsing(start)
	if a.batch.phase != batchIdle {
		t.Fatal("batch not released")
	}
}
func TestBatchStopAndDirtyPreviewGuards(t *testing.T) {
	a := &application{manual: manualSession{phase: manualRunning, cancel: func() {}}, batch: manualBatch{phase: batchServing, pending: []taskconfig.ID{"later"}}}
	a.stopManual()
	if a.batch.phase != batchIdle || len(a.batch.pending) != 0 || a.manual.phase != manualStopping {
		t.Fatal("stop retained queue")
	}
	a.manual.phase = manualFinished
	a.batch.begin([]taskconfig.ID{"x"})
	a.manual.dirty = true
	a.advanceBatchUsing(func(taskconfig.ID, string, syncengine.ManualTaskRequest, bool) { t.Fatal("dirty batch dispatched") })
	if a.batch.phase != batchIdle {
		t.Fatal("dirty batch retained")
	}
	a.manual.dirty = false
	a.manual.phase = manualRunning
	a.batch.begin([]taskconfig.ID{"later"})
	a.requestQuit()
	if a.batch.phase != batchIdle || !a.manual.quitPending || a.manual.phase != manualStopping {
		t.Fatal("exit retained batch")
	}
}

func TestBatchRejectsPreviewsAndAutomaticEntry(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	l, _, e = saveTask(l, "", "Saved", manualFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	for _, block := range []func(*application){func(a *application) { a.manual.dirty = true }, func(a *application) { a.importPreview = &appcore.LegacyImport{} }, func(a *application) { a.regexSession = &regexSession{} }, func(a *application) { a.manual.mirrorPreview = &syncengine.MirrorPreview{} }} {
		a := &application{tasks: l, manual: manualSession{phase: manualReady}}
		block(a)
		a.startBatch()
		if a.batch.phase != batchIdle || a.manual.phase != manualReady {
			t.Fatal("blocked batch started")
		}
	}
	a := &application{batch: manualBatch{phase: batchServing}, manual: manualSession{phase: manualFinished}}
	a.startTaskRun("automatic", "Automatic", syncengine.ManualTaskRequest{}, true)
	if a.manual.phase != manualFinished || a.cards.running != "" {
		t.Fatal("automatic entry bypassed batch")
	}
}
