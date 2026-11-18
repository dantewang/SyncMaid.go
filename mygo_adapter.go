package main

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo"
)

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
	return mygo.Shell.OpenPath(pathLabel(nativePath))
}
