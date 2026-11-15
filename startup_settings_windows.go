package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const startupRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const startupApprovedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

type startupEntry struct{ name, command string }
type startupStatus string

const (
	startupAbsent     startupStatus = "未登记"
	startupRegistered startupStatus = "已登记"
	startupDisabled   startupStatus = "已登记，但被系统禁用"
	startupUnknown    startupStatus = "已登记，系统批准状态未知"
	startupForeign    startupStatus = "同名启动项命令不同，未修改"
)

func currentStartupEntry(i installation) (startupEntry, error) {
	exe, err := os.Executable()
	if err != nil {
		return startupEntry{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return startupEntry{}, err
	}
	return startupEntry{name: "SyncMaid.Go." + strings.TrimPrefix(i.key, "SyncMaid-"), command: `"` + exe + `"`}, nil
}
func (e startupEntry) commandValue() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(e.name)
	return value, err
}
func (e startupEntry) status() (startupStatus, error) {
	command, err := e.commandValue()
	if errors.Is(err, registry.ErrNotExist) {
		return startupAbsent, nil
	}
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(command, e.command) {
		return startupForeign, nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return startupRegistered, nil
	}
	if err != nil {
		return startupUnknown, err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue(e.name)
	if errors.Is(err, registry.ErrNotExist) {
		return startupRegistered, nil
	}
	if err != nil {
		return startupUnknown, err
	}
	if len(b) < 12 {
		return startupUnknown, nil
	}
	switch b[0] {
	case 2:
		return startupRegistered, nil
	case 3:
		return startupDisabled, nil
	default:
		return startupUnknown, nil
	}
}
func (e startupEntry) set(enabled bool) error {
	old, err := e.commandValue()
	missing := errors.Is(err, registry.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if !missing && !strings.EqualFold(old, e.command) {
		return fmt.Errorf("%s", startupForeign)
	}
	if !enabled && missing {
		return nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, startupRunKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Recheck after acquiring the key; this is not a registry transaction/CAS.
	actual, _, readErr := k.GetStringValue(e.name)
	if readErr != nil && !errors.Is(readErr, registry.ErrNotExist) {
		return readErr
	}
	if readErr == nil && !strings.EqualFold(actual, e.command) {
		return fmt.Errorf("%s", startupForeign)
	}
	if enabled {
		return k.SetStringValue(e.name, e.command)
	}
	err = k.DeleteValue(e.name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}
