package main

import (
	"errors"
	"log"
	"log/slog"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type application struct {
	installation installation
	lifecycle    lifecycle
}

func (a *application) view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(16).Padding(24).Children(func() {
		ui.Text(c, "SyncMaid").FontSize(26).Bold()
		ui.Text(c, "Native UI project initialized.").TextColor(c.Theme().TextMuted)
		ui.Text(c, "Close hides to the tray. Use Exit to quit.")
		ui.Text(c, "Data: "+pathLabel(a.installation.data)).FontSize(12)
		ui.Button(c, "Exit").OnClick(a.lifecycle.exit)
	})
}

func run() (result error) {
	install, err := currentInstallation()
	if err != nil {
		return err
	}
	owner, err := acquireOwnership(install)
	if errors.Is(err, errOccupied) {
		// Activation failure cannot grant writer status to this process.
		notifyInstallation(install)
		slog.Info("ownership-occupied", "directory", install.directory)
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, owner.Close()) }()
	// Use the pinned target's path, not a mutable Data junction spelling.
	dataDirectory := owner.dataDirectory()
	install.data = dataDirectory.path
	a := &application{installation: install}
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
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.lifecycle.beginQuit() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.lifecycle.finish() })
	mygo.App.OnQuit(func() { slog.Info("application-exited") })
	var startupError error
	mygo.App.WhenReady(func() {
		icon, err := trayIcon()
		if err != nil {
			startupError = err
			mygo.App.Quit()
			return
		}
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID: install.key, Icon: icon, ToolTip: "SyncMaid",
		})
		if err != nil {
			// Without a tray there must not be an unreachable hidden app.
			startupError = err
			mygo.App.Quit()
			return
		}
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:     "SyncMaid",
			Width:     640,
			Height:    420,
			MinWidth:  320,
			MinHeight: 280,
			Content:   ui.View(a.view),
		})
		a.lifecycle.ready(window, mygo.App.Quit, tray.Destroy)
		window.OnClose(func(e *mygo.CloseEvent) {
			if a.lifecycle.closeRequested() {
				e.PreventDefault()
			}
		})
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Show main window", Click: func(*mygo.MenuItem, *mygo.Window) { a.lifecycle.show() }},
			{Label: "Exit", Click: func(*mygo.MenuItem, *mygo.Window) { a.lifecycle.exit() }},
		})
		tray.OnClick(a.lifecycle.show)
		tray.OnRightClick(func() {
			tray.SetMenu(menu)
			defer tray.SetMenu(nil)
			tray.PopUpMenu()
		})
		slog.Info("native-ready", "directory", pathLabel(install.directory), "data", pathLabel(install.data), "trayBounds", tray.Bounds())
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	return startupError
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
