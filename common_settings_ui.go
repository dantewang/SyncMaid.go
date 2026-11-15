package main

import "github.com/egoist/mygo/ui"

func (a *application) closeWindow() {
	a.answerMirror(false)
	if a.commonValue.CloseToTray {
		a.lifecycle.closeRequested()
		return
	}
	a.requestQuit()
}
func (a *application) changeCommon(value commonSettings) {
	if a.commonBusy || a.common == nil || a.commonError != nil {
		return
	}
	a.commonBusy = true
	store := a.common
	go func() {
		err := store.save(value)
		a.window.Update(func() {
			a.commonBusy = false
			a.commonError = err
			if err == nil {
				a.commonValue = value
			}
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) reloadCommon() {
	if a.commonBusy {
		return
	}
	a.commonBusy = true
	data := a.installation.data
	go func() {
		s := loadCommonSettings(data)
		a.window.Update(func() {
			a.commonBusy = false
			a.common = s
			a.commonError = s.err
			if s.err == nil {
				a.commonValue = s.value
			}
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) setStartup(enabled bool) {
	if a.commonBusy || a.startupEntry.name == "" {
		return
	}
	a.commonBusy = true
	entry := a.startupEntry
	go func() {
		err := entry.set(enabled)
		status, readErr := entry.status()
		a.window.Update(func() {
			a.commonBusy = false
			a.startupStatus = status
			a.startupError = err
			if err == nil {
				a.startupError = readErr
			}
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) commonSettingsView(c *ui.Context) {
	locked := a.commonBusy || a.common == nil || a.commonError != nil
	closeLabel := "关闭窗口：退出"
	if a.commonValue.CloseToTray {
		closeLabel = "关闭窗口：隐藏到托盘"
	}
	startLabel := "启动时：显示窗口"
	if a.commonValue.StartMinimized {
		startLabel = "启动时：隐藏到托盘"
	}
	ui.Button(c, a.tr(closeLabel)).Disabled(locked).OnClick(func() { v := a.commonValue; v.CloseToTray = !v.CloseToTray; a.changeCommon(v) })
	ui.Button(c, a.tr(startLabel)).Disabled(locked).OnClick(func() { v := a.commonValue; v.StartMinimized = !v.StartMinimized; a.changeCommon(v) })
	ui.Button(c, a.tr("重新载入设置")).Disabled(a.commonBusy).OnClick(a.reloadCommon)
	if a.commonError != nil {
		ui.Text(c, a.tr("设置不可用或保存失败，原文保留：")+a.commonError.Error())
	}
	if a.commonBusy {
		ui.Text(c, a.tr("正在更新设置…"))
	}
	ui.Text(c, a.tr("开机自启：")+a.tr(string(a.startupStatus)))
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, a.tr("登记本副本自启")).Disabled(a.commonBusy).OnClick(func() { a.setStartup(true) })
		ui.Button(c, a.tr("移除本副本自启")).Disabled(a.commonBusy).OnClick(func() { a.setStartup(false) })
	})
	if a.startupError != nil {
		ui.Text(c, a.tr("自启操作或读取失败：")+a.startupError.Error())
	}
	ui.Text(c, a.tr("系统禁用请在任务管理器中处理；移动程序后需重新登记，旧路径项不会自动清理。"))
}
