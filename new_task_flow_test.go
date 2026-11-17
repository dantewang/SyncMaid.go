package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNewTaskDraftCanReachDestinationsBeforeSaving(t *testing.T) {
	data := t.TempDir()
	library, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	a := &application{tasks: library, locale: "zh-Hans"}
	a.newManual()
	a.cards.editor = true
	a.manual.name = "首次新建"
	a.manual.source = filepath.Join(t.TempDir(), "中文源")
	if err := os.Mkdir(a.manual.source, 0700); err != nil {
		t.Fatal(err)
	}
	a.manual.edited()
	view := ui.NewTester(a.view, 760, 620)
	if _, ok := view.Find("下一步：配置目标"); !ok {
		t.Fatal("missing visible next step")
	}
	if view.HasText("保存任务") {
		t.Fatal("new task offers save before configuring destination")
	}
	a.openDraftDestinations()
	if !a.cards.destinations || !a.manual.dirty || a.manual.id != "" || a.manual.name != "首次新建" {
		t.Fatal("draft lost or navigation blocked")
	}
	q := a.manual.draftRequest()
	if err := validateMultiple(context.Background(), q); err == nil || !strings.Contains(err.Error(), "目标文件夹未填写") {
		t.Fatalf("wrong blank destination diagnosis: %v", err)
	} else {
		a.finishTaskSave(nil, "", err, nil)
		if a.manual.name != "首次新建" || a.manual.source != q.Source || !a.manual.dirty || !a.cards.destinations {
			t.Fatal("failed validation lost draft or editor")
		}
	}
	a.manual.target = filepath.Join(t.TempDir(), "中文目标")
	if err := os.Mkdir(a.manual.target, 0700); err != nil {
		t.Fatal(err)
	}
	a.manual.edited()
	q = a.manual.draftRequest()
	if err := validateMultiple(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	_, id, err := library.saveMultiple("", a.manual.name, q)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reloaded.find(id)
	if err != nil || saved.SourcePath != q.Source || saved.Destinations[0].Target.Path != q.Destinations[0].Path {
		t.Fatal("saved paths changed", saved, err)
	}
	a.manual.phase = manualChoosing
	a.cards.destinations = false
	a.openDraftDestinations()
	if a.cards.destinations {
		t.Fatal("navigation bypassed dialog guard")
	}
}

func TestNewTaskPathErrorDistinguishesSourceAndDestination(t *testing.T) {
	for _, q := range []manualTaskRequest{
		{Source: `D:中文源`, Destinations: []manualDestination{{}}},
		{Source: "", Destinations: []manualDestination{{}}},
	} {
		before := q.Source
		err := validateMultiple(context.Background(), q)
		if err == nil || !strings.Contains(err.Error(), "源文件夹") || strings.Contains(err.Error(), "目标") || q.Source != before {
			t.Fatal("wrong source diagnosis", err)
		}
	}
}
