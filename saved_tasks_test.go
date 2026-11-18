package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syncmaid/internal/appcore"
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
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := manualFixture(t), manualFixture(t)
	l, id1, err := saveTask(l, "", "First", q1)
	if err != nil {
		t.Fatal(err)
	}
	l, id2, err := saveTask(l, "", "Second", q2)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := l.Find(id1)
	destID := first.Destinations[0].ID
	q1.Source = manualFixture(t).Source
	l, editedID, err := saveTask(l, id1, "Edited", q1)
	if err != nil || editedID != id1 {
		t.Fatal(editedID, err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil || len(l.Tasks) != 2 {
		t.Fatal(l, err)
	}
	first, err = l.Find(id1)
	if err != nil || first.Name != "Edited" || first.SourcePath != q1.Source || first.Destinations[0].ID != destID {
		t.Fatal(first, err)
	}
	if _, err = l.Find(id2); err != nil {
		t.Fatal(err)
	}
	l, err = l.Remove(id1)
	if err != nil {
		t.Fatal(err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil || len(l.Tasks) != 1 || l.Tasks[0].ID != id2 {
		t.Fatal(l, err)
	}
	manualRead(t, old, "legacy untouched")
	manualRead(t, backup, "legacy backup untouched")
}

func TestSavedTaskUnknownFieldsAndStrategyAreNotDowngraded(t *testing.T) {
	for _, unsupported := range []string{"Task", "Trigger", "Destination", "Target", "Rule", "Strategy"} {
		t.Run(unsupported, func(t *testing.T) {
			data := t.TempDir()
			l, err := appcore.LoadSavedTasks(data)
			if err != nil {
				t.Fatal(err)
			}
			l, id, err := saveTask(l, "", "Future", manualFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(data, appcore.SavedTaskFile)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var raw []map[string]any
			var document map[string]json.RawMessage
			if err = json.Unmarshal(b, &document); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(document["Tasks"], &raw); err != nil {
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
			document["Tasks"] = b
			b, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			l, err = appcore.LoadSavedTasks(data)
			if err != nil {
				t.Fatal(err)
			}
			task, _ := l.Find(id)
			if _, err = requestFromSaved(task); err == nil {
				t.Fatal("unsupported task runnable")
			}
			if _, _, err = saveTask(l, id, "Changed", manualFixture(t)); err == nil {
				t.Fatal("unsupported task editable")
			}
			if _, err = l.Remove(id); err == nil {
				t.Fatal("unsupported task removable")
			}
			l, _, err = saveTask(l, "", "Supported", manualFixture(t))
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
	path := filepath.Join(data, appcore.SavedTaskFile)
	manualWrite(t, path, "damaged")
	l, err := appcore.LoadSavedTasks(data)
	if err == nil || l != nil {
		t.Fatal(l, err)
	}
	if _, _, err = saveTask(l, "", "New", manualFixture(t)); !errors.Is(err, taskstore.ErrBlocked) {
		t.Fatal(err)
	}
	manualRead(t, path, "damaged")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	l, id, err := saveTask(l, "", "Before", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	other, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := other.Find(id)
	q, _ := requestFromSaved(task)
	if _, _, err = saveTask(other, id, "External", q); err != nil {
		t.Fatal(err)
	}
	if _, _, err = saveTask(l, id, "Unsaved edit", q); !errors.Is(err, taskstore.ErrChanged) {
		t.Fatal(err)
	}
	if l.Tasks[0].Name != "Before" {
		t.Fatal("failed save changed UI library")
	}
	loaded, err := appcore.LoadSavedTasks(data)
	if err != nil || loaded.Tasks[0].Name != "External" {
		t.Fatal(loaded, err)
	}
}

func TestSavedTaskSelectionAndRunningGuards(t *testing.T) {
	l, err := appcore.LoadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := manualFixture(t), manualFixture(t)
	manualWrite(t, filepath.Join(q1.Source, "first"), "one")
	manualWrite(t, filepath.Join(q2.Source, "second"), "two")
	l, id1, err := saveTask(l, "", "First", q1)
	if err != nil {
		t.Fatal(err)
	}
	l, id2, err := saveTask(l, "", "Second", q2)
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
	if len(a.tasks.Tasks) != 2 {
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
	l, err := appcore.LoadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, id, err := saveTask(l, "", "Task", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.Find(id)
	task.Destinations[0].Filters = []taskconfig.Rule{}
	if _, err = requestFromSaved(task); err == nil {
		t.Fatal("empty filter downgraded to all")
	}
}

func TestSavedTaskSaveFailureRetainsEditorAndStopsPendingQuit(t *testing.T) {
	l, err := appcore.LoadSavedTasks(t.TempDir())
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
