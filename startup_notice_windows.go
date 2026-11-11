package main

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
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

// A synchronous Win32 message box needs no MyGo event loop, config path or
// application-owned Data. Called on main after run returns and releases locks.
func showStartupNotice(notice startupNotice) error {
	text, err := windows.UTF16PtrFromString(notice.message)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString(notice.title)
	if err != nil {
		return err
	}
	result, err := windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TASKMODAL|windows.MB_SETFOREGROUND)
	if result == 0 && err == nil {
		return errors.New("native startup notice returned no result")
	}
	return err
}
