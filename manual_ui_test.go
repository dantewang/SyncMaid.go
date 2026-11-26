package main

import (
	"context"
	"errors"
	"path/filepath"
	"syncmaid/internal/appcore"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func TestManualSessionStopAndQuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := taskconfig.NewID()
	a := &application{run: activeRun{phase: runRunning, id: 1, taskID: id, cancel: cancel}, manual: manualSession{id: id}}
	if a.canEditTask(id) {
		t.Fatal("running task remained editable")
	}
	a.requestQuit()
	if !a.quitPending || !a.run.stopping() || ctx.Err() != context.Canceled {
		t.Fatal(a.run.phase, ctx.Err())
	}
	// Closing to tray retains a running worker; only Exit cancels it.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	b := &application{run: activeRun{phase: runRunning, id: 2, taskID: id, cancel: cancel2}}
	b.lifecycle.ready(&recordedWindow{}, func() {}, func() {})
	if !b.lifecycle.closeRequested() || ctx2.Err() != nil || b.run.phase != runRunning {
		t.Fatal("hide canceled work")
	}
}

func TestMirrorConfirmationReplyIsSingleUse(t *testing.T) {
	reply := make(chan error, 1)
	a := application{manual: manualSession{mirrorReply: reply, mirrorPreview: &syncengine.MirrorPreview{}}}
	a.answerMirror(false)
	a.answerMirror(true)
	if !errors.Is(<-reply, syncengine.ErrMirrorDeclined) || a.manual.mirrorReply != nil || a.manual.mirrorPreview != nil {
		t.Fatal("stale confirmation retained")
	}
}

func TestMirrorFailureIsolatedAndPersistence(t *testing.T) {
	q, p := multiFixture(t)
	q.Destinations[0].Strategy = taskconfig.Mirror
	q.Destinations[0].DeleteMode = taskconfig.Permanent
	manualWrite(t, filepath.Join(p.Source, "f.txt"), "input")
	for i := 0; i < 10; i++ {
		manualWrite(t, filepath.Join(p.Target, "extra-"+string(rune('a'+i))+".txt"), "keep")
	}
	r, err := syncengine.RunMultipleConfirmed(context.Background(), q, nil, func(context.Context, syncengine.MirrorPreview) error { return syncengine.ErrMirrorDeclined }, nil)
	if !errors.Is(err, syncengine.ErrMirrorDeclined) || r[q.Destinations[0].ID].State != syncengine.DestinationFailed || r[q.Destinations[1].ID].State != syncengine.DestinationSucceeded {
		t.Fatal(r, err)
	}
	data := t.TempDir()
	l, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	l, id, e := saveMultiple(l, "", "Mirror", q)
	if e != nil {
		t.Fatal(e)
	}
	l, e = appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	record, e := l.Find(id)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := appcore.MultipleRequestFromSaved(record)
	if e != nil || saved.Destinations[0].Strategy != taskconfig.Mirror || saved.Destinations[0].DeleteMode != taskconfig.Permanent {
		t.Fatal(saved, e)
	}
	q.Destinations[0].Filters = []taskconfig.Rule{}
	if _, _, e = saveMultiple(l, id, "invalid", q); e == nil {
		t.Fatal("filtered mirror saved")
	}
}

func TestMovePersistenceAndGuards(t *testing.T) {
	data := t.TempDir()
	l, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := moveFixture(t)
	q.Destinations[0].Flatten = true
	l, id, e := saveMultiple(l, "", "Move", q)
	if e != nil {
		t.Fatal(e)
	}
	l, e = appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	a := application{tasks: l}
	a.selectManual(id)
	if a.manual.kind != taskconfig.Moving || !a.manual.targets[0].Flatten {
		t.Fatal(a.manual)
	}
	a.setManualKind(taskconfig.Sync)
	if a.manual.kind != taskconfig.Moving {
		t.Fatal("saved kind changed")
	}
	a.run = activeRun{phase: runRunning, id: 1, taskID: id}
	a.setMoveFlatten(false)
	if !a.manual.targets[0].Flatten {
		t.Fatal("running edit")
	}
	a.setMoveCollision(taskconfig.Suffix)
	if a.manual.targets[0].Collision == taskconfig.Suffix {
		t.Fatal("running edit changed collision")
	}
	task, _ := l.Find(id)
	task.Destinations[0].CollisionPolicy = taskconfig.CollisionPolicy("Future")
	if _, e := appcore.MultipleRequestFromSaved(task); e == nil {
		t.Fatal("unsupported collision policy ignored")
	}
	q.Kind = taskconfig.Sync
	if _, _, e = saveMultiple(l, id, "wrong kind", q); e == nil {
		t.Fatal("kind downgraded")
	}
	_, _, e = syncengine.PrepareMoveRoutes(context.Background(), nil, []syncengine.ManualDestination{{ID: taskconfig.NewID(), Strategy: taskconfig.Move, Filters: []taskconfig.Rule{{Kind: "regex", Pattern: filterText("a")}}}})
	if !errors.Is(e, filefilter.ErrUnsupportedRegex) {
		t.Fatal(e)
	}
}

func TestMirrorThresholdPersistenceAndUIControls(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := multiFixture(t)
	q.Destinations = q.Destinations[:1]
	q.Destinations[0].Strategy = taskconfig.Mirror
	q.Destinations[0].DeleteMode = taskconfig.Permanent
	q.Destinations[0].MassDeleteThreshold = 0.33
	q.Destinations[0].MassDeleteThresholdSet = true

	l, id, err := saveMultiple(l, "", "MirrorThreshold", q)
	if err != nil {
		t.Fatal(err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, err := l.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Destinations[0].MassDeleteThreshold != 0.33 {
		t.Fatalf("persisted threshold = %v, want 0.33", task.Destinations[0].MassDeleteThreshold)
	}

	a := &application{locale: "zh-Hans", tasks: l}
	a.selectManual(id)
	if a.manual.mirrorThresholdText != "33" {
		t.Fatalf("mirrorThresholdText = %q, want 33", a.manual.mirrorThresholdText)
	}

	tt := ui.NewTester(func(c *ui.Context) { a.destinationModeView(c, false) }, 720, 420)
	if tt.HasText("当前") {
		t.Fatalf("unexpected '当前' badge in threshold UI: %v", tt.Texts())
	}
	if tt.HasText("源目录为空时始终禁止删除") {
		t.Fatalf("help section should be collapsed by default: %v", tt.Texts())
	}
	tt.Click(a.tr("阈值规则说明"))
	if !a.manual.mirrorThresholdHelpOpen {
		t.Fatal("expected ? icon click to toggle help section open")
	}
	tt2 := ui.NewTester(func(c *ui.Context) { a.destinationModeView(c, false) }, 720, 420)
	if !tt2.HasText("33%") {
		t.Fatalf("expected expanded help text with 33%%: %v", tt2.Texts())
	}
	tt2.Click(a.tr("关闭 (0%)"))
	if a.manual.mirrorThresholdText != "0" || a.manual.targets[0].MassDeleteThreshold != 0 || !a.manual.targets[0].MassDeleteThresholdSet {
		t.Fatalf("expected 0%% preset to update threshold, got text=%q dest=%+v", a.manual.mirrorThresholdText, a.manual.targets[0])
	}
}

func TestMirrorPreviewShowsExtraDirsWithoutFooterNote(t *testing.T) {
	a := &application{
		locale: "zh-Hans",
		manual: manualSession{
			mirrorPreview: &syncengine.MirrorPreview{
				Name:   "NAS",
				Target: `D:\Backups`,
				Mode:   taskconfig.Recycle,
				Paths:  []string{"old.txt"},
				Dirs:   []string{"empty/sub", "empty"},
			},
		},
	}
	tt := ui.NewTester(func(c *ui.Context) { a.mirrorPreviewView(c) }, 720, 480)
	if !tt.HasText("2个空目录") {
		t.Fatalf("expected empty dir count in preview header: %v", tt.Texts())
	}
	if tt.HasText("空目录仅在") {
		t.Fatalf("unexpected footer note in preview: %v", tt.Texts())
	}
	tt.Click(a.tr("多余空目录 (%d)", 2))
	tt2 := ui.NewTester(func(c *ui.Context) { a.mirrorPreviewView(c) }, 720, 480)
	if !tt2.HasText("empty/sub") {
		t.Fatalf("expected extra empty dir in preview list: %v", tt2.Texts())
	}
}
