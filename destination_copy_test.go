package main

import "testing"

func TestDestinationCopyIsIndependentUnsavedConfiguration(t *testing.T) {
	l, e := loadSavedTasks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	l, id, e := l.save("", "Saved", manualFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	a := &application{tasks: l, locale: "zh-Hans"}
	a.selectManual(id)
	original := a.manual.targets[0]
	a.copyManualTarget(original.ID)
	if len(a.manual.targets) != 2 || !a.manual.dirty || len(l.tasks[0].Destinations) != 1 {
		t.Fatal("copy was not an unsaved draft")
	}
	copy := a.manual.targets[1]
	if copy.ID == original.ID || copy.Path != original.Path || copy.Name == original.Name || a.manual.targetID != copy.ID || copy.matcher != nil {
		t.Fatal("copy identity or capability leaked")
	}
	a.setFilterMode(filterNone)
	if !isAllFileRules(a.manual.targets[0].Filters) {
		t.Fatal("editing copy changed original")
	}
	a.toggleDestinationEditor(copy.ID)
	if a.cards.targetHidden == copy.ID {
		t.Fatal("accepted overlapping copy")
	}
	a.manual.target = t.TempDir()
	a.manual.edited()
	a.toggleDestinationEditor(copy.ID)
	if a.cards.targetHidden != copy.ID {
		t.Fatal("valid copy did not accept")
	}
	a.toggleDestinationEditor(copy.ID)
	if a.cards.targetHidden != "" {
		t.Fatal("reopen failed")
	}
	a.manual.phase = manualRunning
	a.copyManualTarget(original.ID)
	if len(a.manual.targets) != 2 {
		t.Fatal("copy bypassed busy guard")
	}
}
