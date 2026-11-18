package main

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"log/slog"
	"syncmaid/internal/winutil"
)

type phase uint8

const (
	starting phase = iota
	visible
	hidden
	exiting
	stopped
)

func (p phase) String() string {
	switch p {
	case starting:
		return "starting"
	case visible:
		return "visible"
	case hidden:
		return "hidden"
	case exiting:
		return "exiting"
	case stopped:
		return "stopped"
	default:
		return "invalid"
	}
}

type mainWindow interface {
	Hide()
	Restore()
	Show()
	Focus()
}

// lifecycle and its window/tray are owned exclusively by MyGo's main thread.
// The phase is the only authority for hide/show/quit decisions. See AGENT.md
// for the transition contract. Session work is canceled before explicit quit.
type lifecycle struct {
	phase       phase
	window      mainWindow
	quitApp     func()
	destroyTray func()
}

func (l *lifecycle) change(next phase, event string) {
	slog.Info("lifecycle", "from", l.phase.String(), "to", next.String(), "event", event)
	l.phase = next
}

func (l *lifecycle) ready(window mainWindow, quit, destroyTray func()) {
	l.window, l.quitApp, l.destroyTray = window, quit, destroyTray
	l.change(visible, "ready")
}

// closeRequested returns whether the native close must be prevented.
func (l *lifecycle) closeRequested() bool {
	switch l.phase {
	case visible, hidden:
		l.window.Hide()
		l.change(hidden, "close-to-tray")
		return true
	case starting:
		return true
	default:
		return false // Explicit quit must be able to destroy the window.
	}
}

func (l *lifecycle) show() {
	if l.phase != visible && l.phase != hidden {
		return
	}
	l.window.Restore()
	l.window.Show()
	l.window.Focus()
	l.change(visible, "show")
}

func (l *lifecycle) beginQuit() {
	if l.phase != stopped && l.phase != exiting {
		l.change(exiting, "quit")
	}
}

func (l *lifecycle) exit() {
	if l.phase != visible && l.phase != hidden {
		return
	}
	l.beginQuit()
	l.quitApp()
}

// finish runs in OnWillQuit, before the native loop stops. Idempotence comes
// from the terminal phase rather than inferring ownership from nil callbacks.
func (l *lifecycle) finish() {
	if l.phase != exiting {
		return
	}
	if l.destroyTray != nil { // Startup can fail before a tray is acquired.
		l.destroyTray()
	}
	l.change(stopped, "resources-released")
}

// MyGo v0.8.3 NewWindow panics on a backend creation error instead of
// returning it. Restrict recovery to that initialization call, not the app loop.
func createStartupWindow(create func() *mygo.Window) (window *mygo.Window, err error) {
	defer func() {
		if value := recover(); value != nil {
			if cause, ok := value.(error); ok {
				err = fmt.Errorf("native window initialization: %w", cause)
			} else {
				err = fmt.Errorf("native window initialization: %v", value)
			}
		}
	}()
	window = create()
	if window == nil {
		return nil, errors.New("native window initialization returned no window")
	}
	return window, nil
}

// MyGo v0.8.3 has no dedicated invalidate/redraw call without passing a mutator.
func redrawWindow(w *mygo.Window) {
	if w != nil {
		w.Update(func() {})
	}
}

// openShellPath converts native extended paths to human-readable DOS/UNC spelling
// before handing them off to MyGo's Shell opener.
func openShellPath(nativePath string) error {
	return mygo.Shell.OpenPath(winutil.PathLabel(nativePath))
}
