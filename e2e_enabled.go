//go:build e2e

package main

import (
	"os"
	"strconv"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/ui"
)

func e2eConfigureView(c *ui.Context) {
	if os.Getenv("MYGO_AUTOMATION") != "" {
		c.SetMenuPresentation(ui.PopoverMenus)
	}
}

func e2eDialogOpen(opts mygo.OpenDialogOptions) ([]string, error) {
	if os.Getenv("MYGO_AUTOMATION") != "" {
		if path := os.Getenv("SYNCMAID_E2E_OPEN_PATH"); path != "" {
			return []string{path}, nil
		}
	}
	return mygo.Dialog.Open(opts)
}

func e2eDialogMessage(opts mygo.MessageOptions) (mygo.MessageResult, error) {
	if os.Getenv("MYGO_AUTOMATION") != "" {
		if raw := os.Getenv("SYNCMAID_E2E_DIALOG_BUTTON"); raw != "" {
			if button, err := strconv.Atoi(raw); err == nil {
				return mygo.MessageResult{Button: button}, nil
			}
		}
	}
	return mygo.Dialog.Message(opts)
}

func e2eSuppressStartupNotice() bool {
	return os.Getenv("SYNCMAID_E2E_NO_MSGBOX") == "1"
}
