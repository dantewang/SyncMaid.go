package main

import (
	"context"
	"errors"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"syncmaid/internal/appcore"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestSourcePreviewMessagesDistinguishEmptyAndErrorInFourLanguages(t *testing.T) {
	for _, lang := range languageChoices {
		a := &application{locale: lang.tag, sourcePreview: sourcePreviewSession{phase: previewReady, result: &appcore.RoutePreview{EmptyRoot: true}}}
		view := ui.NewTester(a.sourcePreviewView, 760, 620)
		if !view.HasText(a.tr("源目录为空。")) {
			t.Fatal(lang.tag, "empty missing")
		}
		a.sourcePreview.result.EmptyRoot = false
		view = ui.NewTester(a.sourcePreviewView, 760, 620)
		if !view.HasText(a.tr("源目录存在，但没有普通文件。")) {
			t.Fatal(lang.tag, "no-files missing")
		}
		a.sourcePreview.phase = previewFailed
		a.sourcePreview.err = errors.New("fixture unavailable")
		a.sourcePreview.result = nil
		view = ui.NewTester(a.sourcePreviewView, 760, 620)
		if !view.HasText(a.tr("预览失败: %s", "fixture unavailable")) || view.HasText(a.tr("源文件: %d", 0)) {
			t.Fatal(lang.tag, "error became zero")
		}
	}
}
func TestSourceExtensionStatisticsAreBounded(t *testing.T) {
	files := []fileinventory.File{{Path: "a.txt"}, {Path: "b.TXT"}, {Path: "photo.jpg"}}
	for i := 0; i < 16; i++ {
		files = append(files, fileinventory.File{Path: "file.ext" + string(rune('a'+i))})
	}
	stats := appcore.SourceExtensions(files)
	if len(stats) != 12 || stats[0].Text != "txt" || stats[0].Count != 2 {
		t.Fatal(stats)
	}
}

func TestSourcePreviewRequiresCurrentRegexConfirmation(t *testing.T) {
	l, task, p, _ := migrationFixture(t, true)
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "synthetic")
	l, err := l.SaveRegexConfirmation(migrationReview(t, task, `\.txt$`))
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l}
	a.selectManual(task.ID)
	q, err := a.previewDraft()
	if err != nil {
		t.Fatal(err)
	}
	r, err := appcore.ScanSourcePreview(context.Background(), q)
	if err != nil || r.Targets[task.Destinations[0].ID].Count != 1 {
		t.Fatal(r, err)
	}
	a.manual.name = "edited without re-confirming"
	a.manual.dirty = true
	q, err = a.previewDraft()
	if err != nil {
		t.Fatal(err)
	}
	r, err = appcore.ScanSourcePreview(context.Background(), q)
	if r != nil || !errors.Is(err, filefilter.ErrUnsupportedRegex) {
		t.Fatal("preview renewed a receipt", r, err)
	}
	absentMoveFile(t, filepath.Join(p.Target, "a.txt"))
}

func previewFixture(t *testing.T) syncengine.ManualTaskRequest {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"中文/a.txt", "photo.jpg", "notes.txt"} {
		p = filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("synthetic source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return syncengine.ManualTaskRequest{Source: root, Kind: taskconfig.Sync, Destinations: []syncengine.ManualDestination{
		{ID: taskconfig.NewID(), Name: "Text", Path: filepath.Join(root, "missing-target"), Strategy: taskconfig.AddOnly, Filters: []taskconfig.Rule{taskconfig.ExtensionFilter("txt")}},
		{ID: taskconfig.NewID(), Name: "Rest", Path: filepath.Join(root, "also-missing"), Strategy: taskconfig.AddOnly, Filters: syncengine.AllFileRules()},
	}}
}
func TestSourcePreviewSyncAndMoveAreReadOnlyAndBounded(t *testing.T) {
	q := previewFixture(t)
	r, err := appcore.ScanSourcePreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceFiles != 3 || r.EmptyRoot || r.Targets[q.Destinations[0].ID].Count != 2 || r.Targets[q.Destinations[1].ID].Count != 3 {
		t.Fatal(r)
	}
	q.Kind = taskconfig.Moving
	for i := range q.Destinations {
		q.Destinations[i].Strategy = taskconfig.Move
		q.Destinations[i].Collision = taskconfig.Skip
	}
	r, err = appcore.ScanSourcePreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Targets[q.Destinations[0].ID].Count != 2 || r.Targets[q.Destinations[1].ID].Count != 1 || len(r.Contested) != 2 || r.Contested[0].Targets[0] != q.Destinations[0].ID {
		t.Fatal(r)
	}
	for _, p := range []string{"中文/a.txt", "photo.jpg", "notes.txt"} {
		b, e := os.ReadFile(filepath.Join(q.Source, filepath.FromSlash(p)))
		if e != nil || string(b) != "synthetic source" {
			t.Fatal("source changed", e)
		}
	}
	for _, d := range q.Destinations {
		if _, e := os.Stat(d.Path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("preview touched target", e)
		}
	}
	for i := 0; i < 20; i++ {
		if e := os.WriteFile(filepath.Join(q.Source, "more"+string(rune('a'+i))+".txt"), []byte("x"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r, err = appcore.ScanSourcePreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Contested) != appcore.PreviewSamples || len(r.Targets[q.Destinations[0].ID].Samples) != appcore.PreviewSamples {
		t.Fatal("unbounded sample")
	}
}
func TestSourcePreviewEmptyMissingCanceledAndInvalidRules(t *testing.T) {
	q := previewFixture(t)
	q.Source = t.TempDir()
	r, e := appcore.ScanSourcePreview(context.Background(), q)
	if e != nil || !r.EmptyRoot || r.SourceFiles != 0 {
		t.Fatal(r, e)
	}
	if e = os.Mkdir(filepath.Join(q.Source, "folder"), 0700); e != nil {
		t.Fatal(e)
	}
	r, e = appcore.ScanSourcePreview(context.Background(), q)
	if e != nil || r.EmptyRoot || r.SourceFiles != 0 {
		t.Fatal(r, e)
	}
	q.Source = filepath.Join(q.Source, "absent")
	r, e = appcore.ScanSourcePreview(context.Background(), q)
	var scanError *fileinventory.ScanError
	if r != nil || !errors.As(e, &scanError) {
		t.Fatal("missing became empty", r, e)
	}
	q = previewFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e = appcore.ScanSourcePreview(ctx, q)
	if r != nil || !errors.Is(e, context.Canceled) {
		t.Fatal(r, e)
	}
	q.Destinations[1].Filters = []taskconfig.Rule{{Kind: "regex", Pattern: filterText("a")}}
	r, e = appcore.ScanSourcePreview(context.Background(), q)
	if r != nil || e == nil {
		t.Fatal("unconfirmed regex granted partial preview")
	}
}
func TestSourcePreviewDraftIsolationAndLateResult(t *testing.T) {
	a := &application{manual: manualSession{source: "unchanged", targetID: "one", targets: []syncengine.ManualDestination{{ID: "one", Filters: []taskconfig.Rule{taskconfig.ExtensionFilter("txt")}}}}}
	q, e := a.previewDraft()
	if e != nil {
		t.Fatal(e)
	}
	*a.manual.targets[0].Filters[0].Extension = "bin"
	if *q.Destinations[0].Filters[0].Extension != "txt" {
		t.Fatal("worker shares draft")
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &sourcePreviewJob{cancel: cancel, generation: 5}
	p := sourcePreviewSession{generation: 5, phase: previewScanning, job: job}
	p.invalidate()
	if ctx.Err() == nil || p.phase != previewStopping {
		t.Fatal("invalidate did not cancel")
	}
	p.finish(job, &appcore.RoutePreview{SourceFiles: 99}, nil)
	if p.job != nil || p.result != nil || p.phase != previewStale {
		t.Fatal("late result installed")
	}
	newer := &sourcePreviewJob{cancel: func() {}, generation: p.generation}
	p.job = newer
	p.phase = previewScanning
	p.finish(job, &appcore.RoutePreview{}, nil)
	if p.job != newer {
		t.Fatal("old callback cleared new job")
	}
}
func TestPreviewExtensionTargetsExplicitGroupAndGuards(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	l, id, e := saveTask(l, "", "Draft", manualFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	a := &application{tasks: l}
	a.selectManual(id)
	a.setFilterMode(appcore.FilterGrouped)
	a.manual.filterEditor.Groups = append(a.manual.filterEditor.Groups, appcore.FilterDraftGroup{Input: "keep typing"})
	a.sourcePreview = sourcePreviewSession{phase: previewReady, result: &appcore.RoutePreview{Extensions: []appcore.PreviewExtension{{Text: "txt", Count: 2}}}}
	a.addPreviewExtension(1, "txt")
	if len(a.manual.filterEditor.Groups[0].Rows) != 0 || len(a.manual.filterEditor.Groups[1].Rows) != 1 || a.manual.filterEditor.Groups[1].Input != "keep typing" || a.sourcePreview.phase != previewStale || !a.manual.dirty {
		t.Fatal("chip lost draft or selected wrong group")
	}
	a.sourcePreview = sourcePreviewSession{phase: previewReady, result: &appcore.RoutePreview{Extensions: []appcore.PreviewExtension{{Text: "txt", Count: 2}}}}
	a.manual.phase = manualRunning
	a.addPreviewExtension(0, "txt")
	if len(a.manual.filterEditor.Groups[0].Rows) != 0 {
		t.Fatal("busy chip edited")
	}
	a.manual.phase = manualDraft
	a.manual.targets[0].Strategy = taskconfig.Mirror
	a.addPreviewExtension(0, "txt")
	if len(a.manual.filterEditor.Groups[0].Rows) != 0 {
		t.Fatal("Mirror chip edited")
	}
}
func TestSourcePreviewCloseAndExitCancelWithoutLosingDraft(t *testing.T) {
	for _, exit := range []bool{false, true} {
		canceled := false
		a := &application{manual: manualSession{dirty: true, source: "keep"}, sourcePreview: sourcePreviewSession{phase: previewScanning, job: &sourcePreviewJob{cancel: func() { canceled = true }}}}
		if exit {
			a.requestQuit()
		} else {
			a.closeTaskEditor()
		}
		if !canceled || a.manual.source != "keep" || !a.manual.dirty {
			t.Fatal("close lost draft")
		}
		if exit && !a.manual.quitPending {
			t.Fatal("exit did not wait for preview")
		}
	}
}
