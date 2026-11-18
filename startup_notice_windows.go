package main

import (
	"errors"

	"golang.org/x/sys/windows"
	"syncmaid/internal/winutil"
)

// A synchronous Win32 message box needs no MyGo event loop, config path or
// application-owned Data. Called on main after run returns and releases locks.
func showStartupNotice(notice winutil.StartupNotice) error {
	text, err := windows.UTF16PtrFromString(notice.Message)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString(notice.Title)
	if err != nil {
		return err
	}
	result, err := windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TASKMODAL|windows.MB_SETFOREGROUND)
	if result == 0 && err == nil {
		return errors.New("native startup notice returned no result")
	}
	return err
}
