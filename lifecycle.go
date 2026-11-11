package main

import "log/slog"

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
// for the transition contract; there is no background application work yet.
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
