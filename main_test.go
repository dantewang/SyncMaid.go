package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Smoke-test the native view without starting a desktop window.
func TestNativeView(t *testing.T) {
	a := &application{locale: "zh-Hans", installation: installation{data: `\\?\C:\isolated\Data`}}
	tt := ui.NewTester(a.view, 640, 420)
	if !tt.HasText("SyncMaid") || !tt.HasText("任务概览") || !tt.HasText("全部折叠") {
		t.Fatalf("unexpected startup view: %q", tt.Texts())
	}
	if a.installation.data != `\\?\C:\isolated\Data` {
		t.Fatal("view changed the operation path")
	}
}
