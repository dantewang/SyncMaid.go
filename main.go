package main

import (
	"log"
	"log/slog"
	"os"
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
		ui.Text(c, "Data: "+a.installation.data).FontSize(12)
		ui.Button(c, "Exit").OnClick(a.lifecycle.exit)
	})
}

func run() error {
	install, err := currentInstallation()
	if err != nil {
		return err
	}
	a := &application{installation: install}
	// MyGo's lock key uses App.Name (plus the packaged identifier), so a
	// directory-derived internal name scopes the lock to this portable copy.
	mygo.App.SetName(install.key)
	mygo.App.SetPath(mygo.PathUserData, install.data)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(install.data, "cache"))
	mygo.App.SetPath(mygo.PathLogs, filepath.Join(install.data, "logs"))
	mygo.App.OnSecondInstance(func([]string, string) {
		slog.Info("second-instance", "directory", install.directory)
		a.lifecycle.show()
	})
	if !mygo.App.RequestSingleInstanceLock() {
		slog.Info("activation-forwarded", "directory", install.directory)
		return nil
	}
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.lifecycle.beginQuit() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.lifecycle.finish() })
	mygo.App.OnQuit(func() { slog.Info("application-exited") })
	var startupError error
	mygo.App.WhenReady(func() {
		// Only the winning instance prepares its own portable directory.
		if err := os.MkdirAll(install.data, 0o755); err != nil {
			startupError = err
			mygo.App.Quit()
			return
		}
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
		slog.Info("native-ready", "directory", install.directory, "data", install.data, "trayBounds", tray.Bounds())
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
