package main

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syncmaid/internal/syncengine"
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func TestLanguageCatalogKeysAndInterpolation(t *testing.T) {
	tables := map[string]map[string]string{}
	verbs := regexp.MustCompile(`%[-+.0-9]*[sdf]`)
	for _, choice := range languageChoices {
		b, err := languageFiles.ReadFile("locales/" + choice.tag + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var table map[string]string
		if err := json.Unmarshal(b, &table); err != nil {
			t.Fatal(err)
		}
		tables[choice.tag] = table
		for key, value := range table {
			if value == "" || !reflect.DeepEqual(verbs.FindAllString(key, -1), verbs.FindAllString(value, -1)) {
				t.Errorf("%s: invalid interpolation for %q", choice.tag, key)
			}
			if got := messages.Format(choice.tag, key, nil); got != value {
				t.Errorf("%s %q: catalog %q, want %q", choice.tag, key, got, value)
			}
		}
	}
	for tag, table := range tables {
		if len(table) != len(tables["en"]) {
			t.Errorf("%s missing entries", tag)
		}
		for key := range tables["en"] {
			if _, ok := table[key]; !ok {
				t.Errorf("%s missing %q", tag, key)
			}
		}
	}
	// Literal presentation keys must ship offline in every catalog. Dynamic
	// keys are controlled state labels/field labels covered below.
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			isMessage := false
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				isMessage = fun.Name == "messagef"
			case *ast.SelectorExpr:
				isMessage = fun.Sel.Name == "tr"
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !isMessage || !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			if strings.IndexFunc(key, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
				if _, ok := tables["en"][key]; !ok {
					t.Errorf("%s: missing key %q", name, key)
				}
			}
			return true
		})
	}
}

func TestLanguageEditorAndHistoryRegions(t *testing.T) {
	for _, choice := range languageChoices {
		t.Run(choice.tag, func(t *testing.T) {
			d := syncengine.ManualDestination{ID: taskconfig.NewID(), Name: "完成", Path: `C:\目標`, Filters: syncengine.AllFileRules(), Collision: taskconfig.Suffix}
			a := &application{locale: choice.tag, manual: manualSession{
				name: "Settings", source: `C:\源`, targets: []syncengine.ManualDestination{d}, targetID: d.ID, targetName: d.Name, target: d.Path,
				kind: taskconfig.Moving, filterEditor: filterEditor{Mode: filterGrouped, Groups: []filterDraftGroup{{}}},
				trigger: taskconfig.Trigger{Kind: "scheduled"}, cronText: "*/5 * * * *", cronNext: "2026-11-15 10:00:00 CST",
			}}
			a.cards.editor = true
			view := ui.NewTester(a.view, 1000, 1400)
			a.cards.destinations = true
			destinationView := ui.NewTester(a.view, 1000, 1400)
			for _, key := range []string{"任务名称", "路径前缀", "任一规则", "保留源", "加编号", "cron表达式"} {
				if !view.HasText(a.tr(key)) && !destinationView.HasText(a.tr(key)) {
					t.Errorf("missing translated editor region %q", key)
				}
			}
			if a.manual.name != "Settings" || a.manual.source != `C:\源` || a.manual.targetName != "完成" || a.manual.cronText != "*/5 * * * *" {
				t.Fatal("user fields changed")
			}
			r := runRecord{ID: taskconfig.NewID(), TaskID: taskconfig.NewID(), Name: "完成", Outcome: "stopped", Goals: []runGoal{{ID: d.ID, Name: d.Name, State: syncengine.DestinationCanceled}}}
			a.historyView = runHistoryData{Version: 1, Recent: []runRecord{r}}
			a.historyOpen = true
			view = ui.NewTester(a.view, 1000, 1400)
			for _, key := range []string{"近期运行记录", "已停止"} {
				if !strings.Contains(strings.Join(view.Texts(), "\n"), a.tr(key)) {
					t.Errorf("missing translated history %q", key)
				}
			}
		})
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
