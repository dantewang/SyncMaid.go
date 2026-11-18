package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/winutil"
)

// Smoke-test the native view without starting a desktop window.
func TestNativeView(t *testing.T) {
	a := &application{locale: "zh-Hans", installation: winutil.Installation{Data: `\\?\C:\isolated\Data`}}
	tt := ui.NewTester(a.view, 640, 420)
	if !tt.HasText("SYNC") || !tt.HasText("同步任务") || !tt.HasText("全部折叠") {
		t.Fatalf("unexpected startup view: %q", tt.Texts())
	}
	if a.installation.Data != `\\?\C:\isolated\Data` {
		t.Fatal("view changed the operation path")
	}
}
