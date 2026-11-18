package main

import (
	"errors"
	"os"
	"path/filepath"
	"syncmaid/internal/syncengine"
	"testing"

	"syncmaid/internal/winutil"
)

func TestCommonSettingsDefaultsRoundtripAndProtection(t *testing.T) {
	data := t.TempDir()
	s := winutil.LoadCommonSettings(data)
	if s.Err != nil || s.Value != (winutil.CommonSettings{}) {
		t.Fatal(s.Err, s.Value)
	}
	want := winutil.CommonSettings{CloseToTray: true, StartMinimized: true}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	s = winutil.LoadCommonSettings(data)
	if s.Err != nil || s.Value != want {
		t.Fatal(s.Err, s.Value)
	}
	path := filepath.Join(data, "settings-go.json")
	for _, bad := range []string{`null`, `{"CloseToTray":"yes"}`, `{"Language":"en"}`, `{bad`, `{} {}`} {
		os.WriteFile(path, []byte(bad), 0600)
		s = winutil.LoadCommonSettings(data)
		if s.Err == nil || s.Save(want) == nil {
			t.Fatalf("accepted %s", bad)
		}
		b, _ := os.ReadFile(path)
		if string(b) != bad {
			t.Fatal("corrupt source overwritten")
		}
	}
	os.Remove(path)
	s = winutil.LoadCommonSettings(data)
	os.WriteFile(path, []byte(`{}`), 0600)
	if s.Save(want) == nil {
		t.Fatal("external creation overwritten")
	}
}
func TestCommonCloseAndBusyQuit(t *testing.T) {
	for _, hide := range []bool{false, true} {
		w := &recordedWindow{}
		quits := 0
		canceled := false
		reply := make(chan error, 1)
		a := &application{commonValue: winutil.CommonSettings{CloseToTray: hide}, manual: manualSession{phase: manualRunning, cancel: func() { canceled = true }, mirrorReply: reply, mirrorPreview: &syncengine.MirrorPreview{}}}
		a.lifecycle.ready(w, func() { quits++ }, nil)
		a.closeWindow()
		select {
		case err := <-reply:
			if !errors.Is(err, syncengine.ErrMirrorDeclined) {
				t.Fatalf("close did not reject Mirror: %v", err)
			}
		default:
			t.Fatal("close left Mirror confirmation pending")
		}
		if a.manual.mirrorReply != nil || a.manual.mirrorPreview != nil {
			t.Fatal("closed confirmation still visible/owned")
		}
		if hide {
			if canceled || a.lifecycle.phase != hidden {
				t.Fatal("hiding canceled run")
			}
		} else {
			if !canceled || !a.manual.quitPending || a.manual.phase != manualStopping || quits != 0 {
				t.Fatal("close did not wait for run cleanup")
			}
		}
	}
	a := &application{commonValue: winutil.CommonSettings{CloseToTray: true}, manual: manualSession{phase: manualReady}}
	w := &recordedWindow{}
	quits := 0
	a.lifecycle.ready(w, func() { quits++ }, nil)
	a.requestQuit()
	if quits != 1 || a.lifecycle.phase != exiting {
		t.Fatal("explicit exit hidden")
	}

}
