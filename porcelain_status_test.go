package main

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"syncmaid/internal/taskconfig"
	"testing"
	"time"
)

func TestRuleSummaryUsesNaturalLabels(t *testing.T) {
	a := &application{locale: "zh-Hans"}
	pattern := "*.txt"
	rules := []taskconfig.Rule{{Kind: "wildcard", Pattern: &pattern}}
	if got := a.ruleSummary(rules); got != "通配符 *.txt" {
		t.Fatal(got)
	}
}

func TestRelativeStatusAndLogRetainRealCountsAndErrors(t *testing.T) {
	a := &application{locale: "zh-Hans"}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	r := destinationResult{State: destinationSucceeded, Ended: now.Add(-49 * time.Hour), Progress: transferProgress{Copied: 120, Moved: 120}}
	text := a.destinationStatus(r, true, now)
	if text != "2天前同步 · 120个文件" {
		t.Fatal(text)
	}
	if a.destinationStatus(r, false, now) != "从未运行" {
		t.Fatal("missing record invented success")
	}
	r.State = destinationFailed
	r.Error = "完整的原始错误"
	if !strings.Contains(a.destinationStatus(r, true, now), r.Error) {
		t.Fatal("error lost")
	}
	r.State = destinationCanceled
	if text := a.destinationStatus(r, true, now); !strings.HasPrefix(text, a.tr("已停止")) {
		t.Fatal("stop mislabeled as failure", text)
	}
}

func TestDiagnosticViewRetainsRootErrorAcrossLanguages(t *testing.T) {
	task, destination := taskconfig.NewID(), taskconfig.NewID()
	a := &application{logTask: task, logDestination: destination, historyView: runHistoryData{Recent: []runRecord{{ID: taskconfig.NewID(), TaskID: task, Name: "Original task", Error: "unabridged root failure", Goals: []runGoal{{ID: destination, Name: "Original destination", State: destinationFailed}}}}}}
	for _, choice := range languageChoices {
		a.locale = choice.tag
		tt := ui.NewTester(a.destinationLogView, 720, 420)
		if !tt.HasText("unabridged root failure") || !tt.HasText(a.tr("同步日志")) {
			t.Fatal("diagnostic content lost", choice.tag, tt.Texts())
		}
	}
}
func TestDestinationLogsBoundedAndLatestFallback(t *testing.T) {
	task, destination := taskconfig.NewID(), taskconfig.NewID()
	a := &application{logTask: task, logDestination: destination, historyView: runHistoryData{Latest: map[taskconfig.ID]runRecord{}}}
	latest := runRecord{ID: taskconfig.NewID(), TaskID: task, Goals: []runGoal{{ID: destination, Error: "preserve"}}}
	a.historyView.Latest[task] = latest
	if records := a.destinationLogRecords(); len(records) != 1 || records[0].Goals[0].Error != "preserve" {
		t.Fatal(records)
	}
	for i := 0; i < 30; i++ {
		a.historyView.Recent = append(a.historyView.Recent, runRecord{ID: taskconfig.NewID(), TaskID: task, Goals: []runGoal{{ID: destination}}})
	}
	if records := a.destinationLogRecords(); len(records) != 20 || records[0].ID != a.historyView.Recent[29].ID {
		t.Fatal("unbounded or unordered log")
	}
	a.logDestination = taskconfig.NewID()
	if len(a.destinationLogRecords()) != 0 {
		t.Fatal("mixed destination logs")
	}
}
