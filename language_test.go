package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/syncengine"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func TestLanguageCatalogLoadsInMyGoI18n(t *testing.T) {
	for _, choice := range languageChoices {
		b, err := languageFiles.ReadFile("locales/" + choice.tag + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var table map[string]string
		if err := json.Unmarshal(b, &table); err != nil {
			t.Fatal(err)
		}
		for key, value := range table {
			if got := messages.Format(choice.tag, key, nil); got != value {
				t.Errorf("%s %q: catalog %q, want %q", choice.tag, key, got, value)
			}
		}
	}
}

func TestLanguageChangeKeepsDraftAndConfirmation(t *testing.T) {
	reply := make(chan error, 1)
	a := &application{locale: "en", settingsOpen: true, manual: manualSession{
		name: "完成", source: `C:\任务\原样`, cronText: "*/5 * * * *", dirty: true,
		phase: manualRunning, targetName: "Settings", mirrorReply: reply,
		mirrorPreview: &syncengine.MirrorPreview{Name: "任务名", Paths: []string{"保持.txt"}, Mode: taskconfig.Permanent},
		message:       messagef("任务存在失败或未执行目标: %s", "原始诊断"),
	}}
	before := a.manual
	wants := map[string]string{"en": "Settings", "ja": "設定", "zh-Hans": "设置", "zh-Hant": "設定"}
	for _, choice := range languageChoices {
		a.locale = choice.tag // The same UI-thread snapshot changed by MyGo's listener.
		tt := ui.NewTester(a.view, 1000, 780)
		if !tt.HasText(wants[choice.tag]) {
			t.Fatalf("%s: %v", choice.tag, tt.Texts())
		}
		if a.manual.name != before.name || a.manual.source != before.source || a.manual.cronText != before.cronText || !a.manual.dirty || a.manual.phase != manualRunning || a.manual.mirrorReply != reply || a.manual.mirrorPreview != before.mirrorPreview {
			t.Fatal("locale changed task/confirmation state")
		}
		if got := a.render(a.manual.message); !strings.HasSuffix(got, "原始诊断") || strings.Contains(got, "%!") {
			t.Fatal(got)
		}
	}
	if len(reply) != 0 {
		t.Fatal("switching language authorized removal")
	}
	if got := messagef("确认删除任务「%s」？", "完成").render("en"); got != "Delete task “完成”?" {
		t.Fatal(got)
	}
}

func TestLanguageStorageProtectionAndRestart(t *testing.T) {
	data := t.TempDir()
	if got, err := checkLanguageFile(data); got != "" || err != nil {
		t.Fatal(got, err)
	}
	for _, choice := range languageChoices {
		if err := prepareLanguageChange(data, choice.tag); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "locale"), []byte(choice.tag+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyLanguageChange(data, choice.tag); err != nil {
			t.Fatal(err)
		}
		// A fresh reader recovers the framework's one persisted authority.
		if got, err := checkLanguageFile(data); err != nil || got != choice.tag {
			t.Fatal(got, err)
		}
	}
	if err := verifyLanguageChange(data, "en"); err == nil {
		t.Fatal("silent framework save failure not reported")
	}
	if err := prepareLanguageChange(data, ""); err != nil {
		t.Fatal(err)
	}
	if err := verifyLanguageChange(data, ""); err == nil {
		t.Fatal("failed reset was not detected")
	}
	if err := os.Remove(filepath.Join(data, "locale")); err != nil {
		t.Fatal(err)
	}
	if err := verifyLanguageChange(data, ""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "{broken}", "fr", strings.Repeat("x", 129)} {
		path := filepath.Join(data, "locale")
		os.WriteFile(path, []byte(bad), 0600)
		if err := prepareLanguageChange(data, "ja"); err == nil {
			t.Fatal("allowed overwriting bad locale")
		}
		if err := prepareLanguageChange(data, ""); err == nil {
			t.Fatal("reset would remove malformed preference")
		}
		got, _ := os.ReadFile(path)
		if string(got) != bad {
			t.Fatal("bad data changed")
		}
	}
}

func TestLanguageReadAndSaveFailures(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "locale")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := prepareLanguageChange(data, "ja"); err == nil {
		t.Fatal("directory accepted")
	}
	os.Remove(path)
	if err := os.WriteFile(path, []byte("en\n"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+".tmp", []byte("unowned bytes"), 0600)
	if err := prepareLanguageChange(data, "ja"); err == nil {
		t.Fatal("unknown temporary file overwritten")
	}
	got, _ := os.ReadFile(path + ".tmp")
	if string(got) != "unowned bytes" {
		t.Fatal("temp changed")
	}
	a := application{languageBusy: true}
	want := errors.New("save failure")
	a.finishLanguageChange(want)
	if a.languageBusy || !errors.Is(a.languageError, want) {
		t.Fatal("failure hidden or busy stuck")
	}
	got, _ = os.ReadFile(path)
	if string(got) != "en\n" {
		t.Fatal("previous choice lost")
	}
}
