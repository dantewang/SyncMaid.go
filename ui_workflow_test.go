package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func emptyTaskApplication(t *testing.T) (*application, string) {
	t.Helper()
	data := t.TempDir()
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l, locale: "zh-Hans"}
	a.newManual()
	a.manual.name = "独立任务"
	a.manual.source = filepath.Join(t.TempDir(), "尚不可用的源")
	a.manual.edited()
	return a, data
}

func TestTaskSheetSavesZeroDestinationsWithoutRuntimePreflight(t *testing.T) {
	a, data := emptyTaskApplication(t)
	if len(a.manual.targets) != 0 {
		t.Fatal("new task manufactured a destination")
	}
	q := a.manual.draftRequest()
	l, id, err := a.tasks.saveMultiple("", a.manual.name, q)
	if err != nil {
		t.Fatal(err)
	}
	a.manual.phase = manualSaving
	a.cards.editor = true
	a.finishTaskSave(l, id, nil, nil)
	if a.cards.editor || a.manual.dirty {
		t.Fatal("save did not close sheet")
	}
	reloaded, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	record, err := reloaded.find(id)
	if err != nil || len(record.Destinations) != 0 || record.SourcePath != q.Source {
		t.Fatal(record, err)
	}
	if _, err := multipleRequestFromSaved(record); err != nil {
		t.Fatal("empty task unavailable", err)
	}
	for _, kind := range []taskconfig.TaskKind{taskconfig.Sync, taskconfig.Moving} {
		q.Kind = kind
		results, err := runMultiple(context.Background(), q, func(destinationResult) { t.Fatal("empty task emitted file progress") })
		if err != nil || len(results) != 0 {
			t.Fatal(results, err)
		}
	}
	if _, err := os.Stat(q.Source); !os.IsNotExist(err) {
		t.Fatal("no-op accessed or created missing source", err)
	}
	a.startManual()
	if a.manual.message.key != "无目标，未执行文件操作。" || a.manual.busy() {
		t.Fatal(a.manual.message)
	}
	record.Trigger = taskconfig.Trigger{Kind: "watch"}
	a.tasks.tasks[0] = record
	a.syncWatches()
	if len(a.watches) != 0 {
		t.Fatal("empty task subscribed to source")
	}
}

func TestTaskSheetCancelXEscapeAndBackdropDiscardOnce(t *testing.T) {
	for _, how := range []string{"取消", "关闭编辑", "escape", "backdrop"} {
		t.Run(how, func(t *testing.T) {
			a, _ := emptyTaskApplication(t)
			a.cards.editor = true
			view := ui.NewTester(a.view, 760, 620)
			switch how {
			case "取消", "关闭编辑":
				if err := view.Click(how); err != nil {
					t.Fatal(err)
				}
			case "backdrop":
				view.ClickAt(8, 300)
			case "escape":
				view.Key(0, ui.KeyEscape)
			}
			if a.cards.editor || a.manual.dirty || len(a.tasks.tasks) != 0 {
				t.Fatal("cancel retained draft or saved it")
			}
		})
	}
}

func TestDestinationCancelIsRowScopedAndCopyIsIndependent(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	q, _ := multiFixture(t)
	l, id, err := a.tasks.saveMultiple("", "Task", q)
	if err != nil {
		t.Fatal(err)
	}
	a.tasks = l
	a.manual.dirty = false
	a.selectManual(id)
	original := a.manual.targets[0]
	a.cards.targetHidden = original.ID
	a.toggleDestinationEditor(original.ID)
	a.manual.targetName = "discard me"
	a.manual.edited()
	a.cancelDestination(original.ID)
	if a.manual.targets[0].Name != original.Name || l.tasks[0].Destinations[0].Name != original.Name {
		t.Fatal("cancel polluted original")
	}
	a.addManualTarget()
	newID := a.manual.targetID
	a.cancelDestination(newID)
	if len(a.manual.targets) != 2 {
		t.Fatal("cancelled new destination survived")
	}
	a.copyManualTarget(original.ID)
	copyID := a.manual.targetID
	if copyID == original.ID {
		t.Fatal("copy reused identity")
	}
	a.manual.target = t.TempDir()
	a.manual.targetName = "accepted copy"
	a.manual.edited()
	if !a.acceptDestination(copyID) {
		t.Fatal(a.manual.message)
	}
	a.cards.targetHidden = copyID
	a.toggleDestinationEditor(copyID)
	a.manual.targetName = "cancel second edit"
	a.manual.edited()
	a.cancelDestination(copyID)
	if len(a.manual.targets) != 3 || a.manual.targets[2].Name != "accepted copy" {
		t.Fatal("row cancel discarded accepted workspace changes")
	}
	a.moveManualTarget(copyID, -2)
	if a.manual.targets[0].ID != copyID {
		t.Fatal("reorder lost ID")
	}
	a.manual.selectTarget(copyID)
	a.removeManualTarget()
	if len(a.manual.targets) != 2 || len(l.tasks[0].Destinations) != 2 {
		t.Fatal("delete mutated stored snapshot")
	}
	if a.cards.targetHidden != a.manual.targetID {
		t.Fatal("delete implicitly opened another row without an original snapshot")
	}
	a.closeTaskEditor()
	if a.manual.dirty || len(a.manual.targets) != 2 {
		t.Fatal("workspace cancel failed")
	}
}

func TestWorkspaceBusyGuardsAndMoveCatchAllOrder(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	a.manual.dirty = false
	a.manual.kind = taskconfig.Moving
	a.addManualTarget()
	catchAll := a.manual.targetID
	a.manual.target = t.TempDir()
	a.manual.edited()
	if !a.acceptDestination(catchAll) {
		t.Fatal(a.manual.message)
	}
	a.addManualTarget()
	leaf := a.manual.targetID
	a.manual.target = t.TempDir()
	a.manual.edited()
	a.manual.targets[1].Filters = []taskconfig.Rule{taskconfig.ExtensionFilter("txt")}
	if !a.acceptDestination(leaf) {
		t.Fatal(a.manual.message)
	}
	// Accepting the catch-all settles it after ordinary rules.
	a.manual.selectTarget(catchAll)
	a.manual.beginDestinationEdit(catchAll, &a.manual.targets[0])
	if !a.acceptDestination(catchAll) || a.manual.targets[1].ID != catchAll {
		t.Fatal("catch-all not last")
	}
	a.moveManualTarget(catchAll, -1)
	if a.manual.targets[1].ID != catchAll {
		t.Fatal("catch-all reordered ahead of rule")
	}
	for _, phase := range []manualPhase{manualRunning, manualSaving, manualChoosing, manualReviewWorking} {
		a.manual.phase = phase
		a.cards.editor = true
		a.addManualTarget()
		a.cancelDestination(catchAll)
		a.acceptDestination(catchAll)
		a.removeManualTarget()
		a.closeTaskEditor()
		if len(a.manual.targets) != 2 || !a.cards.editor || a.manual.phase != phase {
			t.Fatal("busy guard bypass", phase)
		}
	}
}

func TestConfigurationRefusalKeepsEditorWritable(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	a.cards.editor = true
	q := a.manual.draftRequest()
	q.Destinations = []manualDestination{{ID: taskconfig.NewID(), Name: "overlap", Path: q.Source, Filters: allFileRules()}}
	_, id, err := a.tasks.prepareMultiple("", a.manual.name, q)
	if err == nil {
		t.Fatal("overlap accepted")
	}
	a.manual.phase = manualSaving
	a.finishTaskSave(nil, id, err, nil)
	if a.taskError != nil || !a.cards.editor || !a.manual.dirty || a.manual.busy() {
		t.Fatal("domain refusal blocked all further editing")
	}
}

func TestAcceptOneDestinationDoesNotConsumeAnotherRowsInvalidDraft(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	q, _ := multiFixture(t)
	l, id, err := a.tasks.saveMultiple("", "Rows", q)
	if err != nil {
		t.Fatal(err)
	}
	a.tasks = l
	a.manual.dirty = false
	a.selectManual(id)
	first, other := a.manual.targets[0], a.manual.targets[1]
	a.manual.beginDestinationEdit(first.ID, &first)
	a.manual.beginDestinationEdit(other.ID, &other)
	a.manual.filterDrafts[other.ID] = filterEditor{Error: "invalid input in other row"}
	if !a.acceptDestination(first.ID) {
		t.Fatal("another row blocked independent accept")
	}
	if a.manual.filterDraftError() == nil {
		t.Fatal("accept cleared other row's validation error")
	}
	a.cancelDestination(other.ID)
	if a.manual.filterDraftError() != nil {
		t.Fatal("cancel did not discard invalid input")
	}
}

func TestExplicitTaskDeletionDoesNotRequireRegexExecutionPermission(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "keep.txt"), "unchanged")
	l, id, err := a.tasks.save("", "Regex task", q)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	pattern := ".*"
	tasks[0].Destinations[0].Filters = []taskconfig.Rule{{Kind: "regex", Pattern: &pattern}}
	snapshot, err := l.snapshot.Save(tasks)
	if err != nil {
		t.Fatal(err)
	}
	l, err = savedLibrary(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	l, err = l.remove(id)
	if err != nil || len(l.tasks) != 0 {
		t.Fatal("explicit configuration deletion required filter execution", err)
	}
	manualRead(t, filepath.Join(q.Source, "keep.txt"), "unchanged")
}

func TestEmptyTaskCardDoesNotDisplayOldTargetSuccessAsCurrentState(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	l, id, err := a.tasks.saveMultiple("", a.manual.name, a.manual.draftRequest())
	if err != nil {
		t.Fatal(err)
	}
	a.tasks = l
	a.manual.dirty = false
	a.selectManual(id)
	a.results = map[taskconfig.ID]savedTaskResult{id: {message: messagef("全部目标同步完成"), destinations: multipleResult{taskconfig.NewID(): {State: destinationSucceeded}}}}
	view := ui.NewTester(a.view, 760, 620)
	if !view.HasText(a.tr("无目标，未执行文件操作。")) || view.HasText(a.tr("全部已同步")) {
		t.Fatal("past results masked empty task state")
	}
}

func TestCancelAfterStorageFailureDiscardsDraftWithoutUnblockingLibrary(t *testing.T) {
	a, _ := emptyTaskApplication(t)
	fault := errors.New("storage denied")
	a.taskError = fault
	a.cards.editor = true
	a.closeTaskEditor()
	if a.cards.editor || a.manual.dirty || a.manual.name != "" || a.manual.source != "" || !errors.Is(a.taskError, fault) {
		t.Fatal("cancel retained draft or removed storage protection")
	}
}
