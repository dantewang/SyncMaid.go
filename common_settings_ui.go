package main

import "syncmaid/internal/winutil"

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
