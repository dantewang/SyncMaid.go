package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"testing"

	"syncmaid/internal/taskconfig"
)

func multiFixture(t *testing.T) (syncengine.ManualTaskRequest, syncengine.ManualRequest) {
	t.Helper()
	first := manualFixture(t)
	second := filepath.Join(filepath.Dir(first.Target), "second-target")
	if e := os.Mkdir(second, 0700); e != nil {
		t.Fatal(e)
	}
	q := syncengine.ManualTaskRequest{Source: first.Source, Destinations: []syncengine.ManualDestination{{ID: taskconfig.NewID(), Name: "First", Path: first.Target, Filters: syncengine.AllFileRules()}, {ID: taskconfig.NewID(), Name: "Second", Path: second, Filters: syncengine.AllFileRules()}}}
	return q, first
}
func TestMultipleDestinationsCRUDKeepsIDsAndFields(t *testing.T) {
	data := t.TempDir()
	l, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := saveMultiple(l, "", "Multiple", q)
	if e != nil {
		t.Fatal(e)
	}
	// Preserve an existing supported field the editor does not expose.
	tasks, _ := l.Snapshot.Tasks()
	tasks[0].Destinations[0].VerifyContents = false
	tasks[0].Destinations[0].MassDeleteThreshold = 0.37
	snap, e := l.Snapshot.Save(tasks)
	if e != nil {
		t.Fatal(e)
	}
	l, e = appcore.SavedLibrary(snap)
	if e != nil {
		t.Fatal(e)
	}
	q.Destinations[0], q.Destinations[1] = q.Destinations[1], q.Destinations[0]
	q.Destinations[0].Name = "Renamed"
	l, _, e = saveMultiple(l, id, "Edited", q)
	if e != nil {
		t.Fatal(e)
	}
	reloaded, e := appcore.LoadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	task, e := reloaded.Find(id)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := appcore.MultipleRequestFromSaved(task)
	if e != nil {
		t.Fatal(e)
	}
	if actual.Destinations[0].ID != q.Destinations[0].ID || actual.Destinations[0].Name != "Renamed" || actual.Destinations[1].ID != q.Destinations[1].ID || task.Destinations[1].VerifyContents || task.Destinations[1].MassDeleteThreshold != 0.37 {
		t.Fatal(task)
	}
	q.Destinations = q.Destinations[1:]
	reloaded, _, e = saveMultiple(reloaded, id, "One again", q)
	if e != nil {
		t.Fatal(e)
	}
	task, _ = reloaded.Find(id)
	if len(task.Destinations) != 1 || task.Destinations[0].ID != q.Destinations[0].ID {
		t.Fatal(task)
	}
}

func TestMultipleFailureContinuesHealthyTarget(t *testing.T) {
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f"), "complete")
	if e := os.Remove(q.Destinations[0].Path); e != nil {
		t.Fatal(e)
	}
	r, e := runMultiple(context.Background(), q, nil)
	if e == nil || r[q.Destinations[0].ID].State != syncengine.DestinationFailed || r[q.Destinations[1].ID].State != syncengine.DestinationSucceeded {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(q.Destinations[1].Path, "f"), "complete")
}
func TestMultipleCancelStopsRemainingTargets(t *testing.T) {
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f"), strings.Repeat("x", 1<<20))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := runMultiple(ctx, q, func(r syncengine.DestinationResult) {
		if r.ID == q.Destinations[0].ID && r.Progress.Bytes > 0 {
			cancel()
		}
	})
	if !errors.Is(e, context.Canceled) || r[q.Destinations[0].ID].State != syncengine.DestinationCanceled || r[q.Destinations[1].ID].State != syncengine.DestinationSkipped {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(filepath.Join(q.Destinations[1].Path, "f")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	noTransferTemps(t, q.Destinations[0].Path)
}
func TestMultipleOverlapBlocksBeforeAnyWrite(t *testing.T) {
	for _, kind := range []string{"source", "nested-target", "alias-target", "alias-source"} {
		t.Run(kind, func(t *testing.T) {
			q, first := multiFixture(t)
			manualWrite(t, filepath.Join(first.Source, "f"), "do not copy")
			switch kind {
			case "source":
				q.Destinations[1].Path = first.Source
			case "nested-target":
				q.Destinations[1].Path = filepath.Join(first.Target, "nested")
				if e := os.Mkdir(q.Destinations[1].Path, 0700); e != nil {
					t.Fatal(e)
				}
			case "alias-target":
				alias := filepath.Join(filepath.Dir(first.Target), "alias")
				manualJunction(t, alias, first.Target)
				q.Destinations[1].Path = alias
			case "alias-source":
				alias := filepath.Join(filepath.Dir(first.Target), "alias")
				manualJunction(t, alias, first.Source)
				q.Destinations[1].Path = alias
			}
			r, e := runMultiple(context.Background(), q, nil)
			if !errors.Is(e, syncengine.ErrTransferOverlap) {
				t.Fatal(r, e)
			}
			if _, e = os.Stat(filepath.Join(first.Target, "f")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("wrote before preflight completed", e)
			}
		})
	}
}
func TestMultipleEditorGuardsAndExplicitDiscard(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := saveMultiple(l, "", "Task", q)
	if e != nil {
		t.Fatal(e)
	}
	a := &application{tasks: l}
	a.selectManual(id)
	a.manual.name = "Unsaved"
	a.manual.edited()
	a.newManual()
	a.selectManual(id)
	if !a.manual.dirty || a.manual.name != "Unsaved" {
		t.Fatal("draft silently lost")
	}
	a.discardManual()
	if a.manual.dirty || a.manual.name != "Task" {
		t.Fatal(a.manual)
	}
	a.run = activeRun{phase: runRunning, id: 1, taskID: id}
	before := append([]syncengine.ManualDestination(nil), a.manual.targets...)
	selected := a.manual.targetID
	a.addManualTarget()
	a.removeManualTarget()
	a.moveManualTarget(before[1].ID, -1)
	a.toggleDestinationEditor(before[1].ID)
	if len(a.manual.targets) != len(before) || a.manual.targets[0].ID != before[0].ID || a.manual.targetID != selected || a.run.phase != runRunning {
		t.Fatal(a.manual)
	}
}

func TestMultipleTargetEditorKeepsSelectionByID(t *testing.T) {
	l, e := appcore.LoadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := saveMultiple(l, "", "Task", q)
	if e != nil {
		t.Fatal(e)
	}
	a := &application{tasks: l}
	a.selectManual(id)
	selected := a.manual.targetID
	a.manual.targetName = "Edited first"
	a.manual.edited()
	a.moveManualTarget(q.Destinations[1].ID, -1)
	if a.manual.targetID != selected || a.manual.targets[1].ID != selected || a.manual.targets[1].Name != "Edited first" {
		t.Fatal(a.manual)
	}
	a.addManualTarget()
	if len(a.manual.targets) != 3 || a.manual.targetID == selected {
		t.Fatal(a.manual)
	}
	a.removeManualTarget()
	if len(a.manual.targets) != 2 || a.manual.targets[1].Name != "Edited first" {
		t.Fatal(a.manual)
	}
}
