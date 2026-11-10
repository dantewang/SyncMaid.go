package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Smoke-test the native view without starting a desktop window.
func TestNativeView(t *testing.T) {
	tt := ui.NewTester(view, 640, 420)
	if !tt.HasText("SyncMaid") || !tt.HasText("Native UI project initialized.") {
		t.Fatalf("unexpected startup view: %q", tt.Texts())
	}
}
