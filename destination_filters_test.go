package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"testing"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

func editorRules(t *testing.T, e appcore.FilterEditor) []taskconfig.Rule {
	t.Helper()
	rules, err := e.Rules()
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func notRule(r taskconfig.Rule) taskconfig.Rule {
	return taskconfig.Rule{Kind: "not", Rule: &r}
}

func TestDestinationIncludeExcludeSelection(t *testing.T) {
	rules := editorRules(t, appcore.FilterEditor{
		Mode:     appcore.FilterGrouped,
		MatchAll: true,
		Groups: []appcore.FilterDraftGroup{
			{Rows: []taskconfig.Rule{taskconfig.PathFilter("中文"), taskconfig.ExtensionFilter("txt"), taskconfig.WildcardFilter("images/*.jpg")}},
			{All: true, Rows: []taskconfig.Rule{notRule(taskconfig.PathFilter("中文/私密")), notRule(taskconfig.ExtensionFilter("bin")), notRule(taskconfig.WildcardFilter("**/skip.*"))}},
		},
	})
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
	allExcept := editorRules(t, appcore.FilterEditor{
		Mode:   appcore.FilterGrouped,
		Groups: []appcore.FilterDraftGroup{{All: true, Rows: []taskconfig.Rule{{Kind: "all"}, notRule(taskconfig.ExtensionFilter("tmp"))}}},
	})
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
	rules := editorRules(t, appcore.FilterEditor{
		Mode:   appcore.FilterGrouped,
		Groups: []appcore.FilterDraftGroup{{Rows: []taskconfig.Rule{taskconfig.ExtensionFilter("*.*.jpg")}}},
	})
	b, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []taskconfig.Rule
	if err = json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	e := appcore.ProjectNativeFilter(decoded)
	if e.Mode != appcore.FilterGrouped {
		t.Fatal(e)
	}
	e.MatchAll = true
	e.Groups = append(e.Groups, appcore.FilterDraftGroup{All: true, Rows: []taskconfig.Rule{notRule(taskconfig.PathFilter("ignored"))}})
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
	deep := syncengine.FilterGroup("allOf", []taskconfig.Rule{syncengine.FilterGroup("anyOf", []taskconfig.Rule{syncengine.FilterGroup("allOf", []taskconfig.Rule{taskconfig.ExtensionFilter("txt")})})})
	for _, rules := range [][]taskconfig.Rule{{deep}, {{Kind: "regex", Pattern: filterText("a+")}}} {
		e := appcore.ProjectNativeFilter(rules)
		if e.Mode != appcore.FilterReadOnly || e.Reason == "" {
			t.Fatal(e)
		}
	}
}
func filterText(s string) *string { return &s }

func TestFilterMatchErrorReturnsNoPartialSelection(t *testing.T) {
	selected, err := syncengine.SelectFilteredFiles(context.Background(), []fileinventory.File{{Path: "good.txt"}, {Path: "bad\x00.txt"}}, syncengine.AllFileRules())
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
	q.Destinations[0].Filters = editorRules(t, appcore.FilterEditor{
		Mode:     appcore.FilterGrouped,
		MatchAll: true,
		Groups: []appcore.FilterDraftGroup{
			{Rows: []taskconfig.Rule{taskconfig.PathFilter("中文"), taskconfig.ExtensionFilter("txt")}},
			{All: true, Rows: []taskconfig.Rule{notRule(taskconfig.ExtensionFilter("bin")), notRule(taskconfig.WildcardFilter("**/skip.txt"))}},
		},
	})
	q.Destinations[1].Filters = editorRules(t, appcore.FilterEditor{
		Mode:   appcore.FilterGrouped,
		Groups: []appcore.FilterDraftGroup{{Rows: []taskconfig.Rule{taskconfig.ExtensionFilter("bin")}}},
	})
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
	if !errors.Is(err, filefilter.ErrUnsupportedRegex) || r[q.Destinations[0].ID].State != syncengine.DestinationFailed || r[q.Destinations[1].ID].State != syncengine.DestinationSucceeded {
		t.Fatal(r, err)
	}
	if entries, err := os.ReadDir(first.Target); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	manualRead(t, filepath.Join(q.Destinations[1].Path, "f.txt"), "source")
}

func TestNoMatchIsNotEmptySourceAndNeverDeletes(t *testing.T) {
	for _, mode := range []appcore.FilterMode{appcore.FilterNone, appcore.FilterGrouped} {
		q, first := multiFixture(t)
		q.Destinations = q.Destinations[:1]
		manualWrite(t, filepath.Join(first.Source, "a.txt"), "input")
		manualWrite(t, filepath.Join(first.Target, "a.txt"), "unchanged")
		editor := appcore.FilterEditor{Mode: mode}
		if mode == appcore.FilterGrouped {
			editor.Groups = []appcore.FilterDraftGroup{{Rows: []taskconfig.Rule{taskconfig.ExtensionFilter("absent")}}}
		}
		q.Destinations[0].Filters = editorRules(t, editor)
		r, err := runMultiple(context.Background(), q, nil)
		result := r[q.Destinations[0].ID]
		if err != nil || result.State != syncengine.DestinationNoMatches || result.Progress.Total != 0 || result.Progress.SourceFiles != 1 || result.Progress.SourceEmpty {
			t.Fatal(r, err)
		}
		manualRead(t, filepath.Join(first.Target, "a.txt"), "unchanged")
	}
}

func TestFilteringNeverSkipsRawScanSafety(t *testing.T) {
	q, first := multiFixture(t)
	q.Destinations = q.Destinations[:1]
	q.Destinations[0].Filters = editorRules(t, appcore.FilterEditor{Mode: appcore.FilterNone})
	manualJunction(t, filepath.Join(first.Source, "excluded-link"), t.TempDir())
	r, err := runMultiple(context.Background(), q, nil)
	if err == nil || r[q.Destinations[0].ID].State != syncengine.DestinationFailed {
		t.Fatal(r, err)
	}
}

func TestFilterDraftBindingErrorAndReload(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := multiFixture(t)
	l, id, err := saveMultiple(l, "", "Filters", q)
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l}
	a.selectManual(id)
	first, second := a.manual.targets[0].ID, a.manual.targets[1].ID
	a.setFilterMode(appcore.FilterGrouped)
	a.manual.filterEditor.Groups[0].Kind = 1
	a.manual.filterEditor.Groups[0].Input = "."
	a.addFilterRule(0)
	if a.manual.filterDraftError() == nil {
		t.Fatal("invalid draft accepted")
	}
	a.createManual()
	if a.manual.phase != manualDraft || a.taskError != nil {
		t.Fatal("invalid draft wrote or blocked disk unnecessarily")
	}
	a.manual.selectTarget(second)
	a.setFilterMode(appcore.FilterGrouped)
	a.manual.filterEditor.Groups[0].Kind = 2
	a.manual.filterEditor.Groups[0].Input = "**/*.bin"
	a.addFilterRule(0)
	a.moveManualTarget(second, -1)
	a.manual.selectTarget(first)
	if a.manual.filterEditor.Groups[0].Input != "." || a.manual.filterEditor.Error == "" {
		t.Fatal("invalid draft lost on switch")
	}
	a.manual.filterEditor.Error = ""
	a.manual.filterEditor.Groups[0].Input = "txt"
	a.addFilterRule(0)
	l, _, err = saveMultiple(l, id, "Filters", a.manual.draftRequest())
	if err != nil {
		t.Fatal(err)
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.Find(id)
	request, err := appcore.MultipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	firstProj := appcore.ProjectNativeFilter(request.Destinations[0].Filters)
	secondProj := appcore.ProjectNativeFilter(request.Destinations[1].Filters)
	if request.Destinations[0].ID != second || *firstProj.Groups[0].Rows[0].Pattern != "**/*.bin" || request.Destinations[1].ID != first || *secondProj.Groups[0].Rows[0].Extension != "txt" {
		t.Fatal(request)
	}
	a.manual.phase = manualRunning
	before := a.manual.targets[1].Filters
	a.setFilterMode(appcore.FilterNone)
	a.filterEdited()
	if !syncengine.SameFilterRules(before, a.manual.targets[1].Filters) || a.manual.phase != manualRunning {
		t.Fatal("running guard bypassed")
	}
}

func TestReadOnlyUnknownFiltersPreservedAndIsolated(t *testing.T) {
	data := t.TempDir()
	l, err := appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, first := multiFixture(t)
	manualWrite(t, filepath.Join(first.Source, "f.txt"), "healthy-input")
	l, id, err := saveMultiple(l, "", "Future", q)
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := l.Snapshot.Tasks()
	var unknown taskconfig.Rule
	if err = json.Unmarshal([]byte(`{"kind":"extension","Extension":"txt","Future":900719925474099312345}`), &unknown); err != nil {
		t.Fatal(err)
	}
	tasks[0].Destinations[0].Filters = []taskconfig.Rule{unknown}
	snap, err := l.Snapshot.Save(tasks)
	if err != nil {
		t.Fatal(err)
	}
	l, err = appcore.SavedLibrary(snap)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.Find(id)
	request, err := appcore.MultipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	form := appcore.ProjectNativeFilter(request.Destinations[0].Filters)
	if form.Mode != appcore.FilterReadOnly || form.RunError == "" {
		t.Fatal(form)
	}
	request.Destinations[1].Name = "Rename healthy"
	l, _, err = saveMultiple(l, id, "Metadata edit", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Remove(id); err == nil {
		t.Fatal("unsupported record deleted")
	}
	badEdit := request
	badEdit.Destinations = append([]syncengine.ManualDestination(nil), request.Destinations...)
	badEdit.Destinations[0].Filters = syncengine.AllFileRules()
	if _, _, err = saveMultiple(l, id, "Bad rewrite", badEdit); err == nil {
		t.Fatal("unknown rule rewritten")
	}
	l, err = appcore.LoadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = l.Find(id)
	if !task.Destinations[0].Filters[0].HasUnknownFields() {
		t.Fatal("unknown lost")
	}
	request, err = appcore.MultipleRequestFromSaved(task)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runMultiple(context.Background(), request, nil)
	if !errors.Is(err, filefilter.ErrUnknownFields) || r[request.Destinations[1].ID].State != syncengine.DestinationSucceeded {
		t.Fatal(r, err)
	}
	if entries, e := os.ReadDir(first.Target); e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}
