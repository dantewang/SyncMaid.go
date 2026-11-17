package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarMenuIsSingleAlignedArrow(t *testing.T) {
	for _, lang := range languageChoices {
		a := &application{locale: lang.tag}
		view := ui.NewTester(a.view, 760, 620)
		gear, gearOK := view.Find(a.tr("设置"))
		menu, menuOK := view.Find(a.tr("更多操作"))
		if !gearOK || !menuOK || menu.W != 24 || menu.H != 24 || menu.Y != gear.Y || menu.W != gear.W || menu.H != gear.H {
			t.Fatalf("%s: gear=%+v (%v), menu=%+v (%v)", lang.tag, gear, gearOK, menu, menuOK)
		}
		if view.HasText("⋯") {
			t.Fatalf("%s: unwanted ellipsis beside built-in menu arrow", lang.tag)
		}
	}
}
