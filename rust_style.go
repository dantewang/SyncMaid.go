package main

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/porcelain-icons/*.svg assets/porcelain-icons/LICENSE
var porcelainIcons embed.FS

//go:embed assets/syncmaid.png
var porcelainAppIcon []byte

var maidAvatar = func() *ui.Bitmap {
	bmp, err := ui.DecodeBitmap(porcelainAppIcon)
	if err != nil {
		return nil
	}
	return bmp
}()

var porcelainGlyphs = func() map[string]*ui.SVG {
	names := map[string]string{
		"♢":      "pencil",
		"▷":      "play",
		"⌄":      "chevron-down",
		"up":     "arrow-up",
		"›":      "chevron-right",
		"＋":      "plus",
		"⚙":      "settings",
		"menu":   "menu",
		"□":      "folder",
		"■":      "circle-stop",
		"×":      "close",
		"⌫":      "trash-2",
		"sync":   "refresh-cw",
		"move":   "split",
		"manual": "mouse-pointer-click",
		"clock":  "clock",
		"eye":    "eye",
		"filter": "funnel",
		"copy":   "copy",
		"check":  "check",
	}
	out := map[string]*ui.SVG{}
	for key, name := range names {
		data, err := porcelainIcons.ReadFile("assets/porcelain-icons/" + name + ".svg")
		if err != nil {
			panic(err)
		}
		out[key] = ui.MustParseSVG(data)
	}
	return out
}()

// Ribbon Minimal tokens anchored on International Klein Blue (#002FA7) and cool silver.
func rgb(v uint32) ui.Color {
	return ui.Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

var (
	ribbonRailBg      = rgb(0xEBEEF5)
	ribbonSidebarBg   = rgb(0xF3F5FA)
	ribbonSurfaceCard = rgb(0xFFFFFF)
	ribbonBlue        = rgb(0x002FA7)
	ribbonBlueHover   = rgb(0x002585)
	ribbonBluePressed = rgb(0x001B63)
	ribbonBlueSoft    = rgb(0xEAF0FF)
	ribbonSuccess     = rgb(0x16794B)
	ribbonMove        = rgb(0x3154E8)
	ribbonWarnSoft    = rgb(0xFDF6EB)
)

var porcelain = func() ui.Theme {
	t := ui.LightTheme()
	t.Background = rgb(0xF8F9FC)
	t.Surface = ribbonSurfaceCard
	t.SurfaceHover = rgb(0xE6EBF5)
	t.SurfacePressed = rgb(0xD8E0F0)
	t.Text = rgb(0x191C22)
	t.TextMuted = rgb(0x5E677A)
	t.Border = rgb(0xCCD2DF)
	t.Accent = t.Text
	t.InactiveAccent = t.Accent
	t.AccentText = rgb(0xF7FAFC)
	t.AccentHover = ribbonBlueHover
	t.AccentPressed = ribbonBluePressed
	t.Danger = rgb(0xC62F3C)
	t.Warning = rgb(0xB4690E)
	t.Success = ribbonSuccess
	t.Focus = ribbonBlue
	t.Selection = rgb(0xB9CCFF)
	t.FontSize = 15
	t.Radius = 0
	t.Spacing = 4
	return *t
}()

func paintRibbonStripe(e ui.Element) ui.Element {
	return e.DrawOver(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: 1.5, H: r.H}, porcelain.Text, 0)
		p.Fill(ui.Rect{X: r.X + 2.5, Y: r.Y, W: 1.5, H: r.H}, ribbonBlue, 0)
	})
}

func porcelainChoice(c *ui.Context, glyph, title, body string, selected, disabled bool, choose func()) ui.Element {
	e := ui.ButtonBase(c).Label(title).FillWidth().Justify(ui.Start).Gap(12).Padding(10, 12).MinHeight(68).Border(1, porcelain.Border).Background(porcelain.Surface).Disabled(disabled)
	if selected {
		e = e.Background(ribbonBlueSoft).Border(1, porcelain.Text)
	}
	return e.Children(func() {
		ui.Icon(c, porcelainGlyphs[glyph]).Size(22, 22)
		ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
			ui.Text(c, title).Bold()
			ui.Text(c, body).FontSize(13).TextColor(porcelain.TextMuted)
		})
	}).OnClick(choose)
}

func porcelainButton(c *ui.Context, text string) ui.Element {
	return ui.Button(c, text).Radius(0).Padding(5, 10).FontSize(14).Background(porcelain.Surface).Border(1, porcelain.Border).TextColor(porcelain.Text)
}

func porcelainPrimaryButton(c *ui.Context, text string) ui.Element {
	b := ui.ButtonBase(c).Label(text).Radius(0).Padding(5, 12).TextColor(porcelain.AccentText).Children(func() {
		ui.Text(c, text).FontSize(14).FontWeight(600)
	})
	bg := ribbonBlue
	if b.Pressed() {
		bg = ribbonBluePressed
	} else if b.Hovered() {
		bg = ribbonBlueHover
	}
	return b.Border(1, bg).Background(bg)
}

func ribbonInkButton(c *ui.Context, text string) ui.Element {
	return ui.PrimaryButton(c, text).Radius(0).Padding(5, 12).FontSize(14)
}

func ribbonDangerButton(c *ui.Context, text string) ui.Element {
	return ui.ButtonBase(c).Label(text).Radius(0).Padding(5, 12).Border(1, porcelain.Danger).Background(rgb(0xFBECEE)).TextColor(porcelain.Danger).Children(func() {
		ui.Text(c, text).FontSize(14).FontWeight(600)
	})
}

func porcelainSegment(c *ui.Context, text string, selected bool, glyph ...string) ui.Element {
	bg := porcelain.Surface
	border := porcelain.Border
	fg := porcelain.TextMuted
	if selected {
		bg = ribbonBlueSoft
		border = ribbonBlue
		fg = porcelain.Text
	}
	return ui.ButtonBase(c).Label(text).Selected(selected).Gap(6).Padding(5, 10).Border(1, border).Background(bg).TextColor(fg).Children(func() {
		if len(glyph) > 0 && porcelainGlyphs[glyph[0]] != nil {
			ui.Icon(c, porcelainGlyphs[glyph[0]]).Size(15, 15)
		}
		ui.Text(c, text).FontSize(14)
	})
}

func porcelainSwitch(c *ui.Context, on *bool) ui.Element {
	bg, align := rgb(0xC3C9D9), ui.Start
	if *on {
		bg = ribbonBlue
		align = ui.End
	}
	return ui.SwitchBase(c, on).Size(36, 20).Padding(2).Justify(align).Background(bg).Children(func() {
		ui.Box(c).Size(16, 16).Background(porcelain.Surface)
	})
}

func porcelainIcon(c *ui.Context, glyph, label string) ui.Element {
	return ui.Button(c, "").Padding(0).Radius(0).Size(24, 24).Label(label).Tooltip(label).Shrink(0).Background(porcelain.Surface).Border(1, porcelain.Border).Children(func() {
		ui.Icon(c, porcelainGlyphs[glyph]).Size(15, 15)
	})
}

func porcelainPrimaryIcon(c *ui.Context, glyph, label string) ui.Element {
	return ui.PrimaryButton(c, "").Padding(0).Radius(0).Size(24, 24).Label(label).Tooltip(label).Shrink(0).Children(func() {
		ui.Icon(c, porcelainGlyphs[glyph]).Size(15, 15)
	})
}

func porcelainSidebarIcon(c *ui.Context, glyph, label string, disabled bool) ui.Element {
	return ui.ButtonBase(c).Padding(0).Radius(0).Size(28, 28).Border(0, ui.Transparent).Label(label).Tooltip(label).Shrink(0).Disabled(disabled).Children(func() {
		ui.Icon(c, porcelainGlyphs[glyph]).Size(16, 16)
	})
}

func styleSidebarIcon(b ui.Element, active bool) {
	bg, fg := ui.Transparent, porcelain.Text
	if active {
		bg, fg = porcelain.Accent, porcelain.AccentText
		if b.Pressed() {
			bg = porcelain.AccentPressed
		} else if b.Hovered() {
			bg = porcelain.AccentHover
		}
	} else if b.Pressed() {
		bg = porcelain.Border
	} else if b.Hovered() {
		bg = porcelain.SurfaceHover
	}
	b.Background(bg).TextColor(fg)
}

func porcelainPath(c *ui.Context, path string) {
	ui.Text(c, path).Font("Consolas").FontSize(13).TextColor(porcelain.TextMuted).FillWidth().SingleLine().Ellipsis("…").Tooltip(path)
}
