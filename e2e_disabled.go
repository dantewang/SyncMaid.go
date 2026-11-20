//go:build !e2e

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func e2eConfigureView(*ui.Context) {}

func e2eDialogOpen(opts mygo.OpenDialogOptions) ([]string, error) {
	return mygo.Dialog.Open(opts)
}

func e2eDialogMessage(opts mygo.MessageOptions) (mygo.MessageResult, error) {
	return mygo.Dialog.Message(opts)
}

func e2eSuppressStartupNotice() bool { return false }
