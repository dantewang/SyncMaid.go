package main

import (
	"embed"
	"github.com/egoist/mygo/ui"
)

//go:embed assets/porcelain-icons/*.svg assets/porcelain-icons/LICENSE
var porcelainIcons embed.FS

//go:embed assets/syncmaid.png
var porcelainAppIcon []byte
var porcelainGlyphs = func() map[string]*ui.SVG {
	names := map[string]string{"♢": "pencil", "▷": "play", "⌄": "chevron-down", "up": "arrow-up", "›": "chevron-right", "＋": "plus", "⚙": "settings", "□": "folder", "■": "circle-stop", "×": "close", "⌫": "trash-2", "sync": "refresh-cw", "move": "split", "manual": "mouse-pointer-click", "clock": "clock", "eye": "eye", "filter": "funnel", "copy": "copy", "check": "check"}
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

// Porcelain tokens from the Rust application's theme.rs.
func rgb(v uint32) ui.Color {
	return ui.Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func porcelainChoice(c *ui.Context, glyph, title, body string, selected, disabled bool, choose func()) ui.Element {
	e := ui.ButtonBase(c).FillWidth().Justify(ui.Start).Gap(12).Padding(12).MinHeight(72).Border(1, porcelain.Border).Disabled(disabled)
	if selected {
		e = e.Background(rgb(0xDEF0F4)).Border(1, porcelain.Text)
	}
	return e.Children(func() {
		ui.Icon(c, porcelainGlyphs[glyph]).Size(24, 24)
		ui.Column(c).Grow(1).Shrink(1).Gap(6).Children(func() { ui.Text(c, title).Bold(); ui.Text(c, body).FontSize(14).TextColor(porcelain.TextMuted) })
	}).OnClick(choose)
}

var porcelain = func() ui.Theme {
	t := ui.LightTheme()
	t.Background = rgb(0xF8FAFB)
	t.Surface = t.Background
	t.SurfaceHover = rgb(0xE7EAEC)
	t.SurfacePressed = rgb(0xD9DFE3)
	t.Text = rgb(0x191C22)
	t.TextMuted = rgb(0x7C878F)
	t.Border = rgb(0xC2CBD1)
	t.Accent = t.Text
	t.InactiveAccent = t.Accent
	t.AccentText = rgb(0xF2F6F7)
	t.AccentHover = rgb(0x116F8E)
	t.AccentPressed = rgb(0x0C4C61)
	t.Danger = rgb(0xC62F3C)
	t.Warning = rgb(0x8A6410)
	t.Success = t.Text
	t.Focus = rgb(0x116F8E)
	t.Selection = rgb(0xA5E5EF)
	t.FontSize = 16
	t.Radius = 0
	t.Spacing = 4
	return *t
}()

func porcelainButton(c *ui.Context, text string) ui.Element {
	return ui.Button(c, text).Radius(0).Padding(5, 10).FontSize(16)
}
func porcelainPrimaryButton(c *ui.Context, text string) ui.Element {
	return ui.PrimaryButton(c, text).Radius(0).Padding(5, 10).FontSize(16)
}
func porcelainSegment(c *ui.Context, text string, selected bool, glyph ...string) ui.Element {
	bg := porcelain.Background
	if selected {
		bg = rgb(0xD9DFE3)
	}
	return ui.ButtonBase(c).Label(text).Selected(selected).Gap(8).Padding(5, 10).Border(1, porcelain.Border).Background(bg).TextColor(porcelain.Text).Children(func() {
		if len(glyph) > 0 && porcelainGlyphs[glyph[0]] != nil {
			ui.Icon(c, porcelainGlyphs[glyph[0]]).Size(16, 16)
		}
		ui.Text(c, text).FontSize(16)
	})
}
func porcelainSwitch(c *ui.Context, on *bool) ui.Element {
	bg, align := rgb(0xC2CBD1), ui.Start
	if *on {
		bg = porcelain.Accent
		align = ui.End
	}
	return ui.SwitchBase(c, on).Size(36, 20).Padding(2).Justify(align).Background(bg).Children(func() { ui.Box(c).Size(16, 16).Background(porcelain.Background) })
}
func porcelainIcon(c *ui.Context, glyph, label string) ui.Element {
	return ui.Button(c, "").Padding(0).Radius(0).Size(24, 24).Label(label).Tooltip(label).Shrink(0).Border(1, porcelain.Border).Children(func() { ui.Icon(c, porcelainGlyphs[glyph]).Size(16, 16) })
}
func porcelainPrimaryIcon(c *ui.Context, glyph, label string) ui.Element {
	return ui.PrimaryButton(c, "").Padding(0).Radius(0).Size(24, 24).Label(label).Tooltip(label).Shrink(0).Border(1, porcelain.Border).Children(func() { ui.Icon(c, porcelainGlyphs[glyph]).Size(16, 16) })
}
func porcelainTag(c *ui.Context, text string, primary bool) {
	pip := rgb(0x5B666E)
	e := ui.Row(c).Gap(6).LineHeight(1).Shrink(0).Padding(4, 10).Border(1, porcelain.Border).TextColor(porcelain.TextMuted).Tooltip(text)
	if primary {
		pip = porcelain.AccentText
		e = e.Background(porcelain.Accent).TextColor(porcelain.AccentText).Border(1, porcelain.Accent)
	}
	e.Children(func() {
		ui.Box(c).Size(6, 6).Background(pip)
		ui.Text(c, text).FontSize(12).FontWeight(400).SingleLine()
	})
}
func porcelainPath(c *ui.Context, path string) {
	ui.Text(c, path).Font("Consolas").FontSize(13).TextColor(porcelain.TextMuted).FillWidth().SingleLine().Ellipsis("…").Tooltip(path)
}
