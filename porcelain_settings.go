package main

import (
	_ "embed"
	"encoding/json"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

//go:embed mygo.json
var appMetadata []byte

var appVersion = func() string {
	var metadata struct{ Version string }
	if err := json.Unmarshal(appMetadata, &metadata); err != nil {
		panic(err)
	}
	return metadata.Version
}()

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
	ui.Scroll(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
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
						a.openDataError = mygo.Shell.OpenPath(pathLabel(a.installation.data))
					})
				})
				if a.openDataError != nil {
					ui.Text(c, a.tr("无法打开数据目录：%s", a.openDataError.Error())).TextColor(porcelain.Danger)
				}
				porcelainPath(c, pathLabel(a.installation.data))
				porcelainButton(c, a.tr("打开旧版设置或记录")).Disabled(a.commonBusy || a.manual.busy() || a.languageBusy).OnClick(a.openLegacyData)
				if a.legacyDataError != nil {
					ui.Text(c, a.tr("无法打开旧版设置或记录：%s", a.legacyDataError.Error())).TextColor(porcelain.Danger)
				}
			})
			porcelainSection(c, a.tr("关于"), func() {
				ui.Row(c).Gap(14).Padding(11, 0).Children(func() {
					ui.Text(c, "SyncMaid").Bold()
					ui.Text(c, appVersion).Font("Consolas").FontSize(14).TextColor(porcelain.TextMuted)
				})
			})

		})
	})
}
func (a *application) commonSettingsView(c *ui.Context) {
	locked := a.commonBusy || a.common == nil || a.commonError != nil
	porcelainSection(c, a.tr("启动"), func() {
		porcelainSettingRow(c, a.tr("开机自启：")+a.tr(string(a.startupStatus)), func() {
			enabled := a.startupStatus == startupRegistered || a.startupStatus == startupDisabled || a.startupStatus == startupUnknown
			porcelainSwitch(c, &enabled).Label(a.tr("开机自启：")).Disabled(a.commonBusy || a.startupError != nil || a.startupEntry.name == "" || a.startupStatus == startupDisabled || a.startupStatus == startupForeign).OnChange(func() { a.setStartup(enabled) })
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
			enabled := a.commonValue.CloseToTray
			porcelainSwitch(c, &enabled).Label(a.tr(closeLabel)).Disabled(locked).OnChange(func() { v := a.commonValue; v.CloseToTray = enabled; a.changeCommon(v) })
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
		startLabel := "启动时：显示窗口"
		if a.commonValue.StartMinimized {
			startLabel = "启动时：隐藏到托盘"
		}
		porcelainSettingRow(c, a.tr("启动时最小化"), func() {
			enabled := a.commonValue.StartMinimized
			porcelainSwitch(c, &enabled).Label(a.tr(startLabel)).Disabled(locked).OnChange(func() { v := a.commonValue; v.StartMinimized = enabled; a.changeCommon(v) })
		})
		if a.commonError != nil {
			ui.Text(c, a.tr("设置不可用或保存失败，原文保留：")+a.commonError.Error()).TextColor(porcelain.Danger)
		}
		if a.commonBusy {
			ui.Text(c, a.tr("正在更新设置…")).FontSize(12)
		}
	})
}
