package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
	"time"
)

func TestTaskHealthUsesCurrentDestinations(t *testing.T) {
	task := taskconfig.Task{ID: taskconfig.NewID(), Destinations: []taskconfig.Destination{{ID: taskconfig.NewID()}, {ID: taskconfig.NewID()}}}
	first, second := task.Destinations[0].ID, task.Destinations[1].ID
	results := syncengine.MultipleResult{first: {State: syncengine.DestinationSucceeded}}
	a := &application{locale: "zh-Hans", results: map[taskconfig.ID]savedTaskResult{
		task.ID: {destinations: results, message: messagef("已停止")},
	}}
	assertHealth := func(want string, color ui.Color) {
		t.Helper()
		if got, ink := a.taskHealth(task); got != a.tr(want) || ink != color {
			t.Fatalf("health = %q %v; want %q %v", got, ink, want, color)
		}
	}
	assertHealth("部分目标已完成", porcelain.Text)
	task.Kind = taskconfig.Moving
	a.manual = manualSession{id: task.ID, phase: manualDraft, message: messagef("任务已保存，可以运行。")}
	results[second] = syncengine.DestinationResult{State: syncengine.DestinationSucceeded, Progress: syncengine.TransferProgress{Retained: 1}}
	assertHealth("完成 · 保留 1 个源文件", porcelain.Text)
	result := a.results[task.ID]
	result.outcome, result.message = "failed", messagef("关闭句柄失败")
	a.results[task.ID] = result
	assertHealth("关闭句柄失败", porcelain.Danger)
	result.outcome, result.message = "stopped", messagef("已停止")
	a.results[task.ID] = result
	results[second] = syncengine.DestinationResult{State: syncengine.DestinationCanceled}
	assertHealth("已停止", porcelain.Warning)
	result.outcome = "succeeded"
	a.results[task.ID] = result
	results[first] = syncengine.DestinationResult{State: syncengine.DestinationNoMatches}
	results[second] = syncengine.DestinationResult{State: syncengine.DestinationNoMatches}
	assertHealth(string(syncengine.DestinationNoMatches), porcelain.Text)
	task.Destinations = nil
	assertHealth("尚未添加目标", porcelain.TextMuted)
}

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
	r := syncengine.DestinationResult{State: syncengine.DestinationSucceeded, Ended: now.Add(-49 * time.Hour), Progress: syncengine.TransferProgress{Copied: 120, Moved: 120}}
	text := a.destinationStatus(r, true, now)
	if text != "2天前同步 · 120个文件" {
		t.Fatal(text)
	}
	if got := a.destinationStatus(syncengine.DestinationResult{State: syncengine.DestinationSucceeded, Ended: now, Progress: syncengine.TransferProgress{Removed: 24}}, true, now); got != "刚刚同步 · 移除24个文件" {
		t.Fatalf("removed-only status = %q", got)
	}
	if got := a.destinationStatus(syncengine.DestinationResult{State: syncengine.DestinationSucceeded, Ended: now, Progress: syncengine.TransferProgress{Copied: 3, Removed: 24}}, true, now); got != "刚刚同步 · 3个文件 · 移除24个" {
		t.Fatalf("copied+removed status = %q", got)
	}
	if a.destinationStatus(r, false, now) != "从未运行" {
		t.Fatal("missing record invented success")
	}
	r.State = syncengine.DestinationFailed
	r.Error = "目标文件夹: directory identity cannot be verified: The parameter is incorrect."
	if got := a.destinationStatus(r, true, now); got != "失败 · 目录或文件系统不受支持" {
		t.Fatalf("expected generalized error summary, got %q", got)
	}
	r.Error = "此目标不支持回收确认；多余文件未移除。若需要，请明确选择永久删除后重新运行。"
	if got := a.destinationStatus(r, true, now); got != "失败 · 不支持回收站" {
		t.Fatalf("expected recycle-unsupported error summary, got %q", got)
	}
	r.Error = "unknown raw failure detail"
	if got := a.destinationStatus(r, true, now); got != "失败 · 查看日志" {
		t.Fatalf("expected generic error summary, got %q", got)
	}
	r.State = syncengine.DestinationCanceled
	if text := a.destinationStatus(r, true, now); text != a.tr("已停止") {
		t.Fatalf("stop status = %q, want %q", text, a.tr("已停止"))
	}
	r.State = syncengine.DestinationIncomplete
	r.Progress = syncengine.TransferProgress{Deferred: 2}
	if text := a.destinationStatus(r, true, now); text != "部分完成 · 延迟 2 个" {
		t.Fatalf("incomplete status = %q", text)
	}
	if color := destinationHealthColor(r, true); color != porcelain.Warning {
		t.Fatalf("incomplete color = %v, want Warning", color)
	}
	if got := a.destinationStatus(syncengine.DestinationResult{State: syncengine.DestinationSucceeded, Ended: now, Progress: syncengine.TransferProgress{Removed: 2, RemovedDirs: 3}}, true, now); got != "刚刚同步 · 移除2个文件 · 清理3个空目录" {
		t.Fatalf("removed+dirs status = %q", got)
	}
}

func TestDestinationCardStatusDoesNotOverflowNarrowWidth(t *testing.T) {
	taskID := taskconfig.NewID()
	destID := taskconfig.NewID()
	task := taskconfig.Task{
		ID:         taskID,
		Name:       "Eagle",
		Kind:       taskconfig.Sync,
		SourcePath: `G:\EagleRes\`,
		Destinations: []taskconfig.Destination{{
			ID:         destID,
			Name:       "NAS-Eagle_backup_with_very_long_destination_name",
			Strategy:   taskconfig.Mirror,
			DeleteMode: taskconfig.Recycle,
			Target:     taskconfig.Target{Kind: "local", Path: `Z:\Eagle_backup\very\deep\folder\path`},
			Filters:    []taskconfig.Rule{{Kind: "all"}},
		}},
	}
	a := &application{
		locale: "en",
		results: map[taskconfig.ID]savedTaskResult{
			taskID: {
				outcome: "failed",
				destinations: syncengine.MultipleResult{
					destID: {
						ID:    destID,
						Name:  "NAS-Eagle_backup",
						State: syncengine.DestinationFailed,
						Error: "目标文件夹: directory identity cannot be verified: The parameter is incorrect.",
					},
				},
			},
		},
	}
	const viewWidth = 640
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(&porcelain)
		ui.Column(c).Fill().Children(func() {
			a.selectedTaskDetailView(c, task, viewWidth)
		})
	}, viewWidth, 480)
	summary := a.destinationStatus(a.results[taskID].destinations[destID], true, time.Now())
	info, ok := tt.AccessByRole(ui.RoleLink, summary)
	if !ok {
		t.Fatalf("missing destination status link %q in texts %v", summary, tt.Texts())
	}
	if info.Bounds.X < 0 || info.Bounds.X+info.Bounds.W > float32(viewWidth)-10 {
		t.Fatalf("status link overflowed card bounds: bounds=%+v viewWidth=%d", info.Bounds, viewWidth)
	}
}

func TestClassifySyncErrorCatalogCoverage(t *testing.T) {
	cases := []struct {
		raw     string
		wantKey string
	}{
		{"目标文件夹: directory identity cannot be verified: The parameter is incorrect.", "目录或文件系统不受支持"},
		{"源与同步目标不可重叠: 目标目录相同", "源与目标目录重叠"},
		{"open directory: The system cannot find the path specified.", "目录不可访问"},
		{"open directory: Access is denied.", "权限不足"},
		{"Rust regex compatibility not implemented", "过滤规则未就绪"},
		{"源没有普通文件，Mirror禁止删除", "镜像安全检查未通过"},
		{"some unexpected low-level failure", "查看日志"},
	}
	for _, tc := range cases {
		summaryKey, hintKey := classifySyncError(tc.raw)
		if summaryKey != tc.wantKey {
			t.Fatalf("classifySyncError(%q) = %q, want %q", tc.raw, summaryKey, tc.wantKey)
		}
		for _, choice := range languageChoices {
			if got := messages.Format(choice.tag, summaryKey, nil); got == "" || got == summaryKey && choice.tag == "en" {
				t.Fatalf("missing %s translation for summary %q: %q", choice.tag, summaryKey, got)
			}
			if got := messages.Format(choice.tag, hintKey, nil); got == "" || got == hintKey && choice.tag == "en" {
				t.Fatalf("missing %s translation for hint %q: %q", choice.tag, hintKey, got)
			}
		}
	}
}

func TestHistoryViewShowsEnrichedMetadataAndLocalizedReason(t *testing.T) {
	taskID, destID, runID := taskconfig.NewID(), taskconfig.NewID(), taskconfig.NewID()
	start := time.Date(2026, 11, 22, 17, 47, 25, 0, time.UTC)
	end := start.Add(25 * time.Millisecond)
	a := &application{
		locale:          "en",
		historySelected: runID,
		historyView: appcore.RunHistoryData{
			Recent: []appcore.RunRecord{{
				ID:         runID,
				TaskID:     taskID,
				Name:       "Eagle",
				Kind:       taskconfig.Sync,
				Origin:     appcore.OriginManual,
				SourcePath: `G:\EagleRes\`,
				Started:    start,
				Ended:      end,
				Outcome:    appcore.OutcomeFailed,
				Error:      "NAS-Eagle_backup: 目标文件夹: directory identity cannot be verified: The parameter is incorrect.",
				Goals: []appcore.RunGoal{{
					ID:         destID,
					Name:       "NAS-Eagle_backup",
					TargetPath: `Z:\Eagle_backup`,
					Stage:      "检查目标目录",
					State:      syncengine.DestinationFailed,
					Error:      "目标文件夹: directory identity cannot be verified: The parameter is incorrect.",
				}},
			}},
		},
	}
	tt := ui.NewTester(a.historyViewContent, 760, 620)
	for _, want := range []string{
		`G:\EagleRes\`,
		`Z:\Eagle_backup`,
		"Checking destination folder",
		"25ms",
		"Unsupported folder or filesystem",
		"directory identity cannot be verified: The parameter is incorrect.",
	} {
		if !tt.HasText(want) {
			t.Fatalf("historyViewContent missing %q in %v", want, tt.Texts())
		}
	}
}

func TestEditorInlineOverlapHintsAndSaveDisable(t *testing.T) {
	otherID, editID, destID, siblingID := taskconfig.NewID(), taskconfig.NewID(), taskconfig.NewID(), taskconfig.NewID()
	a := &application{
		locale: "zh-Hans",
		tasks: &appcore.SavedTaskLibrary{
			Tasks: []taskconfig.Task{
				{
					ID:         otherID,
					Name:       "Downloads",
					Kind:       taskconfig.Moving,
					SourcePath: `D:\Downloads`,
					Destinations: []taskconfig.Destination{{
						ID:     taskconfig.NewID(),
						Name:   "Archive",
						Target: taskconfig.Target{Kind: "local", Path: `Z:\Archive`},
					}},
				},
				{
					ID:         editID,
					Name:       "Eagle",
					Kind:       taskconfig.Sync,
					SourcePath: `G:\EagleRes`,
					Destinations: []taskconfig.Destination{{
						ID:     destID,
						Name:   "Backup",
						Target: taskconfig.Target{Kind: "local", Path: `Z:\Eagle_backup`},
					}},
				},
			},
		},
		cards: taskCardLayout{editor: true, destinations: false},
		manual: manualSession{
			id:     editID,
			name:   "Eagle",
			kind:   taskconfig.Sync,
			source: `D:\Downloads\Sub`,
			targets: []syncengine.ManualDestination{
				{ID: destID, Name: "Backup", Path: `Z:\Eagle_backup`},
				{ID: siblingID, Name: "Sibling", Path: `Z:\Sibling`},
			},
			targetID:   destID,
			targetName: "Backup",
			target:     `Z:\Eagle_backup`,
		},
	}

	taskModal := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(&porcelain)
		ui.Column(c).Fill().Children(func() {
			a.taskEditorView(c, 520)
		})
	}, 600, 620)
	if !taskModal.HasText("Downloads") {
		t.Fatalf("missing source overlap hint naming Downloads: %v", taskModal.Texts())
	}
	if saveBtn, ok := taskModal.Access(a.tr("保存任务")); !ok || !saveBtn.Disabled {
		t.Fatalf("expected Save Task button to be disabled on source overlap: ok=%t info=%+v", ok, saveBtn)
	}

	a.manual.source = `G:\EagleRes`
	a.cards.destinations = true
	a.manual.target = `Z:\Archive\Nested`
	destWorkspace := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(&porcelain)
		ui.Column(c).Fill().Children(func() {
			a.destinationWorkspaceView(c)
		})
	}, 760, 620)
	if !destWorkspace.HasText("Downloads") {
		t.Fatalf("missing destination overlap hint naming Downloads: %v", destWorkspace.Texts())
	}
	if saveBtn, ok := destWorkspace.Access(a.tr("保存")); !ok || !saveBtn.Disabled {
		t.Fatalf("expected Save button to be disabled on destination overlap: ok=%t info=%+v", ok, saveBtn)
	}

	a.manual.target = `Z:\Sibling`
	if msg, ok := a.destinationOverlapMessage(destID, a.manual.target); !ok || msg.key == "" {
		t.Fatal("expected sibling destination overlap message")
	}
	if a.acceptDestination(destID) {
		t.Fatal("acceptDestination should reject overlapping path")
	}
}

func TestDiagnosticViewRetainsRootErrorAcrossLanguages(t *testing.T) {
	task, destination := taskconfig.NewID(), taskconfig.NewID()
	a := &application{historyView: appcore.RunHistoryData{Recent: []appcore.RunRecord{{ID: taskconfig.NewID(), TaskID: task, Name: "Original task", Error: "unabridged root failure", Goals: []appcore.RunGoal{{ID: destination, Name: "Original destination", State: syncengine.DestinationFailed}}}}}}
	a.openDestinationLog(task, destination)
	for _, choice := range languageChoices {
		a.locale = choice.tag
		tt := ui.NewTester(a.historyViewContent, 960, 620)
		if !tt.HasText("unabridged root failure") || !tt.HasText(a.tr("近期运行记录")) || !tt.HasText(a.tr("已定位")) {
			t.Fatal("diagnostic content lost", choice.tag, tt.Texts())
		}
	}
}
func TestDestinationLogsBoundedAndLatestFallback(t *testing.T) {
	task, destination := taskconfig.NewID(), taskconfig.NewID()
	a := &application{historyView: appcore.RunHistoryData{Latest: map[taskconfig.ID]appcore.RunRecord{}}}
	latest := appcore.RunRecord{ID: taskconfig.NewID(), TaskID: task, Goals: []appcore.RunGoal{{ID: destination, Error: "preserve"}}}
	a.historyView.Latest[task] = latest
	a.openDestinationLog(task, destination)
	if entries := a.historyEntries(); len(entries) != 1 || entries[0].Record.Goals[0].Error != "preserve" {
		t.Fatal(entries)
	}
	for i := 0; i < 30; i++ {
		a.historyView.Recent = append(a.historyView.Recent, appcore.RunRecord{ID: taskconfig.NewID(), TaskID: task, Goals: []appcore.RunGoal{{ID: destination}}})
	}
	a.openDestinationLog(task, destination)
	if entries := a.historyEntries(); len(entries) != 31 || entries[0].Record.ID != a.historyView.Recent[29].ID || a.historySelected != a.historyView.Recent[29].ID {
		t.Fatal("unordered or unlocated history entries")
	}
	a.openDestinationLog(taskconfig.NewID(), taskconfig.NewID())
	if len(a.historyEntries()) != 0 {
		t.Fatal("mixed task logs")
	}
}

func TestDestinationRecycleProbeAndSwitchToPermanent(t *testing.T) {
	orig := probeRecycleSupport
	t.Cleanup(func() { probeRecycleSupport = orig })
	probeCalls := 0
	probeRecycleSupport = func(dirPath string) (bool, bool) {
		probeCalls++
		return dirPath == `Z:\share`, true
	}

	destID := taskconfig.NewID()
	a := &application{
		locale: "zh-Hans",
		tasks:  &appcore.SavedTaskLibrary{},
		manual: manualSession{
			kind:       taskconfig.Sync,
			targetID:   destID,
			targetName: "NAS",
			target:     `Z:\share`,
			targets: []syncengine.ManualDestination{{
				ID:         destID,
				Name:       "NAS",
				Path:       `Z:\share`,
				Strategy:   taskconfig.Mirror,
				DeleteMode: taskconfig.Recycle,
				Filters:    syncengine.AllFileRules(),
			}},
		},
	}

	a.triggerRecycleProbe(destID, `Z:\share`)
	if probeCalls != 1 {
		t.Fatalf("probeCalls = %d, want 1", probeCalls)
	}
	if !a.destinationRecycleUnsupported(a.manual.targets[0]) {
		t.Fatal("expected Z:\\share to be flagged as recycle-unsupported")
	}
	if !a.destinationRecycleUnsupported(syncengine.ManualDestination{ID: taskconfig.NewID(), Path: `\\nas\share`}) {
		t.Fatal("expected UNC path to be flagged as recycle-unsupported")
	}

	tt := ui.NewTester(func(c *ui.Context) { a.destinationModeView(c, false) }, 640, 320)
	if !tt.HasText(a.tr("此目标不支持回收站，Mirror 清理多余文件将失败。")) {
		t.Fatalf("missing recycle warning in mode view: %v", tt.Texts())
	}
	tt.Click(a.tr("切换为永久删除"))
	if a.manual.targets[0].DeleteMode != taskconfig.Permanent {
		t.Fatalf("expected DeleteMode to switch to Permanent, got %q", a.manual.targets[0].DeleteMode)
	}
	if !a.manual.dirty {
		t.Fatal("expected manual session to be marked dirty after switching delete mode")
	}
}
