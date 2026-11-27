package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"strings"
	"syncmaid/internal/atomicfile"
	"syncmaid/internal/buildinfo"
	"syncmaid/internal/winutil"
)

//go:embed mygo.json
var appMetadata []byte

func embeddedAppVersion() string {
	var metadata struct{ Version string }
	if err := json.Unmarshal(appMetadata, &metadata); err != nil {
		panic(err)
	}
	return metadata.Version
}

var (
	appBuild   = buildinfo.Resolve(embeddedAppVersion())
	appVersion = appBuild.Display()
)

func porcelainSection(c *ui.Context, title string, content func()) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, title).Font("Consolas").FontSize(11).TextColor(porcelain.TextMuted).Padding(0, 0, 6, 0)
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
		content()
	})
}
func porcelainSettingRow(c *ui.Context, title string, controls func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(16).Padding(11, 0).Children(func() {
		ui.Text(c, title).Grow(1).Shrink(1).MinWidth(100).FontWeight(500)
		controls()
	})
}
func (a *application) settingsView(c *ui.Context) {
	porcelainScroll(c, &a.scrolls.settings, func() {
		ui.Column(c).FillWidth().LineHeight(1.5).Gap(22).Padding(16, 22).Children(func() {
			ui.Text(c, a.tr("设置")).FontSize(18).FontWeight(500).Padding(0, 0, 6, 0)
			porcelainSection(c, a.tr("常规"), func() {
				porcelainSettingRow(c, a.tr("界面语言"), func() {
					ui.Row(c).Gap(0).Wrap().Shrink(1).MinWidth(0).Children(func() {
						porcelainSegment(c, a.tr("系统默认"), a.languagePreference == "").Disabled(a.languageBusy || a.manual.phase == manualLoading).OnClick(func() { a.changeLanguage("") })
						for _, i := range []int{0, 2, 3, 1} {
							choice := languageChoices[i]
							tag := choice.tag
							porcelainSegment(c, choice.name, a.languagePreference == tag).Disabled(a.languageBusy || a.manual.phase == manualLoading).OnClick(func() { a.changeLanguage(tag) })
						}
					})
				})
				if a.languageBusy {
					ui.Text(c, a.tr("正在保存语言…")).FontSize(12)
				}
				if a.languageError != nil {
					ui.Text(c, a.tr("语言设置未能保存或读取；请保留并检查 Data/locale 后重试。当前界面可能只在本次生效。")).TextColor(porcelain.Danger)
					ui.Text(c, a.languageError.Error()).FontSize(12)
				}
			})
			a.commonSettingsView(c)
			porcelainSection(c, a.tr("存储"), func() {
				porcelainSettingRow(c, a.tr("数据目录"), func() {
					porcelainButton(c, a.tr("打开数据目录")).OnClick(func() {
						a.openDataError = openShellPath(a.installation.Data)
					})
				})
				if a.openDataError != nil {
					ui.Text(c, a.tr("无法打开数据目录：%s", a.openDataError.Error())).TextColor(porcelain.Danger)
				}
				porcelainPath(c, winutil.PathLabel(a.installation.Data))
				porcelainButton(c, a.tr("打开旧版设置或记录")).Disabled(a.commonBusy || a.configBusy() || a.languageBusy).OnClick(a.openLegacyData)
				if a.legacyDataError != nil {
					ui.Text(c, a.tr("无法打开旧版设置或记录：%s", a.legacyDataError.Error())).TextColor(porcelain.Danger)
				}
			})
			porcelainSection(c, a.tr("任务与迁移"), func() {
				porcelainSettingRow(c, a.tr("导入旧任务"), func() {
					porcelainButton(c, a.tr("导入旧任务")).Disabled(!a.canCreateTask()).OnClick(a.beginImport)
				})
				ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
				porcelainSettingRow(c, a.tr("旧正则迁移"), func() {
					porcelainButton(c, a.tr("旧正则迁移")).Disabled(!a.canEditTask(a.manual.id) || a.manual.id == "").OnClick(a.beginRegexMigration)
				})
				ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
				porcelainSettingRow(c, a.tr("重新载入"), func() {
					ui.Row(c).Gap(8).Children(func() {
						porcelainButton(c, a.tr("重新载入")).Disabled(a.configBusy() || a.isRunning()).OnClick(a.reloadTasks)
						porcelainButton(c, a.tr("重新载入设置")).Disabled(a.commonBusy).OnClick(a.reloadCommon)
					})
				})
			})
			porcelainSection(c, a.tr("关于"), func() {
				ui.Row(c).Gap(14).AlignItems(ui.Center).Padding(11, 0).FillWidth().Children(func() {
					if maidAvatar != nil {
						ui.Image(c, maidAvatar).Size(40, 40)
					}
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "SyncMaid").Bold()
						ui.Text(c, appVersion).Font("Consolas").FontSize(14).TextColor(porcelain.TextMuted)
					})
					ribbonDangerButton(c, a.tr("Exit")).OnClick(a.requestQuit)
				})
			})

		})
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
}
func porcelainBoundSwitch(c *ui.Context, value bool, label string, disabled bool, onToggle func(bool)) {
	state := value
	porcelainSwitch(c, &state).Label(label).Disabled(disabled).OnChange(func() { onToggle(state) })
}

func (a *application) commonSettingsView(c *ui.Context) {
	locked := a.commonBusy || a.common == nil || a.commonError != nil
	porcelainSection(c, a.tr("启动"), func() {
		porcelainSettingRow(c, a.tr("开机自启：")+a.tr(string(a.startupStatus)), func() {
			enabled := a.startupStatus == winutil.StartupRegistered || a.startupStatus == winutil.StartupDisabled || a.startupStatus == winutil.StartupUnknown
			disabled := a.commonBusy || a.startupError != nil || a.startupEntry.Name == "" || a.startupStatus == winutil.StartupDisabled || a.startupStatus == winutil.StartupForeign
			porcelainBoundSwitch(c, enabled, a.tr("开机自启："), disabled, a.setStartup)
		})
		if a.startupError != nil {
			ui.Text(c, a.tr("自启操作或读取失败：")+a.startupError.Error()).FontSize(12).TextColor(porcelain.Danger)
		}
	})
	porcelainSection(c, a.tr("窗口"), func() {
		closeLabel := "关闭窗口：退出"
		if a.commonValue.CloseToTray {
			closeLabel = "关闭窗口：隐藏到托盘"
		}
		porcelainSettingRow(c, a.tr("关闭到托盘"), func() {
			porcelainBoundSwitch(c, a.commonValue.CloseToTray, a.tr(closeLabel), locked, func(next bool) {
				v := a.commonValue
				v.CloseToTray = next
				a.changeCommon(v)
			})
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
		startLabel := "启动时：显示窗口"
		if a.commonValue.StartMinimized {
			startLabel = "启动时：隐藏到托盘"
		}
		porcelainSettingRow(c, a.tr("启动时最小化"), func() {
			porcelainBoundSwitch(c, a.commonValue.StartMinimized, a.tr(startLabel), locked, func(next bool) {
				v := a.commonValue
				v.StartMinimized = next
				a.changeCommon(v)
			})
		})
		if a.commonError != nil {
			ui.Text(c, a.tr("设置不可用或保存失败，原文保留：")+a.commonError.Error()).TextColor(porcelain.Danger)
		}
		if a.commonBusy {
			ui.Text(c, a.tr("正在更新设置…")).FontSize(12)
		}
	})
}

func (a *application) closeWindow() {
	a.sourcePreview.invalidate()
	a.answerMirror(false)
	if a.commonValue.CloseToTray {
		a.lifecycle.closeRequested()
		return
	}
	a.requestQuit()
}
func (a *application) changeCommon(value winutil.CommonSettings) {
	if a.commonBusy || a.common == nil || a.commonError != nil {
		return
	}
	a.commonBusy = true
	store := a.common
	go func() {
		err := store.Save(value)
		a.window.Update(func() {
			a.commonBusy = false
			a.commonError = err
			if err == nil {
				a.commonValue = value
			}
			a.finishAsyncStep(false)
		})
	}()
}
func (a *application) reloadCommon() {
	if a.commonBusy {
		return
	}
	a.commonBusy = true
	data := a.installation.Data
	go func() {
		s := winutil.LoadCommonSettings(data)
		a.window.Update(func() {
			a.commonBusy = false
			a.common = s
			a.commonError = s.Err
			if s.Err == nil {
				a.commonValue = s.Value
			}
			a.finishAsyncStep(false)
		})
	}()
}
func (a *application) setStartup(enabled bool) {
	if a.commonBusy || a.startupEntry.Name == "" {
		return
	}
	a.commonBusy = true
	entry := a.startupEntry
	go func() {
		err := entry.Set(enabled)
		status, readErr := entry.Status()
		a.window.Update(func() {
			a.commonBusy = false
			a.startupStatus = status
			a.startupError = err
			if err == nil {
				a.startupError = readErr
			}
			a.finishAsyncStep(false)
		})
	}()
}

func decodeLegacyWindowSettings(raw []byte) (winutil.CommonSettings, error) {
	v, err := atomicfile.DecodeStrictJSONObject[struct {
		winutil.CommonSettings
		Language *string
	}](raw)
	if err != nil {
		return winutil.CommonSettings{}, err
	}
	return v.CommonSettings, nil
}

func readLegacyWindowSettings(path string) (winutil.CommonSettings, error) {
	v, err := atomicfile.ReadRegular(path, 4096)
	if err != nil {
		return winutil.CommonSettings{}, err
	}
	if v.Info == nil {
		return winutil.CommonSettings{}, errors.New("legacy settings file missing or above 4 KiB")
	}
	return decodeLegacyWindowSettings(v.Data)
}

// Old diagnostics stay separate; they do not certify any Go task as complete.
func (a *application) openLegacyData() {
	if a.commonBusy || a.configBusy() || a.quitPending || a.languageBusy {
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
		paths, err := e2eDialogOpen(mygo.OpenDialogOptions{Parent: a.window, Title: title})
		var settings *winutil.CommonSettings
		var diagnostic string
		if err == nil && len(paths) == 1 {
			path := paths[0]
			switch strings.ToLower(filepath.Base(path)) {
			case "settings.json":
				var value winutil.CommonSettings
				value, err = readLegacyWindowSettings(path)
				if err == nil {
					state := func(flag bool) string {
						if flag {
							return on
						}
						return off
					}
					message := messagef("关闭到托盘：%s；启动时最小化：%s", state(value.CloseToTray), state(value.StartMinimized))
					result, dialogErr := e2eDialogMessage(mygo.MessageOptions{Parent: a.window, Title: title, Message: message.render(locale), Detail: detail, Buttons: []string{cancel, ok}, DefaultButton: 0, CancelButton: 0})
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
			if a.quitPending {
				a.finishAsyncStep(false)
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
				a.legacyDataError = openShellPath(diagnostic)
			}
		})
	}()
}
