package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func multiFixture(t *testing.T) (manualTaskRequest, manualRequest) {
	t.Helper()
	first := manualFixture(t)
	second := filepath.Join(filepath.Dir(first.Target), "second-target")
	if e := os.Mkdir(second, 0700); e != nil {
		t.Fatal(e)
	}
	q := manualTaskRequest{Source: first.Source, Destinations: []manualDestination{{ID: taskconfig.NewID(), Name: "First", Path: first.Target}, {ID: taskconfig.NewID(), Name: "Second", Path: second}}}
	return q, first
}
func TestMultipleDestinationsCRUDKeepsIDsAndFields(t *testing.T) {
	data := t.TempDir()
	l, e := loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := l.saveMultiple("", "Multiple", q)
	if e != nil {
		t.Fatal(e)
	}
	// Preserve an existing supported field the editor does not expose.
	tasks, _ := l.snapshot.Tasks()
	tasks[0].Destinations[0].VerifyContents = false
	tasks[0].Destinations[0].MassDeleteThreshold = 0.37
	snap, e := l.snapshot.Save(tasks)
	if e != nil {
		t.Fatal(e)
	}
	l, e = savedLibrary(snap)
	if e != nil {
		t.Fatal(e)
	}
	q.Destinations[0], q.Destinations[1] = q.Destinations[1], q.Destinations[0]
	q.Destinations[0].Name = "Renamed"
	l, _, e = l.saveMultiple(id, "Edited", q)
	if e != nil {
		t.Fatal(e)
	}
	reloaded, e := loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	task, e := reloaded.find(id)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := multipleRequestFromSaved(task)
	if e != nil {
		t.Fatal(e)
	}
	if actual.Destinations[0].ID != q.Destinations[0].ID || actual.Destinations[0].Name != "Renamed" || actual.Destinations[1].ID != q.Destinations[1].ID || task.Destinations[1].VerifyContents || task.Destinations[1].MassDeleteThreshold != 0.37 {
		t.Fatal(task)
	}
	q.Destinations = q.Destinations[1:]
	reloaded, _, e = reloaded.saveMultiple(id, "One again", q)
	if e != nil {
		t.Fatal(e)
	}
	task, _ = reloaded.find(id)
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
	if e == nil || r[q.Destinations[0].ID].State != destinationFailed || r[q.Destinations[1].ID].State != destinationSucceeded {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(q.Destinations[1].Path, "f"), "complete")
}
func TestMultipleCancelStopsRemainingTargets(t *testing.T) {
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f"), strings.Repeat("x", 1<<20))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := runMultiple(ctx, q, func(r destinationResult) {
		if r.ID == q.Destinations[0].ID && r.Progress.Bytes > 0 {
			cancel()
		}
	})
	if !errors.Is(e, context.Canceled) || r[q.Destinations[0].ID].State != destinationCanceled || r[q.Destinations[1].ID].State != destinationSkipped {
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
			if !errors.Is(e, errTransferOverlap) {
				t.Fatal(r, e)
			}
			if _, e = os.Stat(filepath.Join(first.Target, "f")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("wrote before preflight completed", e)
			}
		})
	}
}
func TestMultipleEditorGuardsAndExplicitDiscard(t *testing.T) {
	l, e := loadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := l.saveMultiple("", "Task", q)
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
	a.manual.phase = manualRunning
	before := append([]manualDestination(nil), a.manual.targets...)
	selected := a.manual.targetID
	a.addManualTarget()
	a.removeManualTarget()
	a.moveManualTarget(before[1].ID, -1)
	a.manual.selectTarget(before[1].ID)
	a.discardManual()
	if len(a.manual.targets) != len(before) || a.manual.targets[0].ID != before[0].ID || a.manual.targetID != selected || a.manual.phase != manualRunning {
		t.Fatal(a.manual)
	}
}

func TestMultipleTargetEditorKeepsSelectionByID(t *testing.T) {
	l, e := loadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	q, _ := multiFixture(t)
	l, id, e := l.saveMultiple("", "Task", q)
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
