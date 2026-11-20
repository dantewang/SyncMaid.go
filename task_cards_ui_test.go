package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestCardLayoutIdentityAndEditorGuards(t *testing.T) {
	l, err := appcore.LoadSavedTasks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, first, err := saveTask(l, "", "First", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	l, second, err := saveTask(l, "", "Second", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: l}
	a.selectManual(first)
	a.toggleCard(first)
	a.toggleCard(second)
	a.toggleCard(first)
	if a.cards.collapsed[first] || !a.cards.collapsed[second] {
		t.Fatal("collapse state leaked across IDs")
	}
	// Reordering the library cannot change the ID selected by a card.
	l.Tasks[0], l.Tasks[1] = l.Tasks[1], l.Tasks[0]
	task, _ := l.Find(second)
	destination := task.Destinations[0].ID
	a.editCard(second, destination)
	if a.manual.id != second || a.manual.targetID != destination || !a.cards.editor {
		t.Fatal("wrong editor identity")
	}
	a.manual.dirty = true
	a.closeTaskEditor()
	if a.cards.editor || a.manual.dirty || a.manual.id != second {
		t.Fatal("cancel did not discard and close")
	}
	a.manual.dirty = false
	for _, phase := range []manualPhase{manualRunning, manualSaving, manualReviewing, manualChoosing} {
		a.manual.phase = phase
		a.editCard(first, "")
		if a.manual.id != second || a.cardActionAllowed() {
			t.Fatal("busy editor bypassed", phase)
		}
	}
	a.manual.phase = manualFinished
	a.closeTaskEditor()
	if a.cards.editor {
		t.Fatal("clean editor stayed open")
	}
}

func TestCollapseActionTracksCurrentCards(t *testing.T) {
	id := taskconfig.NewID()
	a := &application{tasks: &appcore.SavedTaskLibrary{Tasks: []taskconfig.Task{{ID: id}}}}
	a.toggleCard(taskconfig.NewID())
	if a.hasCollapsedCards() {
		t.Fatal("removed card controls the action")
	}
	a.toggleCard(id)
	if !a.hasCollapsedCards() {
		t.Fatal("collapsed card ignored")
	}
	a.toggleCard(id)
	if a.hasCollapsedCards() {
		t.Fatal("expanded card still counted as collapsed")
	}
}

func TestPorcelainOverlayCannotHideMirrorOrStop(t *testing.T) {
	a := &application{locale: "zh-Hans", historyOpen: true, cards: taskCardLayout{editor: true}, manual: manualSession{phase: manualRunning, mirrorPreview: &syncengine.MirrorPreview{Name: "Synthetic", Paths: []string{"synthetic.txt"}, Mode: taskconfig.Permanent}}}
	tt := ui.NewTester(a.view, 940, 620)
	if !tt.HasText(a.tr("确认本次移除")) || !tt.HasText(a.tr("停止")) {
		t.Fatal("navigation hid safety actions", tt.Texts())
	}
	if tt.HasText(a.tr("任务名称")) {
		t.Fatal("editor covered pending confirmation")
	}
}
