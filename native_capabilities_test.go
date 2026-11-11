package main

import (
	"testing"
	"testing/fstest"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// Catalog formatting is usable from Go-only views without a web frontend or
// generated bindings. This does not register global locale state or persist it.
func TestNativeCatalogFourLanguageViews(t *testing.T) {
	files := fstest.MapFS{
		"en.json":      {Data: []byte(`{"greeting":"Ready, {name}!"}`)},
		"ja.json":      {Data: []byte(`{"greeting":"準備完了、{name}！"}`)},
		"zh-Hans.json": {Data: []byte(`{"greeting":"准备好，{name}！"}`)},
		"zh-Hant.json": {Data: []byte(`{"greeting":"準備好，{name}！"}`)},
	}
	catalog, err := i18n.NewCatalog(files, "en")
	if err != nil {
		t.Fatal(err)
	}
	for language, want := range map[string]string{
		"en": "Ready, SyncMaid!", "ja": "準備完了、SyncMaid！",
		"zh-Hans": "准备好，SyncMaid！", "zh-Hant": "準備好，SyncMaid！",
	} {
		t.Run(language, func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Text(c, catalog.Format(language, "greeting", map[string]any{"name": "SyncMaid"}))
			}, 520, 120)
			if !tt.HasText(want) {
				t.Fatalf("language %s: %q", language, tt.Texts())
			}
		})
	}
}

// This exercises Unicode text events and focus routing, not OS IME composition.
func TestNativeUnicodeEditingAndFocus(t *testing.T) {
	first, second := "", ""
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(12).Children(func() {
			ui.TextInput(c, &first).Label("First input").Width(380)
			ui.TextInput(c, &second).Label("Second input").Width(380)
			ui.Text(c, "First: "+first)
			ui.Text(c, "Second: "+second)
		})
	}, 520, 300)
	if err := tt.Click("First input"); err != nil {
		t.Fatal(err)
	}
	tt.Type("中文編輯测试")
	tt.Key(0, ui.KeyBackspace)
	if first != "中文編輯测" || !tt.HasText("First: 中文編輯测") {
		t.Fatalf("Unicode edit: %q %q", first, tt.Texts())
	}
	if err := tt.Click("Second input"); err != nil {
		t.Fatal(err)
	}
	tt.Type("焦点")
	if second != "焦点" || first != "中文編輯测" {
		t.Fatalf("focus routing: %q %q", first, second)
	}
	if err := tt.Click("First input"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Type("试")
	if first != "中文編輯测试" || second != "焦点" {
		t.Fatalf("restored focus: %q %q", first, second)
	}
}
