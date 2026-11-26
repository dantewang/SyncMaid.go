package main

import (
	"context"
	"path/filepath"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestEditCascadedTaskWhileDownstreamRuns(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	srcA := filepath.Join(root, "src-a")
	midDir := filepath.Join(root, "mid")
	dstB := filepath.Join(root, "dst-b")
	srcC := filepath.Join(root, "src-c")
	dstC := filepath.Join(root, "dst-c")

	reqA := syncengine.ManualTaskRequest{
		Kind:   taskconfig.Sync,
		Source: srcA,
		Destinations: []syncengine.ManualDestination{{
			ID:      taskconfig.NewID(),
			Name:    "ToMid",
			Path:    midDir,
			Filters: syncengine.AllFileRules(),
		}},
	}
	reqB := syncengine.ManualTaskRequest{
		Kind:   taskconfig.Sync,
		Source: midDir,
		Destinations: []syncengine.ManualDestination{{
			ID:      taskconfig.NewID(),
			Name:    "ToDstB",
			Path:    dstB,
			Filters: syncengine.AllFileRules(),
		}},
	}

	l, idA, err := saveMultiple(l, "", "UpstreamA", reqA)
	if err != nil {
		t.Fatal(err)
	}
	l, idB, err := saveMultiple(l, "", "DownstreamB", reqB)
	if err != nil {
		t.Fatal(err)
	}

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	a := &application{
		locale: "zh-Hans",
		tasks:  l,
		run: activeRun{
			phase:  runRunning,
			id:     1,
			taskID: idB,
			cancel: cancelB,
		},
		liveSnapshot: liveRunSnapshot{
			Active:   true,
			TaskID:   idB,
			TaskName: "DownstreamB",
			Stage:    "同步中",
		},
	}

	// Select running task B: viewing is allowed, editing/deleting B is blocked.
	a.selectManual(idB)
	if a.manual.id != idB {
		t.Fatalf("expected to select running task B for inspection, got %q", a.manual.id)
	}
	a.editCard(idB, "")
	if a.cards.editor || a.canEditTask(idB) {
		t.Fatal("running task B must not enter edit mode")
	}
	a.addManualTarget()
	a.deleteManual(idB)
	if len(a.manual.targets) != 1 || len(a.tasks.Tasks) != 2 {
		t.Fatal("running task B was mutated or deleted")
	}

	// Switch to cascaded upstream task A while B is running, edit and persist A.
	a.selectManual(idA)
	if !a.canEditTask(idA) {
		t.Fatal("non-running cascaded task A should be editable while B runs")
	}
	ttA := ui.NewTester(a.view, 1040, 680)
	if !ttA.HasText("UpstreamA") || !ttA.HasText(a.tr("编辑任务")) {
		t.Fatalf("expected task A view with edit button: %v", ttA.Texts())
	}

	a.editCard(idA, "")
	if !a.cards.editor || a.manual.id != idA {
		t.Fatal("failed to open editor for task A while B runs")
	}
	a.manual.name = "UpstreamA-Renamed"
	a.manual.edited()

	// Cross-task overlap check still prevents pointing A's source at B's source.
	badReq := a.manual.draftRequest()
	badReq.Source = midDir
	if _, _, overlapErr := a.tasks.PrepareMultiple(idA, a.manual.name, badReq); overlapErr == nil {
		t.Fatal("overlap with running task B source was not rejected")
	}

	tasks, nextID, prepErr := a.tasks.PrepareMultiple(idA, a.manual.name, a.manual.draftRequest())
	if prepErr != nil {
		t.Fatal(prepErr)
	}
	snap, saveErr := a.tasks.Snapshot.Save(tasks)
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	nextLib, libErr := appcore.SavedLibrary(snap)
	if libErr != nil {
		t.Fatal(libErr)
	}
	a.manual.phase = manualSaving
	a.finishTaskSave(nextLib, nextID, nil, nil)
	if a.cards.editor || a.manual.dirty || a.manual.name != "UpstreamA-Renamed" {
		t.Fatalf("save did not cleanly update task A while B ran: editor=%v dirty=%v name=%q", a.cards.editor, a.manual.dirty, a.manual.name)
	}
	if !a.isTaskRunning(idB) || ctxB.Err() != nil {
		t.Fatal("saving task A disturbed running task B")
	}

	reloaded, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	savedA, err := reloaded.Find(idA)
	if err != nil || savedA.Name != "UpstreamA-Renamed" {
		t.Fatalf("persisted task A = %+v, err=%v", savedA, err)
	}

	// Creating a brand-new task C while B runs is also allowed.
	a.newManual()
	a.cards.editor = true
	a.manual.name = "NewTaskC"
	a.manual.source = srcC
	a.manual.targets = []syncengine.ManualDestination{{ID: taskconfig.NewID(), Name: "DstC", Path: dstC, Filters: syncengine.AllFileRules()}}
	a.manual.edited()
	tasks, idC, prepErr := a.tasks.PrepareMultiple("", a.manual.name, a.manual.draftRequest())
	if prepErr != nil {
		t.Fatal(prepErr)
	}
	snap, saveErr = a.tasks.Snapshot.Save(tasks)
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	nextLib, libErr = appcore.SavedLibrary(snap)
	if libErr != nil {
		t.Fatal(libErr)
	}
	a.manual.phase = manualSaving
	a.finishTaskSave(nextLib, idC, nil, nil)
	if len(a.tasks.Tasks) != 3 || !a.isTaskRunning(idB) {
		t.Fatalf("expected 3 tasks and B still running, got len=%d running=%v", len(a.tasks.Tasks), a.run)
	}
}

func TestDerivedWatchAndScheduleSuppressionAcrossEditLifecycle(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}

	qA, _ := multiFixture(t)
	cronExpr := "* * * * *"
	qA.Trigger = taskconfig.Trigger{Kind: "scheduled", CronExpression: &cronExpr}
	l, idA, err := saveMultiple(l, "", "SchedA", qA)
	if err != nil {
		t.Fatal(err)
	}

	qB, _ := multiFixture(t)
	qB.Trigger = watchTrigger(10)
	l, idB, err := saveMultiple(l, "", "WatchB", qB)
	if err != nil {
		t.Fatal(err)
	}

	a := &application{tasks: l}
	a.syncWatches()
	defer a.stopWatches(watchesStopped)

	wA := a.watches[idA]
	wB := a.watches[idB]
	if wA == nil || wB == nil || !a.liveWatch(wA) || !a.liveWatch(wB) {
		t.Fatalf("expected both watches enrolled and live: wA=%v wB=%v", wA, wB)
	}

	// Opening editor on Task A (even before dirty edits) suppresses only Task A.
	a.editCard(idA, "")
	if a.editingTaskID() != idA || a.liveWatch(wA) || !a.liveWatch(wB) {
		t.Fatal("editor open did not isolate suppression to Task A")
	}

	// Due event arriving while Task A is open in editor is ignored (no catch-up or pending).
	dueRev := wA.ticket.revision
	a.scheduledReady(wA, dueRev, wA.next.Add(time.Millisecond))
	if wA.ticket.phase == watchPending {
		t.Fatal("scheduled task A became pending while editor was open")
	}

	// Simulate wA.next elapsing in the past while the editor remained open.
	wA.next = time.Now().Add(-2 * time.Minute)

	// Meanwhile Task B still processes file-watch events normally.
	a.watchChanged(wB)
	a.watchReady(wB, wB.ticket.revision)
	if wB.ticket.phase != watchPending {
		t.Fatal("editing Task A suppressed unrelated Task B watch")
	}

	// Closing Task A's editor restores liveWatch without manual resume and re-arms future schedule.
	a.closeTaskEditor()
	if a.editingTaskID() != "" || !a.liveWatch(wA) {
		t.Fatal("closing editor leaked watch suppression on Task A")
	}
	if !wA.next.After(time.Now()) {
		t.Fatalf("expired schedule on Task A was not re-armed to future: %v", wA.next)
	}
}
