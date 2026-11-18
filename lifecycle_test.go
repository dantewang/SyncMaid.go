package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/egoist/mygo"
)

func TestWindowInitializationFailureCanQuitAndReleaseTray(t *testing.T) {
	cause := errors.New("backend initialization failed")
	_, err := createStartupWindow(func() *mygo.Window { panic(cause) })
	if !errors.Is(err, cause) {
		t.Fatalf("lost window failure: %v", err)
	}
	l := &lifecycle{}
	releases := 0
	l.destroyTray = func() { releases++ }
	l.beginQuit()
	l.finish()
	l.finish()
	if releases != 1 || l.phase != stopped {
		t.Fatal("tray leaked on failed window initialization")
	}
}

type recordedWindow struct{ calls []string }

func (w *recordedWindow) Hide()    { w.calls = append(w.calls, "hide") }
func (w *recordedWindow) Restore() { w.calls = append(w.calls, "restore") }
func (w *recordedWindow) Show()    { w.calls = append(w.calls, "show") }
func (w *recordedWindow) Focus()   { w.calls = append(w.calls, "focus") }

func TestHideRestoreQuitCannotReviveOrLeakResources(t *testing.T) {
	for _, quitHidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible", true: "hidden"}[quitHidden], func(t *testing.T) {
			l := &lifecycle{}
			w := &recordedWindow{}
			quits, releases := 0, 0
			l.show() // Early activation cannot touch a not-yet-created window.
			l.ready(w, func() { quits++ }, func() { releases++ })
			if !l.closeRequested() || l.phase != hidden {
				t.Fatal("close must hide and retain the app")
			}
			l.show()
			if l.phase != visible || !reflect.DeepEqual(w.calls, []string{"hide", "restore", "show", "focus"}) {
				t.Fatalf("restore failed: %v %v", l.phase, w.calls)
			}
			if quitHidden {
				l.closeRequested()
			}
			l.exit()
			calls := len(w.calls)
			l.exit()
			l.show() // A late activation or tray click while quitting is ignored.
			if l.closeRequested() || quits != 1 || l.phase != exiting || len(w.calls) != calls {
				t.Fatal("explicit quit was blocked, repeated, or revived the window")
			}
			l.finish()
			l.finish()
			l.beginQuit()
			l.show()
			if l.phase != stopped || releases != 1 || len(w.calls) != calls {
				t.Fatal("resources not released exactly once, or terminal app revived")
			}
		})
	}
}

func TestSystemQuitAndStartupFailureCleanup(t *testing.T) {
	l := &lifecycle{}
	l.beginQuit() // Startup can fail before window/tray creation.
	l.finish()
	if l.phase != stopped {
		t.Fatal("failed startup did not terminate")
	}
	w := &recordedWindow{}
	l = &lifecycle{}
	released := 0
	l.ready(w, func() { t.Fatal("system quit should not recurse into Quit") }, func() { released++ })
	l.beginQuit()
	if l.closeRequested() {
		t.Fatal("system quit close was intercepted")
	}
	l.finish()
	if released != 1 || l.phase != stopped {
		t.Fatal("system quit left resources")
	}
}
