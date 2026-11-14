package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

func TestSavedTasksCRUDAndReloadKeepLegacyFiles(t *testing.T) {
	data := t.TempDir()
	old := filepath.Join(data, "tasks.json")
	backup := old + ".bak"
	manualWrite(t, old, "legacy untouched")
	manualWrite(t, backup, "legacy backup untouched")
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := manualFixture(t), manualFixture(t)
	l, id1, err := l.save("", "First", q1)
	if err != nil {
		t.Fatal(err)
	}
	l, id2, err := l.save("", "Second", q2)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := l.find(id1)
	destID := first.Destinations[0].ID
	q1.Source = q2.Source
	l, editedID, err := l.save(id1, "Edited", q1)
	if err != nil || editedID != id1 {
		t.Fatal(editedID, err)
	}
	l, err = loadSavedTasks(data)
	if err != nil || len(l.tasks) != 2 {
		t.Fatal(l, err)
	}
	first, err = l.find(id1)
	if err != nil || first.Name != "Edited" || first.SourcePath != q2.Source || first.Destinations[0].ID != destID {
		t.Fatal(first, err)
	}
	if _, err = l.find(id2); err != nil {
		t.Fatal(err)
	}
	l, err = l.remove(id1)
	if err != nil {
		t.Fatal(err)
	}
	l, err = loadSavedTasks(data)
	if err != nil || len(l.tasks) != 1 || l.tasks[0].ID != id2 {
		t.Fatal(l, err)
	}
	manualRead(t, old, "legacy untouched")
	manualRead(t, backup, "legacy backup untouched")
}

func TestSavedTaskUnknownFieldsAndStrategyAreNotDowngraded(t *testing.T) {
	for _, unsupported := range []string{"Task", "Trigger", "Destination", "Target", "Rule", "Strategy"} {
		t.Run(unsupported, func(t *testing.T) {
			data := t.TempDir()
			l, err := loadSavedTasks(data)
			if err != nil {
				t.Fatal(err)
			}
			l, id, err := l.save("", "Future", manualFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(data, savedTaskFile)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var raw []map[string]any
			if err = json.Unmarshal(b, &raw); err != nil {
				t.Fatal(err)
			}
			d := raw[0]["Destinations"].([]any)[0].(map[string]any)
			var object map[string]any
			switch unsupported {
			case "Task":
				object = raw[0]
			case "Trigger":
				object = raw[0]["Trigger"].(map[string]any)
			case "Destination":
				object = d
			case "Target":
				object = d["Target"].(map[string]any)
			case "Rule":
				object = d["Filters"].([]any)[0].(map[string]any)
			case "Strategy":
				d["Strategy"] = "Move"
			}
			if object != nil {
				object["Future"] = json.RawMessage(`{"Counter":900719925474099312345}`)
			}
			b, err = json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			l, err = loadSavedTasks(data)
			if err != nil {
				t.Fatal(err)
			}
			task, _ := l.find(id)
			if _, err = requestFromSaved(task); err == nil {
				t.Fatal("unsupported task runnable")
			}
			if _, _, err = l.save(id, "Changed", manualFixture(t)); err == nil {
				t.Fatal("unsupported task editable")
			}
			if _, err = l.remove(id); err == nil {
				t.Fatal("unsupported task removable")
			}
			l, _, err = l.save("", "Supported", manualFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if unsupported == "Strategy" {
				if !bytes.Contains(b, []byte(`"Move"`)) {
					t.Fatal("strategy lost")
				}
			} else if !bytes.Contains(b, []byte("900719925474099312345")) {
				t.Fatal("unknown data lost")
			}
		})
	}
}

func TestSavedTasksCorruptAndExternalChangeBlockSave(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, savedTaskFile)
	manualWrite(t, path, "damaged")
	l, err := loadSavedTasks(data)
	if err == nil || l != nil {
		t.Fatal(l, err)
	}
	if _, _, err = l.save("", "New", manualFixture(t)); !errors.Is(err, taskstore.ErrBlocked) {
		t.Fatal(err)
	}
	manualRead(t, path, "damaged")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	l, err = loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	l, id, err := l.save("", "Before", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	other, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := other.find(id)
	q, _ := requestFromSaved(task)
	if _, _, err = other.save(id, "External", q); err != nil {
		t.Fatal(err)
	}
	if _, _, err = l.save(id, "Unsaved edit", q); !errors.Is(err, taskstore.ErrChanged) {
		t.Fatal(err)
	}
	if l.tasks[0].Name != "Before" {
		t.Fatal("failed save changed UI library")
	}
	loaded, err := loadSavedTasks(data)
	if err != nil || loaded.tasks[0].Name != "External" {
		t.Fatal(loaded, err)
	}
}

func TestSavedTaskSelectionAndRunningGuards(t *testing.T) {
	l, err := loadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := manualFixture(t), manualFixture(t)
	manualWrite(t, filepath.Join(q1.Source, "first"), "one")
	manualWrite(t, filepath.Join(q2.Source, "second"), "two")
	l, id1, err := l.save("", "First", q1)
	if err != nil {
		t.Fatal(err)
	}
	l, id2, err := l.save("", "Second", q2)
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l}
	a.selectManual(id2)
	if a.manual.id != id2 || a.manual.request.Source != q2.Source || len(a.manual.request.Destinations) != 1 || a.manual.request.Destinations[0].Path != q2.Target {
		t.Fatal(a.manual)
	}
	if _, err = runMultiple(context.Background(), a.manual.request, nil); err != nil {
		t.Fatal(err)
	}
	manualRead(t, filepath.Join(q2.Target, "second"), "two")
	if _, err = os.Stat(filepath.Join(q1.Target, "first")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrong target used", err)
	}
	a.manual.phase = manualRunning
	a.createManual()
	a.confirmDeleteManual()
	a.deleteManual(id2)
	a.newManual()
	a.selectManual(id1)
	a.manual.edited()
	if a.manual.phase != manualRunning || a.manual.id != id2 || a.manual.name != "Second" {
		t.Fatal("running task changed", a.manual)
	}
	if len(a.tasks.tasks) != 2 {
		t.Fatal("running task removed")
	}
	a.manual.phase = manualSaving
	e := &mygo.QuitEvent{}
	a.beforeQuit(e)
	if !e.DefaultPrevented() || !a.manual.quitPending || a.manual.phase != manualSaving {
		t.Fatal("quit skipped storage work")
	}
}

func TestSavedTaskEmptyFilterIsNotAll(t *testing.T) {
	l, err := loadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, id, err := l.save("", "Task", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.find(id)
	task.Destinations[0].Filters = []taskconfig.Rule{}
	if _, err = requestFromSaved(task); err == nil {
		t.Fatal("empty filter downgraded to all")
	}
}

func TestSavedTaskSaveFailureRetainsEditorAndStopsPendingQuit(t *testing.T) {
	l, err := loadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := manualFixture(t)
	id := taskconfig.NewID()
	a := &application{tasks: l, manual: manualSession{phase: manualSaving, name: "Unsaved", source: q.Source, target: q.Target, quitPending: true}}
	fault := errors.New("save fault")
	a.finishTaskSave(nil, id, nil, fault)
	if a.manual.name != "Unsaved" || a.manual.source != q.Source || a.manual.target != q.Target || a.manual.id != id || a.manual.quitPending || a.manual.phase != manualDraft || !errors.Is(a.taskError, fault) {
		t.Fatal(a.manual, a.taskError)
	}
	a.createManual()
	a.startManual() // blocked; neither launches a worker nor uses a window
	if a.manual.phase != manualDraft {
		t.Fatal("write failure was bypassed")
	}
}
