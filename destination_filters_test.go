package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

func editorRules(t *testing.T, e filterEditor) []taskconfig.Rule {
	t.Helper()
	rules, err := e.rules()
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestDestinationIncludeExcludeSelection(t *testing.T) {
	rules := editorRules(t, filterEditor{Mode: filterCustom, IncludePaths: "中文", IncludeExtensions: "txt", IncludeGlobs: "images/*.jpg", ExcludePaths: "中文/私密", ExcludeExtensions: "bin", ExcludeGlobs: "**/skip.*"})
	m, err := filefilter.Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want bool
	}{{"中文/a.dat", true}, {"中文/私密/a.txt", false}, {"outside.txt", true}, {"skip.txt", false}, {"images/p.jpg", true}, {"images/deep/p.jpg", false}, {"other.bin", false}, {"中文/a.bin", false}} {
		if got, err := m.Match(c.path); err != nil || got != c.want {
			t.Fatal(c, got, err)
		}
	}
	allExcept := editorRules(t, filterEditor{Mode: filterCustom, ExcludeExtensions: "tmp"})
	m, err = filefilter.Compile(allExcept)
	if err != nil {
		t.Fatal(err)
	}
	if yes, err := m.Match("任意/a.txt"); !yes || err != nil {
		t.Fatal(yes, err)
	}
	if yes, err := m.Match("a.tmp"); yes || err != nil {
		t.Fatal(yes, err)
	}
}

func TestFilterProjectionKeepsOnePassExtensionAndReadOnlyShapes(t *testing.T) {
	rules := editorRules(t, filterEditor{Mode: filterCustom, IncludeExtensions: "*.*.jpg"})
	b, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []taskconfig.Rule
	if err = json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	e := projectFilterEditor(decoded)
	if e.Mode != filterCustom {
		t.Fatal(e)
	}
	e.ExcludePaths = "ignored"
	again := editorRules(t, e)
	m, err := filefilter.Compile(again)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want bool
	}{{"a.*.jpg", true}, {"a.jpg", false}, {"ignored/a.*.jpg", false}} {
		if yes, err := m.Match(c.path); err != nil || yes != c.want {
			t.Fatal(c, yes, err)
		}
	}
	for _, rules := range [][]taskconfig.Rule{{taskconfig.ExtensionFilter("txt")}, {{Kind: "regex", Pattern: filterText("a+")}}} {
		e := projectFilterEditor(rules)
		if e.Mode != filterReadOnly || e.Reason == "" {
			t.Fatal(e)
		}
	}
	if _, err := (filterEditor{Mode: filterCustom, IncludeExtensions: "."}).rules(); err == nil {
		t.Fatal("empty normalized extension accepted")
	}
}
func filterText(s string) *string { return &s }

func TestFilterMatchErrorReturnsNoPartialSelection(t *testing.T) {
	selected, err := selectFilteredFiles(context.Background(), []fileinventory.File{{Path: "good.txt"}, {Path: "bad\x00.txt"}}, allFileRules())
	if !errors.Is(err, filefilter.ErrInvalidInput) || selected != nil {
		t.Fatal(selected, err)
	}
}

func TestFilteredTransfersOnlyChangeMatchedFiles(t *testing.T) {
	q, first := multiFixture(t)
	for p, text := range map[string]string{"中文/update.txt": "new-matching-content", "中文/no.bin": "bin-content", "new.txt": "new-txt", "skip.txt": "excluded-txt", "other.bin": "other-bin"} {
		manualWrite(t, filepath.Join(first.Source, filepath.FromSlash(p)), text)
	}
	manualWrite(t, filepath.Join(first.Target, "中文/update.txt"), "old")
	manualWrite(t, filepath.Join(first.Target, "other.bin"), "keep-filtered-old")
	manualWrite(t, filepath.Join(q.Destinations[1].Path, "new.txt"), "keep-other-target-txt")
	q.Destinations[0].Filters = editorRules(t, filterEditor{Mode: filterCustom, IncludePaths: "中文", IncludeExtensions: "txt", ExcludeExtensions: "bin", ExcludeGlobs: "**/skip.txt"})
	q.Destinations[1].Filters = editorRules(t, filterEditor{Mode: filterCustom, IncludeExtensions: "bin"})
	r, err := runMultiple(context.Background(), q, nil)
	if err != nil {
		t.Fatal(r, err)
	}
	if r[q.Destinations[0].ID].Progress.Total != 2 || r[q.Destinations[1].ID].Progress.Total != 2 {
		t.Fatal(r)
	}
	manualRead(t, filepath.Join(first.Target, "中文/update.txt"), "new-matching-content")
	manualRead(t, filepath.Join(first.Target, "new.txt"), "new-txt")
	manualRead(t, filepath.Join(first.Target, "other.bin"), "keep-filtered-old")
	if _, err = os.Stat(filepath.Join(first.Target, "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	manualRead(t, filepath.Join(q.Destinations[1].Path, "other.bin"), "other-bin")
	manualRead(t, filepath.Join(q.Destinations[1].Path, "中文/no.bin"), "bin-content")
	manualRead(t, filepath.Join(q.Destinations[1].Path, "new.txt"), "keep-other-target-txt")
}

func TestFilteredCompileFailureDoesNotWriteAndHealthyTargetContinues(t *testing.T) {
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f.txt"), "source")
	// Eager compilation must reject this even though the first OR arm is all.
	q.Destinations[0].Filters = []taskconfig.Rule{{Kind: "all"}, {Kind: "regex", Pattern: filterText("a+")}}
	r, err := runMultiple(context.Background(), q, nil)
	if !errors.Is(err, filefilter.ErrUnsupportedRegex) || r[q.Destinations[0].ID].State != destinationFailed || r[q.Destinations[1].ID].State != destinationSucceeded {
		t.Fatal(r, err)
	}
	if entries, err := os.ReadDir(first.Target); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	manualRead(t, filepath.Join(q.Destinations[1].Path, "f.txt"), "source")
}

func TestNoMatchIsNotEmptySourceAndNeverDeletes(t *testing.T) {
	for _, mode := range []filterMode{filterNone, filterCustom} {
		q, first := multiFixture(t)
		q.Destinations = q.Destinations[:1]
		manualWrite(t, filepath.Join(first.Source, "a.txt"), "input")
		manualWrite(t, filepath.Join(first.Target, "a.txt"), "unchanged")
		q.Destinations[0].Filters = editorRules(t, filterEditor{Mode: mode, IncludeExtensions: "absent"})
		r, err := runMultiple(context.Background(), q, nil)
		result := r[q.Destinations[0].ID]
		if err != nil || result.State != destinationNoMatches || result.Progress.Total != 0 || result.Progress.SourceFiles != 1 || result.Progress.SourceEmpty {
			t.Fatal(r, err)
		}
		manualRead(t, filepath.Join(first.Target, "a.txt"), "unchanged")
	}
}

func TestFilteringNeverSkipsRawScanSafety(t *testing.T) {
	q, first := multiFixture(t)
	q.Destinations = q.Destinations[:1]
	q.Destinations[0].Filters = editorRules(t, filterEditor{Mode: filterNone})
	manualJunction(t, filepath.Join(first.Source, "excluded-link"), t.TempDir())
	r, err := runMultiple(context.Background(), q, nil)
	if err == nil || r[q.Destinations[0].ID].State != destinationFailed {
		t.Fatal(r, err)
	}
}

func TestFilterDraftBindingErrorAndReload(t *testing.T) {
	data := t.TempDir()
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := multiFixture(t)
	l, id, err := l.saveMultiple("", "Filters", q)
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l}
	a.selectManual(id)
	first, second := a.manual.targets[0].ID, a.manual.targets[1].ID
	a.setFilterMode(filterCustom)
	a.manual.filterEditor.IncludeExtensions = "."
	a.filterEdited()
	if a.manual.filterDraftError() == nil {
		t.Fatal("invalid draft accepted")
	}
	a.createManual()
	if a.manual.phase != manualDraft || a.taskError != nil {
		t.Fatal("invalid draft wrote or blocked disk unnecessarily")
	}
	a.manual.selectTarget(second)
	a.setFilterMode(filterCustom)
	a.manual.filterEditor.IncludeGlobs = "**/*.bin"
	a.filterEdited()
	a.moveManualTarget(second, -1)
	a.manual.selectTarget(first)
	if a.manual.filterEditor.IncludeExtensions != "." || a.manual.filterEditor.Error == "" {
		t.Fatal("invalid draft lost on switch")
	}
	a.manual.filterEditor.IncludeExtensions = "txt"
	a.filterEdited()
	l, _, err = l.saveMultiple(id, "Filters", a.manual.draftRequest())
	if err != nil {
		t.Fatal(err)
	}
	l, err = loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.find(id)
	request, err := multipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	if request.Destinations[0].ID != second || projectFilterEditor(request.Destinations[0].Filters).IncludeGlobs != "**/*.bin" || request.Destinations[1].ID != first || projectFilterEditor(request.Destinations[1].Filters).IncludeExtensions != "txt" {
		t.Fatal(request)
	}
	a.manual.phase = manualRunning
	before := a.manual.targets[1].Filters
	a.setFilterMode(filterNone)
	a.filterEdited()
	if !sameFilterRules(before, a.manual.targets[1].Filters) || a.manual.phase != manualRunning {
		t.Fatal("running guard bypassed")
	}
}

func TestReadOnlyUnknownFiltersPreservedAndIsolated(t *testing.T) {
	data := t.TempDir()
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f.txt"), "healthy-input")
	l, id, err := l.saveMultiple("", "Future", q)
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := l.snapshot.Tasks()
	var unknown taskconfig.Rule
	if err = json.Unmarshal([]byte(`{"kind":"extension","Extension":"txt","Future":900719925474099312345}`), &unknown); err != nil {
		t.Fatal(err)
	}
	tasks[0].Destinations[0].Filters = []taskconfig.Rule{unknown}
	snap, err := l.snapshot.Save(tasks)
	if err != nil {
		t.Fatal(err)
	}
	l, err = savedLibrary(snap)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.find(id)
	request, err := multipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	form := projectFilterEditor(request.Destinations[0].Filters)
	if form.Mode != filterReadOnly || form.RunError == "" {
		t.Fatal(form)
	}
	request.Destinations[1].Name = "Rename healthy"
	l, _, err = l.saveMultiple(id, "Metadata edit", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.remove(id); err == nil {
		t.Fatal("unsupported record deleted")
	}
	badEdit := request
	badEdit.Destinations = append([]manualDestination(nil), request.Destinations...)
	badEdit.Destinations[0].Filters = allFileRules()
	if _, _, err = l.saveMultiple(id, "Bad rewrite", badEdit); err == nil {
		t.Fatal("unknown rule rewritten")
	}
	l, err = loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = l.find(id)
	if !task.Destinations[0].Filters[0].HasUnknownFields() {
		t.Fatal("unknown lost")
	}
	request, err = multipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runMultiple(context.Background(), request, nil)
	if !errors.Is(err, filefilter.ErrUnknownFields) || r[request.Destinations[1].ID].State != destinationSucceeded {
		t.Fatal(r, err)
	}
	if entries, e := os.ReadDir(first.Target); e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}
