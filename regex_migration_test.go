package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

func migrationFixture(t *testing.T, move bool) (*savedTaskLibrary, taskconfig.Task, manualRequest, string) {
	t.Helper()
	q, paths := multiFixture(t)
	kind := taskconfig.Sync
	if move {
		kind = taskconfig.Moving
	}
	task := taskconfig.Task{ID: taskconfig.NewID(), Name: "Regex fixture", SourcePath: q.Source, Kind: kind, Trigger: taskconfig.Trigger{Kind: "manual"}}
	for i, d := range q.Destinations {
		strategy := taskconfig.AddOnly
		if move {
			strategy = taskconfig.Move
		}
		rules := allFileRules()
		if i == 0 {
			pattern := "legacy"
			rules = []taskconfig.Rule{{Kind: "regex", Pattern: &pattern}}
		}
		task.Destinations = append(task.Destinations, taskconfig.Destination{ID: d.ID, Name: d.Name, Target: taskconfig.Target{Kind: "local", Path: d.Path}, Strategy: strategy, Filters: rules, CollisionPolicy: taskconfig.Skip, DeleteMode: taskconfig.Recycle})
	}
	data := t.TempDir()
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := l.snapshot.Save([]taskconfig.Task{task})
	if err != nil {
		t.Fatal(err)
	}
	l, err = savedLibrary(snap)
	if err != nil {
		t.Fatal(err)
	}
	return l, task, paths, data
}
func migrationReview(t *testing.T, task taskconfig.Task, pattern string) filefilter.ReviewedRegex {
	t.Helper()
	specs, err := filefilter.Proposals(task, task.Destinations[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[0]
	spec.Pattern = pattern
	preview, err := filefilter.PrepareRegexPreview(spec, []string{"a.txt", "b.bin"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := preview.Confirm()
	if err != nil {
		t.Fatal(err)
	}
	return review
}
func TestRegexMigrationSyncSaveReloadAndInvalidation(t *testing.T) {
	l, task, p, data := migrationFixture(t, false)
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "txt")
	manualWrite(t, filepath.Join(p.Source, "b.bin"), "bin")
	q, err := l.request(task)
	if err != nil {
		t.Fatal(err)
	}
	results, err := runMultiple(context.Background(), q, nil)
	if err == nil || results[task.Destinations[0].ID].State != destinationFailed || results[task.Destinations[1].ID].State != destinationSucceeded {
		t.Fatal("Sync target isolation", results, err)
	}
	absentMoveFile(t, filepath.Join(p.Target, "a.txt"))
	l, err = l.saveRegexConfirmation(migrationReview(t, task, `\.txt$`))
	if err != nil {
		t.Fatal(err)
	}
	// Saving a confirmation has no execution side effect.
	absentMoveFile(t, filepath.Join(p.Target, "a.txt"))
	l, err = loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	q, err = l.request(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runMultiple(context.Background(), q, nil); err != nil {
		t.Fatal(err)
	}
	manualRead(t, filepath.Join(p.Target, "a.txt"), "txt")
	absentMoveFile(t, filepath.Join(p.Target, "b.bin"))
	// Renaming the bound task invalidates every old receipt, not just edits to
	// the pattern itself. The saved legacy rules are never changed to Go syntax.
	task.Name = "changed"
	snap, err := l.snapshot.Save([]taskconfig.Task{task})
	if err != nil {
		t.Fatal(err)
	}
	l, err = savedLibrary(snap)
	if err != nil {
		t.Fatal(err)
	}
	q, _ = l.request(task)
	q = prepareReviewedRequest(q)
	if !errors.Is(q.Destinations[0].filterError, filefilter.ErrStaleReview) {
		t.Fatal(q.Destinations[0].filterError)
	}
}

func TestRegexMigrationPartialMoveCannotReachCatchall(t *testing.T) {
	l, task, p, _ := migrationFixture(t, true)
	pattern := "second legacy"
	task.Destinations[0].Filters = append(task.Destinations[0].Filters, taskconfig.Rule{Kind: "regex", Pattern: &pattern})
	snap, err := l.snapshot.Save([]taskconfig.Task{task})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = savedLibrary(snap)
	l, err = l.saveRegexConfirmation(migrationReview(t, task, `\.txt$`))
	if err != nil {
		t.Fatal(err)
	}
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "keep")
	q, _ := l.request(task)
	if _, err := runMultiple(context.Background(), q, nil); !errors.Is(err, filefilter.ErrNeedsReview) {
		t.Fatal(err)
	}
	manualRead(t, filepath.Join(p.Source, "a.txt"), "keep")
	for _, d := range task.Destinations {
		entries, err := os.ReadDir(d.Target.Path)
		if err != nil || len(entries) != 0 {
			t.Fatal("partial review wrote target", entries, err)
		}
	}
}
func TestRegexMigrationUnknownProfileAndSaveConflictFailClosed(t *testing.T) {
	l, task, _, _ := migrationFixture(t, false)
	review := migrationReview(t, task, "new")
	saved, err := review.Confirmation()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*filefilter.RegexConfirmation){
		func(r *filefilter.RegexConfirmation) { r.Profile = "future-profile" },
		func(r *filefilter.RegexConfirmation) { r.Version = 99 },
		func(r *filefilter.RegexConfirmation) { r.Origin.LegacyPattern = "edited" },
		func(r *filefilter.RegexConfirmation) { r.Pattern = "edited after confirmation" },
		func(r *filefilter.RegexConfirmation) { r.DecisionSHA256 = "" },
	} {
		r := saved
		change(&r)
		snap, err := l.snapshot.SaveConfirmations(l.tasks, []filefilter.RegexConfirmation{r})
		if err != nil {
			t.Fatal(err)
		}
		l, err = savedLibrary(snap)
		if err != nil {
			t.Fatal(err)
		}
		q, _ := l.request(task)
		q = prepareReviewedRequest(q)
		if q.Destinations[0].filterError == nil {
			t.Fatal("invalid confirmation authorized target")
		}
		if q.Destinations[1].filterError != nil {
			t.Fatal("unrelated Sync target blocked")
		}
	}
	// Existing external-edit protection also protects the one confirmation save.
	data := t.TempDir()
	copy, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := copy.snapshot.Save(l.tasks)
	if err != nil {
		t.Fatal(err)
	}
	copy, _ = savedLibrary(snap)
	path := filepath.Join(data, savedTaskFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := append(before, '\n')
	os.WriteFile(path, external, 0600)
	if next, err := copy.saveRegexConfirmation(review); err == nil || next != nil {
		t.Fatal("conflict swallowed")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(external) || len(copy.reviews) != 0 {
		t.Fatal("failed confirmation changed state")
	}
}
func TestRegexMigrationEditingClearsPreviewAndCancelDoesNotSave(t *testing.T) {
	l, task, _, _ := migrationFixture(t, false)
	a := &application{tasks: l, locale: "en", manual: manualSession{phase: manualReady, id: task.ID}}
	a.beginRegexMigration()
	if a.regexSession == nil {
		t.Fatal("missing editor")
	}
	e := &a.regexSession.edits[0]
	spec := e.spec
	spec.Pattern = "sample"
	e.preview, _ = filefilter.PrepareRegexPreview(spec, []string{"sample"})
	view := ui.NewTester(a.view, 1000, 1100)
	if err := view.Click("New pattern"); err != nil {
		t.Fatal(err)
	}
	view.Type("x")
	if e.pattern == "legacy" {
		t.Fatal("test did not edit the input")
	}
	if e.preview != nil {
		t.Fatal("editing retained preview authority")
	}
	a.closeRegexMigration()
	if a.regexSession != nil || len(a.tasks.reviews) != 0 || a.manual.busy() {
		t.Fatal("cancel saved or stayed busy")
	}
}

func TestRegexMigrationAutomaticRequestUsesSameGate(t *testing.T) {
	l, task, p, _ := migrationFixture(t, false)
	seconds := int32(10)
	task.Trigger = taskconfig.Trigger{Kind: "watch", SettleSeconds: &seconds}
	snap, err := l.snapshot.Save([]taskconfig.Task{task})
	if err != nil {
		t.Fatal(err)
	}
	l, err = savedLibrary(snap)
	if err != nil {
		t.Fatal(err)
	}
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "keep")
	// This is the same immutable request factory used by drainWatches; neither
	// it nor the executor creates a confirmation on an automatic request.
	q, err := l.request(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runMultiple(context.Background(), q, nil); err == nil {
		t.Fatal("automatic request bypassed review")
	}
	absentMoveFile(t, filepath.Join(p.Target, "a.txt"))
	if len(l.reviews) != 0 {
		t.Fatal("automatic execution confirmed a rule")
	}
}
