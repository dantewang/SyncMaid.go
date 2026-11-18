package winutil

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

type StartupEntry struct{ Name, Command string }
type StartupStatus string

const (
	StartupAbsent     StartupStatus = "未登记"
	StartupRegistered StartupStatus = "已登记"
	StartupDisabled   StartupStatus = "已登记，但被系统禁用"
	StartupUnknown    StartupStatus = "已登记，系统批准状态未知"
	StartupForeign    StartupStatus = "同名启动项命令不同，未修改"
)

func CurrentStartupEntry(i Installation) (StartupEntry, error) {
	exe, err := os.Executable()
	if err != nil {
		return StartupEntry{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return StartupEntry{}, err
	}
	return StartupEntry{Name: "SyncMaid.Go." + strings.TrimPrefix(i.Key, "SyncMaid-"), Command: `"` + exe + `"`}, nil
}
func (e StartupEntry) commandValue() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(e.Name)
	return value, err
}
func (e StartupEntry) Status() (StartupStatus, error) {
	command, err := e.commandValue()
	if errors.Is(err, registry.ErrNotExist) {
		return StartupAbsent, nil
	}
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(command, e.Command) {
		return StartupForeign, nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return StartupRegistered, nil
	}
	if err != nil {
		return StartupUnknown, err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue(e.Name)
	if errors.Is(err, registry.ErrNotExist) {
		return StartupRegistered, nil
	}
	if err != nil {
		return StartupUnknown, err
	}
	if len(b) < 12 {
		return StartupUnknown, nil
	}
	switch b[0] {
	case 2:
		return StartupRegistered, nil
	case 3:
		return StartupDisabled, nil
	default:
		return StartupUnknown, nil
	}
}
func (e StartupEntry) Set(enabled bool) error {
	if !enabled {
		k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.QUERY_VALUE|registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		actual, _, readErr := k.GetStringValue(e.Name)
		if errors.Is(readErr, registry.ErrNotExist) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		if !strings.EqualFold(actual, e.Command) {
			return fmt.Errorf("%s", StartupForeign)
		}
		err = k.DeleteValue(e.Name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, startupRunKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Recheck after acquiring the key; this is not a registry transaction/CAS.
	actual, _, readErr := k.GetStringValue(e.Name)
	if readErr != nil && !errors.Is(readErr, registry.ErrNotExist) {
		return readErr
	}
	if readErr == nil && !strings.EqualFold(actual, e.Command) {
		return fmt.Errorf("%s", StartupForeign)
	}
	return k.SetStringValue(e.Name, e.Command)
}
