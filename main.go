package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type application struct {
	cards           taskCardLayout
	regexSession    *regexSession
	importPreview   *legacyImport
	importPrevious  manualPhase
	importError     error
	common          *commonSettingsStore
	commonValue     commonSettings
	commonBusy      bool
	commonError     error
	startupEntry    startupEntry
	startupStatus   startupStatus
	startupError    error
	history         *runHistoryStore
	historyView     runHistoryData
	historyError    error
	historyOpen     bool
	historySelected taskconfig.ID
	watchWorkers    sync.WaitGroup
	watches         map[taskconfig.ID]*watchedTask
	watchPhase      watchServicePhase
	watchActivity   localizedMessage
	settingsOpen    bool
	languageBusy    bool
	languageError   error
	locale          string // Native-thread snapshot of MyGo's language, not a second setting.
	installation    installation
	lifecycle       lifecycle
	manual          manualSession
	window          *mygo.Window
	tasks           *savedTaskLibrary
	taskError       error
	results         map[taskconfig.ID]savedTaskResult
}

func (a *application) view(c *ui.Context) {
	a.manualView(c)
}

func run() (result error) {
	install, err := currentInstallation()
	if err != nil {
		return startupProblem(err, startupOwnership, "")
	}
	owner, err := acquireOwnership(install)
	if errors.Is(err, errOccupied) {
		// Activation failure cannot grant writer status to this process.
		if activationError := notifyInstallation(install); activationError != nil {
			return startupProblem(activationError, startupOccupied, install.directory)
		}
		slog.Info("ownership-occupied", "directory", install.directory)
		return nil
	}
	if err != nil {
		return startupProblem(err, startupOwnership, install.data)
	}
	defer func() { result = errors.Join(result, owner.Close()) }()
	// Use the pinned target's path, not a mutable Data junction spelling.
	dataDirectory := owner.dataDirectory()
	install.data = dataDirectory.path
	a := &application{installation: install}
	a.common = loadCommonSettings(install.data)
	a.commonValue, a.commonError = a.common.value, a.common.err
	a.startupEntry, a.startupError = currentStartupEntry(install)
	if a.startupError == nil {
		a.startupStatus, a.startupError = a.startupEntry.status()
	}
	defer a.watchWorkers.Wait()
	activation, err := startActivation(dataDirectory.key, func() { mygo.App.WhenReady(a.lifecycle.show) })
	if err != nil {
		slog.Warn("activation-server-unavailable", "error", err)
	} else {
		defer func() { result = errors.Join(result, activation.Close()) }()
	}
	mygo.App.SetName(install.key)
	// These MyGo paths are Go filesystem I/O inputs (locale_file.go and
	// window_state.go), not Shell/UI labels. Preserve extended-length semantics.
	mygo.App.SetPath(mygo.PathUserData, install.data)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(install.data, "cache"))
	mygo.App.SetPath(mygo.PathLogs, filepath.Join(install.data, "logs"))
	mygo.App.OnBeforeQuit(a.beforeQuit)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.stopWatches(watchesStopped); a.lifecycle.finish() })
	mygo.App.OnQuit(func() { slog.Info("application-exited") })
	var startupError error
	mygo.App.WhenReady(func() {
		a.locale = mygo.App.Locale()
		offLocale := mygo.App.OnLocaleChanged(func(tag string) { a.locale = tag })
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { offLocale() })
		icon, err := trayIcon()
		if err != nil {
			startupError = startupProblem(err, startupFramework, install.directory)
			mygo.App.Quit()
			return
		}
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID: install.key, Icon: icon, ToolTip: "SyncMaid",
		})
		if err != nil {
			// Without a tray there must not be an unreachable hidden app.
			startupError = startupProblem(err, startupFramework, install.directory)
			mygo.App.Quit()
			return
		}
		// Register immediately so a failed window initialization still releases it.
		a.lifecycle.destroyTray = tray.Destroy
		window, err := createStartupWindow(func() *mygo.Window {
			return mygo.NewWindow(mygo.WindowOptions{
				Title:     "SyncMaid",
				Hidden:    a.commonError == nil && a.commonValue.StartMinimized,
				Width:     1000,
				Height:    700,
				MinWidth:  760,
				MinHeight: 560,
				Content:   ui.View(a.view),
			})
		})
		if err != nil {
			startupError = startupProblem(err, startupFramework, install.directory)
			mygo.App.Quit()
			return
		}
		a.lifecycle.ready(window, mygo.App.Quit, tray.Destroy)
		if a.commonError == nil && a.commonValue.StartMinimized {
			a.lifecycle.closeRequested()
		}
		a.window = window
		a.reloadTasks()
		window.OnClose(func(e *mygo.CloseEvent) {
			if a.lifecycle.phase == exiting || a.lifecycle.phase == stopped {
				return
			}
			e.PreventDefault()
			a.closeWindow()
		})
		tray.OnClick(a.lifecycle.show)
		tray.OnRightClick(func() {
			tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
				{Label: a.tr("Show main window"), Click: func(*mygo.MenuItem, *mygo.Window) { a.lifecycle.show() }},
				{Label: a.tr("Exit"), Click: func(*mygo.MenuItem, *mygo.Window) { a.requestQuit() }},
			}))
			defer tray.SetMenu(nil)
			tray.PopUpMenu()
		})
		slog.Info("native-ready", "directory", pathLabel(install.directory), "data", pathLabel(install.data), "trayBounds", tray.Bounds())
	})
	if err := mygo.App.Run(); err != nil {
		return startupProblem(err, startupFramework, install.directory)
	}
	return startupError
}

func main() {
	os.Exit(runStartup(run, showStartupNotice))
}
