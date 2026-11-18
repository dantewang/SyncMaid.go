package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

func decodeLegacyWindowSettings(raw []byte) (commonSettings, error) {
	var v *struct {
		commonSettings
		Language *string
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return commonSettings{}, err
	}
	var tail any
	if v == nil || d.Decode(&tail) != io.EOF {
		return commonSettings{}, errors.New("expected one legacy settings object")
	}
	return v.commonSettings, nil
}

func readLegacyWindowSettings(path string) (commonSettings, error) {
	v, err := readHistoryVersion(path)
	if err != nil {
		return commonSettings{}, err
	}
	if v.info == nil || len(v.data) > 4096 {
		return commonSettings{}, errors.New("legacy settings file missing or above 4 KiB")
	}
	return decodeLegacyWindowSettings(v.data)
}

// Old diagnostics stay separate; they do not certify any Go task as complete.
func (a *application) openLegacyData() {
	if a.commonBusy || a.manual.busy() || a.manual.quitPending || a.languageBusy {
		return
	}
	a.commonBusy = true
	a.legacyDataError = nil
	previous := a.manual.phase
	a.manual.phase = manualChoosing
	title := a.tr("打开旧版设置或记录")
	detail := a.tr("只导入关闭到托盘和启动时最小化。旧文件、语言和开机自启保持不变。")
	locale := a.locale
	on, off := a.tr("开启"), a.tr("关闭")
	ok, cancel := a.tr("导入"), a.tr("取消")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: title})
		var settings *commonSettings
		var diagnostic string
		if err == nil && len(paths) == 1 {
			path := paths[0]
			switch strings.ToLower(filepath.Base(path)) {
			case "settings.json":
				var value commonSettings
				value, err = readLegacyWindowSettings(path)
				if err == nil {
					state := func(flag bool) string {
						if flag {
							return on
						}
						return off
					}
					message := messagef("关闭到托盘：%s；启动时最小化：%s", state(value.CloseToTray), state(value.StartMinimized))
					result, dialogErr := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.window, Title: title, Message: message.render(locale), Detail: detail, Buttons: []string{cancel, ok}, DefaultButton: 0, CancelButton: 0})
					err = dialogErr
					if err == nil && result.Button == 1 {
						settings = &value
					}
				}
			case "status.json", "syncmaid.log":
				err = inspectDestinationLogFile(path)
				if err == nil {
					diagnostic = path
				}
			default:
				err = errors.New("choose settings.json, status.json, or syncmaid.log")
			}
		}
		a.window.Update(func() {
			a.commonBusy = false
			a.manual.phase = previous
			if a.manual.quitPending {
				a.requestQuit()
				return
			}
			defer a.drainWatches()
			a.legacyDataError = err
			if settings != nil {
				if a.commonError != nil {
					a.legacyDataError = a.commonError
					return
				}
				a.changeCommon(*settings)
			}
			if diagnostic != "" {
				a.legacyDataError = mygo.Shell.OpenPath(pathLabel(diagnostic))
			}
		})
	}()
}
