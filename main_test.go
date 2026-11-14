package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Smoke-test the native view without starting a desktop window.
func TestNativeView(t *testing.T) {
	a := &application{installation: installation{data: `\\?\C:\isolated\Data`}}
	tt := ui.NewTester(a.view, 640, 420)
	if !tt.HasText("SyncMaid") || !tt.HasText("手动同步 · 任务保存在 tasks-go.json") || !tt.HasText("任务列表") {
		t.Fatalf("unexpected startup view: %q", tt.Texts())
	}
	if !tt.HasText(`Data: C:\isolated\Data`) {
		t.Fatalf("portable path missing: %q", tt.Texts())
	}
	if a.installation.data != `\\?\C:\isolated\Data` {
		t.Fatal("view changed the operation path")
	}
}
