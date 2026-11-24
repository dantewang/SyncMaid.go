package main

import (
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

type appScrollStates struct {
	taskList      ui.ScrollState
	taskDetail    ui.ScrollState
	previewPanel  ui.ScrollState
	previewModal  ui.ScrollState
	taskModal     ui.ScrollState
	destModal     ui.ScrollState
	settings      ui.ScrollState
	historyList   ui.ScrollState
	historyDetail ui.ScrollState
	destLog       ui.ScrollState
	mirrorConfirm ui.ScrollState
	legacyImport  ui.ScrollState
	regexList     ui.ScrollState
	regexDetail   ui.ScrollState
}

type application struct {
	sourcePreview      sourcePreviewSession
	scrolls            appScrollStates
	batch              manualBatch
	destinationLog     *mygo.Window
	logTask            taskconfig.ID
	logDestination     taskconfig.ID
	logReading         bool
	logFileAvailable   bool
	logOpenError       error
	logDetails         bool
	historyDetails     bool
	cards              taskCardLayout
	regexSession       *regexSession
	importPreview      *appcore.LegacyImport
	importPrevious     manualPhase
	importError        error
	common             *winutil.CommonSettingsStore
	commonValue        winutil.CommonSettings
	commonBusy         bool
	commonError        error
	startupEntry       winutil.StartupEntry
	startupStatus      winutil.StartupStatus
	startupError       error
	history            *appcore.RunHistoryStore
	historyView        appcore.RunHistoryData
	historyError       error
	historyOpen        bool
	historySelected    taskconfig.ID
	topologyOpen       bool
	watchWorkers       sync.WaitGroup
	watches            map[taskconfig.ID]*watchedTask
	watchPhase         watchServicePhase
	watchActivity      localizedMessage
	settingsOpen       bool
	openDataError      error
	legacyDataError    error
	languagePreference string
	languageBusy       bool
	languageError      error
	locale             string // Native-thread snapshot of MyGo's language, not a second setting.
	installation       winutil.Installation
	lifecycle          lifecycle
	manual             manualSession
	liveTracker        *liveRunTracker
	liveSnapshot       liveRunSnapshot
	liveTickerStop     chan struct{}
	window             *mygo.Window
	tasks              *appcore.SavedTaskLibrary
	taskError          error
	recoveryNotice     localizedMessage
	results            map[taskconfig.ID]savedTaskResult
}

func (a *application) stopLiveTicker() {
	if a.liveTickerStop != nil {
		close(a.liveTickerStop)
		a.liveTickerStop = nil
	}
}

func (a *application) view(c *ui.Context) {
	a.manualView(c)
}

func run() (result error) {
	install, err := winutil.CurrentInstallation()
	if err != nil {
		return winutil.StartupProblem(err, winutil.StartupOwnership, "")
	}
	owner, err := winutil.AcquireOwnership(install)
	if errors.Is(err, winutil.ErrOccupied) {
		// Activation failure cannot grant writer status to this process.
		if activationError := winutil.NotifyInstallation(install); activationError != nil {
			return winutil.StartupProblem(activationError, winutil.StartupOccupied, install.Directory)
		}
		slog.Info("ownership-occupied", "directory", install.Directory)
		return nil
	}
	if err != nil {
		return winutil.StartupProblem(err, winutil.StartupOwnership, install.Data)
	}
	defer func() { result = errors.Join(result, owner.Close()) }()
	// Use the pinned target's path, not a mutable Data junction spelling.
	dataDirectory := owner.DataDirectory()
	install.Data = dataDirectory.Path
	a := &application{installation: install}
	a.common = winutil.LoadCommonSettings(install.Data)
	a.commonValue, a.commonError = a.common.Value, a.common.Err
	a.startupEntry, a.startupError = winutil.CurrentStartupEntry(install)
	if a.startupError == nil {
		a.startupStatus, a.startupError = a.startupEntry.Status()
	}
	defer a.watchWorkers.Wait()
	activation, err := winutil.StartActivation(dataDirectory.Key, func() { mygo.App.WhenReady(a.lifecycle.show) })
	if err != nil {
		slog.Warn("activation-server-unavailable", "error", err)
	} else {
		defer func() { result = errors.Join(result, activation.Close()) }()
	}
	mygo.App.SetName(install.Key)
	// These MyGo paths are Go filesystem I/O inputs (locale_file.go and
	// window_state.go), not Shell/UI labels. Preserve extended-length semantics.
	mygo.App.SetPath(mygo.PathUserData, install.Data)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(install.Data, "cache"))
	mygo.App.SetPath(mygo.PathLogs, filepath.Join(install.Data, "logs"))
	mygo.App.OnBeforeQuit(a.beforeQuit)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.stopLiveTicker(); a.stopWatches(watchesStopped); a.lifecycle.finish() })
	mygo.App.OnQuit(func() { slog.Info("application-exited") })
	var startupError error
	mygo.App.WhenReady(func() {
		a.locale = mygo.App.Locale()
		a.languagePreference, a.languageError = checkLanguageFile(install.Data)
		offLocale := mygo.App.OnLocaleChanged(func(tag string) {
			a.locale = tag
			redrawWindow(a.destinationLog)
		})
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { offLocale() })
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID: install.Key, Icon: porcelainAppIcon, ToolTip: "SyncMaid",
		})
		if err != nil {
			// Without a tray there must not be an unreachable hidden app.
			startupError = winutil.StartupProblem(err, winutil.StartupFramework, install.Directory)
			mygo.App.Quit()
			return
		}
		// Register immediately so a failed window initialization still releases it.
		a.lifecycle.destroyTray = tray.Destroy
		window, err := createStartupWindow(func() *mygo.Window {
			return mygo.NewWindow(mygo.WindowOptions{
				Title:          "SyncMaid",
				Hidden:         a.commonError == nil && a.commonValue.StartMinimized,
				Width:          1040,
				Height:         680,
				UseContentSize: true,
				MinWidth:       760,
				MinHeight:      480,
				Content:        ui.View(a.view),
			})
		})
		if err != nil {
			startupError = winutil.StartupProblem(err, winutil.StartupFramework, install.Directory)
			mygo.App.Quit()
			return
		}
		a.lifecycle.ready(window, mygo.App.Quit, tray.Destroy)
		if a.commonError == nil && a.commonValue.StartMinimized {
			a.lifecycle.closeRequested()
		}
		a.window = window
		if err := window.SetIcon(porcelainAppIcon); err != nil {
			slog.Warn("window-icon", "error", err)
		}
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
		slog.Info("native-ready", "directory", winutil.PathLabel(install.Directory), "data", winutil.PathLabel(install.Data), "trayBounds", tray.Bounds())
	})
	if err := mygo.App.Run(); err != nil {
		return winutil.StartupProblem(err, winutil.StartupFramework, install.Directory)
	}
	return startupError
}

func main() {
	os.Exit(winutil.RunStartup(run, showStartupNotice))
}

// A synchronous Win32 message box needs no MyGo event loop, config path or
// application-owned Data. Called on main after run returns and releases locks.
func showStartupNotice(notice winutil.StartupNotice) error {
	if e2eSuppressStartupNotice() {
		slog.Error("startup-notice-suppressed", "title", notice.Title, "message", notice.Message)
		return nil
	}
	text, err := windows.UTF16PtrFromString(notice.Message)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString(notice.Title)
	if err != nil {
		return err
	}
	result, err := windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TASKMODAL|windows.MB_SETFOREGROUND)
	if result == 0 && err == nil {
		return errors.New("native startup notice returned no result")
	}
	return err
}
