package main

func (a *application) closeWindow() {
	a.sourcePreview.invalidate()
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
