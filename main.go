package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// view only builds native UI. File synchronization and persistence are not
// part of the initial shell.
func view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(16).Padding(24).Children(func() {
		ui.Text(c, "SyncMaid").FontSize(26).Bold()
		ui.Text(c, "Native UI project initialized.").TextColor(c.Theme().TextMuted)
	})
}

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "SyncMaid",
			Width:     640,
			Height:    420,
			MinWidth:  320,
			MinHeight: 280,
			Content:   ui.View(view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
