package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

func legacyFixture(t *testing.T, q syncengine.ManualRequest) []byte {
	t.Helper()
	obj := []map[string]any{{"Name": "Imported fixture", "SourcePath": q.Source, "Trigger": map[string]any{"kind": "watch", "SettleSeconds": 10}, "Kind": "Sync", "Destinations": []map[string]any{{"Name": "Destination", "Target": map[string]any{"kind": "local", "Path": q.Target}, "Strategy": "AddOnly", "Filters": []map[string]any{{"kind": "all"}}}}}}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestLegacyImportAppendSnapshotAndRestart(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q := manualFixture(t)
	l, existing, err := saveTask(l, "", "Existing", q)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(t.TempDir(), "tasks.json")
	b := legacyFixture(t, manualFixture(t))
	os.WriteFile(old, b, 0600)
	os.WriteFile(old+".bak", []byte("backup bytes"), 0600)
	p, err := appcore.ReadLegacyImport(old, l)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tasks[0].Trigger.Kind != "manual" || p.Issues[0] != nil {
		t.Fatal("not safely prepared", p.Issues)
	}
	// Changing the selected file cannot change what confirmation saves.
	os.WriteFile(old, []byte("changed externally"), 0600)
	l, err = l.AppendImport(p)
	if err != nil {
		t.Fatal(err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil || len(l.Tasks) != 2 {
		t.Fatal(err)
	}
	if _, err = l.Find(existing); err != nil {
		t.Fatal("existing task lost")
	}
	imported, err := l.Find(p.Tasks[0].ID)
	if err != nil || imported.Trigger.Kind != "manual" {
		t.Fatal(imported, err)
	}
	if _, err = l.AppendImport(p); err == nil {
		t.Fatal("same preview appended twice")
	}
	os.WriteFile(old, b, 0600)
	if _, err = appcore.ReadLegacyImport(old, l); err == nil || !strings.Contains(err.Error(), "duplicate task content") {
		t.Fatal("missing-ID repeated import allowed", err)
	}
	actual, _ := os.ReadFile(old)
	backup, _ := os.ReadFile(old + ".bak")
	if !bytes.Equal(actual, b) || string(backup) != "backup bytes" {
		t.Fatal("legacy source touched")
	}
	q2, err := appcore.MultipleRequestFromSaved(imported)
	if err != nil {
		t.Fatal(err)
	}
	_, err = syncengine.RunMultipleConfirmed(context.Background(), q2, nil, nil, nil)
	if err != nil {
		t.Fatal("manual imported task cannot run", err)
	}
}
func TestLegacyImportPreservesUnknownAndRegex(t *testing.T) {
	l, _ := appcore.LoadSavedTasks(t.TempDir())
	q := manualFixture(t)
	b := legacyFixture(t, q)
	b = bytes.Replace(b, []byte(`"Trigger":{"SettleSeconds":10,"kind":"watch"}`), []byte(`"Trigger":{"kind":"manual","Future":900719925474099312345}`), 1)
	b = bytes.Replace(b, []byte(`"Filters":[{"kind":"all"}]`), []byte(`"Filters":[{"kind":"regex","Pattern":"\\p{L}+","Future":900719925474099312345}]`), 1)
	path := filepath.Join(t.TempDir(), "tasks.json")
	os.WriteFile(path, b, 0600)
	p, err := appcore.ReadLegacyImport(path, l)
	if err != nil {
		t.Fatal(err)
	}
	if p.Issues[0] == nil {
		t.Fatal("unknown semantics not visible")
	}
	l, err = l.AppendImport(p)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := taskconfig.Encode(l.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("900719925474099312345")) || !bytes.Contains(encoded, []byte(`"kind": "regex"`)) {
		t.Fatal("unknown/regex data lost", string(encoded))
	}
	pattern := "x"
	if _, err := filefilter.Compile([]taskconfig.Rule{{Kind: "regex", Pattern: &pattern}}); !errors.Is(err, filefilter.ErrUnsupportedRegex) {
		t.Fatal(err)
	}
	a := application{tasks: l}
	a.syncWatches()
	if len(a.watches) != 0 {
		t.Fatal("import subscribed automatically")
	}
}
func TestLegacyImportRejectsWholeBadOrDuplicateDocument(t *testing.T) {
	l, _ := appcore.LoadSavedTasks(t.TempDir())
	b := legacyFixture(t, manualFixture(t))
	var document []map[string]any
	json.Unmarshal(b, &document)
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"bad", []byte(`[{`)}, {"empty", []byte(`[]`)},
		{"unknown strategy", bytes.Replace(b, []byte(`"AddOnly"`), []byte(`"FutureStrategy"`), 1)},
		{"unknown auto trigger member", bytes.Replace(b, []byte(`"SettleSeconds":10`), []byte(`"SettleSeconds":10,"Future":42`), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.json")
			os.WriteFile(path, tc.body, 0600)
			if p, err := appcore.ReadLegacyImport(path, l); err == nil || p != nil {
				t.Fatal("accepted invalid file")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, tc.body) {
				t.Fatal("source modified")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "tasks.json")
	os.WriteFile(path, b, 0600)
	p, err := appcore.ReadLegacyImport(path, l)
	if err != nil {
		t.Fatal(err)
	}
	p.Tasks = append(p.Tasks, p.Tasks[0])
	if l.CheckImport(p) == nil {
		t.Fatal("duplicate IDs accepted")
	}
	if len(l.Tasks) != 0 {
		t.Fatal("rejected input altered library")
	}
}
func TestLegacyImportCancelAndFailedSaveKeepLibrary(t *testing.T) {
	data := t.TempDir()
	l, _ := appcore.LoadSavedTasks(data)
	path := filepath.Join(t.TempDir(), "tasks.json")
	os.WriteFile(path, legacyFixture(t, manualFixture(t)), 0600)
	p, err := appcore.ReadLegacyImport(path, l)
	if err != nil {
		t.Fatal(err)
	}
	a := application{tasks: l, importPreview: p, importPrevious: manualReady, manual: manualSession{phase: manualImporting}}
	a.cancelImport()
	if a.importPreview != nil || a.manual.phase != manualReady || len(a.tasks.Tasks) != 0 {
		t.Fatal("cancel mutated tasks")
	}
	// External creation conflicts with the missing snapshot and is never overwritten.
	disk := []byte("broken existing content")
	os.WriteFile(filepath.Join(data, appcore.SavedTaskFile), disk, 0600)
	if next, err := l.AppendImport(p); err == nil || next != nil {
		t.Fatal("write failure hidden")
	}
	got, _ := os.ReadFile(filepath.Join(data, appcore.SavedTaskFile))
	if !bytes.Equal(got, disk) || len(l.Tasks) != 0 {
		t.Fatal("failed save damaged old library")
	}
	for _, tag := range []string{"en", "ja", "zh-Hans", "zh-Hant"} {
		a := application{locale: tag, tasks: l, importPreview: p, manual: manualSession{phase: manualImporting}, taskError: errors.New("save failed"), importError: errors.New("save failed")}
		view := ui.NewTester(a.view, 1000, 1200)
		if !view.HasText(a.tr("旧任务导入预览")) {
			t.Fatal(tag, "preview not visible")
		}
		a.confirmImport()
		if a.importPreview != p || a.manual.phase != manualImporting {
			t.Fatal("failed preview resubmitted")
		}
	}
}

func TestCancelImportHonorsPendingQuit(t *testing.T) {
	a := &application{importPreview: &appcore.LegacyImport{}, importPrevious: manualReady, manual: manualSession{phase: manualImporting, quitPending: true}}
	quits := 0
	a.lifecycle.ready(&recordedWindow{}, func() { quits++ }, nil)
	a.cancelImport()
	if quits != 1 || a.lifecycle.phase != exiting || a.importPreview != nil || a.manual.phase != manualReady {
		t.Fatal("cancel lost pending quit")
	}
}
