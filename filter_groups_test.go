package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestNativeFilterProjectionKeepsMeaningAndOriginal(t *testing.T) {
	leaf := taskconfig.ExtensionFilter("*.*.jpg")
	cases := [][]taskconfig.Rule{
		{filterGroup("anyOf", []taskconfig.Rule{filterGroup("allOf", []taskconfig.Rule{taskconfig.PathFilter("中文"), taskconfig.ExtensionFilter("txt")}), filterGroup("anyOf", []taskconfig.Rule{taskconfig.ExtensionFilter("jpg")})})},
		{leaf},
		{filterGroup("allOf", []taskconfig.Rule{taskconfig.PathFilter("中文"), taskconfig.ExtensionFilter("txt")})},
		{filterGroup("anyOf", nil)}, {filterGroup("allOf", nil)},
		{{Kind: "not", Rule: filterRulePtr(filterGroup("anyOf", nil))}},
		editorRules(t, filterEditor{Mode: filterCustom, IncludePaths: "中文", IncludeExtensions: "txt", ExcludeExtensions: "tmp"}),
	}
	for _, rs := range cases {
		before, _ := json.Marshal(rs)
		e := projectNativeFilter(rs)
		if e.Mode == filterReadOnly {
			t.Fatalf("projection rejected %s", before)
		}
		after, err := e.rules()
		if err != nil {
			t.Fatal(err)
		}
		a, _ := filefilter.Compile(rs)
		b, _ := filefilter.Compile(after)
		for _, p := range []string{"中文/a.txt", "中文/a.tmp", "a.txt", "a.*.jpg", "a.jpg", "日本語/a.txt"} {
			x, ex := a.Match(p)
			y, ey := b.Match(p)
			if ex != nil || ey != nil || x != y {
				t.Fatalf("%s %s %v %v", before, p, x, y)
			}
		}
		still, _ := json.Marshal(rs)
		if string(before) != string(still) {
			t.Fatal("projection changed original")
		}
	}
}
func filterRulePtr(r taskconfig.Rule) *taskconfig.Rule { return &r }
func TestNativeFilterUnknownDeepAndRegexStayReadOnly(t *testing.T) {
	var unknown taskconfig.Rule
	if err := json.Unmarshal([]byte(`{"kind":"extension","Extension":"txt","future":{"n":9007199254740993}}`), &unknown); err != nil {
		t.Fatal(err)
	}
	for _, r := range []taskconfig.Rule{unknown, {Kind: "regex", Pattern: filterText("a")}, filterGroup("allOf", []taskconfig.Rule{filterGroup("anyOf", []taskconfig.Rule{filterGroup("allOf", []taskconfig.Rule{taskconfig.PathFilter("a")})})})} {
		before, _ := json.Marshal(r)
		if projectNativeFilter([]taskconfig.Rule{r}).Mode != filterReadOnly {
			t.Fatal("unsafe projection")
		}
		after, _ := json.Marshal(r)
		if string(before) != string(after) {
			t.Fatal("raw changed")
		}
	}
	clone := unknown.Clone()
	*clone.Extension = "bin"
	if *unknown.Extension != "txt" {
		t.Fatal("shared leaf")
	}
	wire, _ := json.Marshal(clone)
	if !json.Valid(wire) || !strings.Contains(string(wire), `9007199254740993`) {
		t.Fatal("unknown member lost")
	}
}
func TestNativeGroupCopyAndSaveRestart(t *testing.T) {
	data := t.TempDir()
	l, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	l, id, err := l.save("", "Saved", manualFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	before := l.tasks[0].Destinations[0].Filters
	a := &application{tasks: l}
	a.selectManual(id)
	if !sameFilterRules(before, a.manual.targets[0].Filters) || a.manual.dirty {
		t.Fatal("opening editor rewrote rules")
	}
	a.setFilterMode(filterGrouped)
	a.manual.filterEditor.Groups[0].Kind = 1
	a.manual.filterEditor.Groups[0].Input = "txt"
	a.addFilterRule(0)
	original := a.manual.targetID
	a.copyManualTarget(original)
	a.manual.target = t.TempDir()
	a.manual.edited()
	*a.manual.targets[1].Filters[0].Rules = []taskconfig.Rule{taskconfig.ExtensionFilter("bin")}
	if *(*a.manual.targets[0].Filters[0].Rules)[0].Extension != "txt" {
		t.Fatal("copy changed original group")
	}
	// Save failure must retain the same draft and ID rather than rebuilding it.
	draft := a.manual.draftRequest()
	l, _, err = l.saveMultiple(id, "Saved", draft)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadSavedTasks(data)
	if err != nil {
		t.Fatal(err)
	}
	if !sameFilterRules(reloaded.tasks[0].Destinations[0].Filters, draft.Destinations[0].Filters) {
		t.Fatal("restart changed rules")
	}
	b, err := os.ReadFile(filepath.Join(data, savedTaskFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, savedTaskFile), append(b, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = l.saveMultiple(id, "Saved", draft); err == nil {
		t.Fatal("invalid save accepted")
	}
	if !a.manual.dirty || a.manual.targetID == original {
		t.Fatal("failed save lost draft")
	}
}
