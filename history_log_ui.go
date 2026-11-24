package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/atomicfile"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type historyFilterKind uint8

const (
	historyFilterAll historyFilterKind = iota
	historyFilterAbnormal
	historyFilterAuto
)

const liveHistoryRunID taskconfig.ID = "__live__"

type historyEntry struct {
	IsLive      bool
	DeletedTask bool
	Record      appcore.RunRecord
}

func (a *application) toggleHistory() {
	a.settingsOpen = false
	a.topologyOpen = false
	a.historyOpen = !a.historyOpen
	if a.historyOpen {
		if r, ok := a.historyView.Latest[a.manual.id]; ok {
			a.historySelected = r.ID
		}
		a.checkDestinationLogFile()
	}
}

func (a *application) openHistoryScreen() {
	a.historyOpen = true
	a.settingsOpen = false
	a.topologyOpen = false
	a.checkDestinationLogFile()
}

func liveHistoryRecord(snap liveRunSnapshot) appcore.RunRecord {
	goals := make([]appcore.RunGoal, 0, len(snap.Destinations))
	for _, d := range snap.Destinations {
		goals = append(goals, appcore.RunGoal{
			ID:         d.ID,
			Name:       d.Name,
			TargetPath: d.TargetPath,
			Stage:      d.Result.Progress.Stage,
			State:      d.Result.State,
			Error:      d.Result.Error,
			Counts:     d.Result.Progress,
			Started:    d.Result.Started,
			Ended:      d.Result.Ended,
		})
	}
	return appcore.RunRecord{
		ID:      liveHistoryRunID,
		TaskID:  snap.TaskID,
		Name:    snap.TaskName,
		Kind:    snap.Kind,
		Origin:  snap.Origin,
		Started: snap.Started,
		Outcome: appcore.OutcomeRunning,
		Goals:   goals,
	}
}

func isAbnormalRunRecord(r appcore.RunRecord) bool {
	if r.Outcome == appcore.OutcomeFailed || r.Outcome == appcore.OutcomeStopped {
		return true
	}
	for _, g := range r.Goals {
		switch g.State {
		case syncengine.DestinationFailed, syncengine.DestinationIncomplete, syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
			return true
		}
	}
	return false
}

func isAutoRunRecord(r appcore.RunRecord) bool {
	return r.Origin == appcore.OriginWatch || r.Origin == appcore.OriginScheduled
}

func (a *application) historyEntries() []historyEntry {
	known := map[taskconfig.ID]bool{}
	if a.tasks != nil {
		for _, t := range a.tasks.Tasks {
			known[t.ID] = true
		}
	}
	matchesFilter := func(isLive bool, r appcore.RunRecord) bool {
		if a.historyTaskFilter != "" && r.TaskID != a.historyTaskFilter {
			return false
		}
		switch a.historyFilter {
		case historyFilterAbnormal:
			if isLive {
				return false
			}
			return isAbnormalRunRecord(r)
		case historyFilterAuto:
			return isAutoRunRecord(r)
		default:
			return true
		}
	}
	deletedFor := func(id taskconfig.ID) bool {
		return a.tasks != nil && id != "" && !known[id]
	}

	var out []historyEntry
	if a.liveSnapshot.Active {
		lr := liveHistoryRecord(a.liveSnapshot)
		if matchesFilter(true, lr) {
			out = append(out, historyEntry{IsLive: true, DeletedTask: deletedFor(lr.TaskID), Record: lr})
		}
	}

	seen := map[taskconfig.ID]bool{}
	for i := len(a.historyView.Recent) - 1; i >= 0; i-- {
		r := a.historyView.Recent[i]
		if r.ID != "" {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
		}
		if matchesFilter(false, r) {
			out = append(out, historyEntry{DeletedTask: deletedFor(r.TaskID), Record: r})
		}
	}
	for _, r := range a.historyView.Latest {
		if r.ID != "" && seen[r.ID] {
			continue
		}
		if matchesFilter(false, r) {
			out = append(out, historyEntry{DeletedTask: deletedFor(r.TaskID), Record: r})
		}
	}
	return out
}

func (a *application) selectedHistoryEntry(entries []historyEntry) (historyEntry, bool) {
	if len(entries) == 0 {
		return historyEntry{}, false
	}
	for _, e := range entries {
		if e.Record.ID == a.historySelected {
			return e, true
		}
	}
	return entries[0], true
}

func (a *application) historyTaskFilterName() string {
	if a.historyTaskFilter == "" {
		return ""
	}
	if a.tasks != nil {
		if t, err := a.tasks.Find(a.historyTaskFilter); err == nil {
			return t.Name
		}
	}
	if a.liveSnapshot.Active && a.liveSnapshot.TaskID == a.historyTaskFilter {
		return a.liveSnapshot.TaskName
	}
	for i := len(a.historyView.Recent) - 1; i >= 0; i-- {
		if a.historyView.Recent[i].TaskID == a.historyTaskFilter {
			return a.historyView.Recent[i].Name
		}
	}
	if latest, ok := a.historyView.Latest[a.historyTaskFilter]; ok {
		return latest.Name
	}
	return string(a.historyTaskFilter)
}

func (a *application) runOutcomeColor(isLive bool, r appcore.RunRecord) ui.Color {
	if isLive {
		return ribbonBlue
	}
	switch r.Outcome {
	case appcore.OutcomeSucceeded:
		return porcelain.Success
	case appcore.OutcomeFailed:
		return porcelain.Danger
	default:
		return porcelain.Warning
	}
}

func (a *application) runOutcomeLabel(isLive bool, r appcore.RunRecord) string {
	if isLive {
		if a.liveSnapshot.Fraction >= 0 {
			return fmt.Sprintf("%s %d%%", a.tr(a.liveSnapshot.Stage), int(a.liveSnapshot.Fraction*100+0.5))
		}
		return a.tr(a.liveSnapshot.Stage)
	}
	return a.tr(appcore.RunOutcome(r))
}

func (a *application) runSummaryLine(isLive bool, r appcore.RunRecord) string {
	if isLive {
		return a.tr("目标 %d/%d · %s", a.liveSnapshot.CurrentDestIndex+1, max(a.liveSnapshot.TotalDests, 1), a.tr(a.liveSnapshot.Stage))
	}
	var copied, moved, removed, failed, deferred int
	for _, g := range r.Goals {
		copied += g.Counts.Copied
		moved += g.Counts.Moved
		removed += g.Counts.Removed
		failed += g.Counts.Failed
		deferred += g.Counts.Deferred
		if g.State == syncengine.DestinationFailed && g.Counts.Failed == 0 {
			failed++
		}
	}
	var parts []string
	if copied > 0 {
		parts = append(parts, a.tr("已发布 %d", copied))
	}
	if moved > 0 {
		parts = append(parts, a.tr("移动 %d", moved))
	}
	if removed > 0 {
		parts = append(parts, a.tr("移除 %d", removed))
	}
	if deferred > 0 {
		parts = append(parts, a.tr("延迟 %d", deferred))
	}
	if failed > 0 {
		parts = append(parts, a.tr("失败 %d", failed))
	}
	if len(parts) == 0 {
		return a.tr("%d 个目标", len(r.Goals))
	}
	return strings.Join(parts, " · ")
}

func (a *application) historyViewContent(c *ui.Context) {
	entries := a.historyEntries()
	selected, hasSelected := a.selectedHistoryEntry(entries)
	if hasSelected && a.historySelected != selected.Record.ID {
		a.historySelected = selected.Record.ID
	}

	ui.Column(c).Grow(1).FillHeight().Gap(0).Children(func() {
		// Top audit toolbar
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Padding(12, 16).Background(porcelain.Surface).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			ui.Text(c, a.tr("近期运行记录")).FontSize(18).FontWeight(600)
			ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
				porcelainSegment(c, a.tr("全部"), a.historyFilter == historyFilterAll).OnClick(func() {
					a.historyFilter = historyFilterAll
				})
				porcelainSegment(c, a.tr("仅异常"), a.historyFilter == historyFilterAbnormal).OnClick(func() {
					a.historyFilter = historyFilterAbnormal
				})
				porcelainSegment(c, a.tr("自动触发"), a.historyFilter == historyFilterAuto).OnClick(func() {
					a.historyFilter = historyFilterAuto
				})
			})
			if name := a.historyTaskFilterName(); name != "" {
				porcelainCompactButton(c, a.tr("任务: %s", name)+" ×").Background(ribbonBlueSoft).Border(1, ribbonBlue).OnClick(func() {
					a.historyTaskFilter = ""
					a.historyHighlightDest = ""
				})
			}
			ui.Box(c).Grow(1)
			diagBtn := porcelainButton(c, a.tr("诊断详情"))
			if a.historyDetails {
				diagBtn = diagBtn.Background(ribbonBlueSoft).Border(1, ribbonBlue).TextColor(ribbonBlue)
			}
			diagBtn.OnClick(func() { a.historyDetails = !a.historyDetails })
			porcelainButton(c, a.tr("刷新")).Disabled(a.logReading).OnClick(a.refreshDestinationLog)
			porcelainPrimaryButton(c, a.tr("打开日志文件")).Disabled(!a.logFileAvailable || a.logReading).OnClick(func() {
				a.openDestinationLogFile(mygo.Shell.OpenPath)
			})
		})

		if a.historyError != nil || a.logOpenError != nil {
			ui.Column(c).FillWidth().Gap(4).Padding(8, 16).Background(ribbonWarnSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
				if a.historyError != nil {
					ui.Text(c, a.tr("记录不可用或未保存：")+appcore.HistoryText(a.historyError.Error(), appcore.HistoryErrorBytes)).FontSize(13).TextColor(porcelain.Danger)
					ui.Text(c, a.tr("已发生的文件执行结果不变。请保留并修复记录文件后重新载入。")).FontSize(12).TextColor(porcelain.TextMuted)
				}
				if a.logOpenError != nil {
					ui.Text(c, a.tr("无法打开日志文件：%s", a.logOpenError.Error())).FontSize(13).TextColor(porcelain.Danger)
				}
			})
		}

		if !hasSelected {
			ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(8).Padding(24).Children(func() {
				ui.Text(c, a.tr("暂无可用运行记录。")).FontSize(14).TextColor(porcelain.TextMuted)
				if a.historyTaskFilter != "" || a.historyFilter != historyFilterAll {
					porcelainButton(c, a.tr("清除筛选")).OnClick(func() {
						a.historyFilter = historyFilterAll
						a.historyTaskFilter = ""
						a.historyHighlightDest = ""
					})
				}
			})
			return
		}

		// Master-Detail Timeline Split
		ui.Row(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Gap(0).Children(func() {
			// Left: Timeline List
			ui.Column(c).Width(248).FillHeight().Background(ribbonSidebarBg).Gap(0).Children(func() {
				porcelainScroll(c, &a.scrolls.historyList, func() {
					ui.Column(c).FillWidth().Gap(0).Children(func() {
						for _, entry := range entries {
							r := entry.Record
							id := r.ID
							isSelected := selected.Record.ID == id
							outcomeColor := a.runOutcomeColor(entry.IsLive, r)
							label := a.tr("查看 ") + r.Name + " · " + r.Started.Local().Format("01-02 15:04:05") + " · " + a.runOutcomeLabel(entry.IsLive, r)
							btn := ui.ButtonBase(c).Label(label).Justify(ui.Start).FillWidth().Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border)
							if isSelected {
								btn = paintRibbonStripe(btn.Background(porcelain.Surface))
							} else if btn.Hovered() {
								btn = btn.Background(porcelain.SurfaceHover)
							}
							btn.Children(func() {
								ui.Column(c).FillWidth().Gap(4).Children(func() {
									ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
										ui.Text(c, r.Name).FontSize(13).FontWeight(600).Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
										if entry.DeletedTask {
											ui.Text(c, a.tr("已删除")).FontSize(11).TextColor(porcelain.TextMuted)
										}
										ui.Text(c, a.runOutcomeLabel(entry.IsLive, r)).FontSize(12).FontWeight(600).TextColor(outcomeColor).Shrink(0)
									})
									ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
										ui.Text(c, r.Started.Local().Format("01-02 15:04:05")).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted).Grow(1)
										dur := formatRunDuration(r.Started, r.Ended)
										if entry.IsLive {
											dur = formatElapsedMMSS(a.liveSnapshot.Elapsed)
										}
										originText := a.tr(string(r.Origin))
										if dur != "" {
											originText += " · " + dur
										}
										ui.Text(c, originText).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted).Shrink(0)
									})
									ui.Text(c, a.runSummaryLine(entry.IsLive, r)).FontSize(12).TextColor(porcelain.TextMuted).SingleLine().Ellipsis("…")
								})
							}).OnClick(func() {
								a.historySelected = id
								a.historyHighlightDest = ""
							})
						}
					})
				}).Grow(1).FillWidth()
			})
			ui.Box(c).Width(1).FillHeight().Background(porcelain.Border)

			// Right: Selected Run Detail
			r := selected.Record
			porcelainScroll(c, &a.scrolls.historyDetail, func() {
				ui.Column(c).FillWidth().Padding(16).Gap(10).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(8).Children(func() {
						ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).FillWidth().Wrap().AlignItems(ui.Baseline).Gap(8).Children(func() {
								ui.Text(c, r.Name+a.tr(" · ")+a.runOutcomeLabel(selected.IsLive, r)).FontSize(16).FontWeight(600).TextColor(a.runOutcomeColor(selected.IsLive, r))
								if selected.DeletedTask {
									ui.Text(c, a.tr("已删除")).FontSize(12).TextColor(porcelain.TextMuted)
								}
							})
							if r.SourcePath != "" {
								ui.Text(c, a.tr("源目录: ")+r.SourcePath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
							}
							if a.historyDetails {
								ui.Text(c, fmt.Sprintf(a.tr("任务 %s · 运行 %s · %s / %s"), r.TaskID, r.ID, r.Kind, a.tr(string(r.Origin)))).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
							}
							ui.Text(c, a.tr("开始: ")+r.Started.Local().Format("2006-01-02 15:04:05 MST")).FontSize(13)
							if !r.Ended.IsZero() {
								endedText := a.tr("结束: ") + r.Ended.Local().Format("2006-01-02 15:04:05 MST")
								if dur := formatRunDuration(r.Started, r.Ended); dur != "" {
									endedText += " (" + dur + ")"
								}
								ui.Text(c, endedText).FontSize(13)
							} else if !selected.IsLive {
								ui.Text(c, a.tr("未记录结束；可能已有文件操作，请核对，不能视作成功或未执行。")).FontSize(13).TextColor(porcelain.Warning)
							}
						})
						if a.historyTaskFilter == "" && r.TaskID != "" {
							taskID := r.TaskID
							porcelainCompactButton(c, a.tr("仅看此任务")).OnClick(func() {
								a.historyTaskFilter = taskID
								a.historyHighlightDest = ""
							})
						}
					})

					if selected.IsLive {
						a.historyLiveProgressPanel(c)
					}
					if r.Error != "" {
						a.renderLogErrorBlock(c, r.Error, porcelain.Danger)
					}

					for _, g := range r.Goals {
						highlighted := a.historyHighlightDest != "" && g.ID == a.historyHighlightDest
						cardBg := porcelain.Surface
						cardBorder := porcelain.Border
						if highlighted {
							cardBg = ribbonBlueSoft
							cardBorder = ribbonBlue
						}
						goalColor := porcelain.Text
						if g.Error != "" {
							goalColor = porcelain.Danger
						} else if g.State == syncengine.DestinationCanceled || g.State == syncengine.DestinationInterrupted || g.State == syncengine.DestinationIncomplete {
							goalColor = porcelain.Warning
						} else if g.State == syncengine.DestinationRunning {
							goalColor = ribbonBlue
						} else if g.State == syncengine.DestinationSucceeded {
							goalColor = porcelain.Success
						}

						ui.Column(c).FillWidth().Padding(12).Gap(4).Background(cardBg).Border(1, cardBorder).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
								ui.Text(c, g.Name+a.tr(" · ")+a.tr(string(g.State))).FontSize(14).FontWeight(600).TextColor(goalColor).Grow(1)
								if highlighted {
									ui.Text(c, a.tr("已定位")).FontSize(12).FontWeight(600).TextColor(ribbonBlue)
								}
							})
							if g.TargetPath != "" {
								ui.Text(c, a.tr("目标目录: ")+g.TargetPath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
							}
							if g.Stage != "" {
								ui.Text(c, a.tr("阶段: ")+a.tr(g.Stage)).FontSize(12).TextColor(porcelain.TextMuted)
							}
							if a.historyDetails {
								ui.Text(c, a.tr("目标ID: ")+string(g.ID)).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
								if !g.Started.IsZero() {
									ui.Text(c, a.tr("目标开始: ")+g.Started.Local().Format("01-02 15:04:05")).FontSize(12).TextColor(porcelain.TextMuted)
								}
								if !g.Ended.IsZero() {
									ui.Text(c, a.tr("目标结束: ")+g.Ended.Local().Format("01-02 15:04:05")).FontSize(12).TextColor(porcelain.TextMuted)
								}
							}
							if !selected.IsLive && r.Outcome != appcore.OutcomeRunning {
								if counts := a.runCounts(g, a.historyDetails); counts != "" {
									ui.Text(c, counts).FontSize(13)
								}
							} else if selected.IsLive && g.State == syncengine.DestinationRunning {
								ui.Text(c, a.destinationStatus(syncengine.DestinationResult{ID: g.ID, Name: g.Name, State: g.State, Progress: g.Counts}, true, time.Now())).Font("Consolas").FontSize(12).TextColor(ribbonBlue)
							}
							if g.Error != "" {
								a.renderLogErrorBlock(c, g.Error, porcelain.Danger)
							}
						})
					}
				})
			}).Grow(1).Shrink(1).MinHeight(0).FillHeight().Background(porcelain.Background)
		})
	})
}

func (a *application) historyLiveProgressPanel(c *ui.Context) {
	snap := a.liveSnapshot
	ui.Column(c).FillWidth().Padding(12).Gap(6).Background(ribbonBlueSoft).Border(1, ribbonBlue).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("实时执行状态")).FontSize(13).FontWeight(600).TextColor(ribbonBlue).Grow(1)
			ui.Text(c, a.tr("已用 %s", formatElapsedMMSS(snap.Elapsed))).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
		})
		ui.Progress(c, snap.Fraction).Height(6).FillWidth()
		n := len(snap.Events)
		start := max(0, n-6)
		for i := start; i < n; i++ {
			ev := snap.Events[i]
			evColor := porcelain.Text
			if ev.Level == liveEventWarn {
				evColor = porcelain.Warning
			} else if ev.Level == liveEventError {
				evColor = porcelain.Danger
			}
			ui.Text(c, ev.At.Local().Format("15:04:05")+"  "+a.render(ev.Message)).Font("Consolas").FontSize(12).TextColor(evColor)
		}
	})
}

func (a *application) runCounts(goal appcore.RunGoal, details bool) string {
	p := goal.Counts
	if details {
		return a.tr("源 %d · 匹配 %d · 已发布 %d · 移动 %d · 移除 %d · 保留/待核对 %d · 未变化 %d · 移动失败 %d · 源未匹配 %d · %d字节", p.SourceFiles, p.Total, p.Copied, p.Moved, p.Removed, p.Retained, p.Unchanged, p.Failed, p.Unmatched, p.Bytes)
	}
	var counts []string
	for _, item := range []struct {
		label string
		n     int
	}{
		{"已发布 %d", p.Copied}, {"移动 %d", p.Moved}, {"移除 %d", p.Removed},
		{"保留/待核对 %d", p.Retained}, {"未变化 %d", p.Unchanged}, {"移动失败 %d", p.Failed}, {"源未匹配 %d", p.Unmatched},
	} {
		if item.n != 0 {
			counts = append(counts, a.tr(item.label, item.n))
		}
	}
	return strings.Join(counts, " · ")
}

func taskIDs(tasks []taskconfig.Task) []taskconfig.ID {
	ids := make([]taskconfig.ID, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func historyResult(r appcore.RunRecord) savedTaskResult {
	message := messagef("%s · %s", r.Started.Local().Format("2006-01-02 15:04:05"), messagef(appcore.RunOutcome(r)))
	if r.Error != "" {
		message = messagef("%s · %s", message, r.Error)
	}
	results := syncengine.MultipleResult{}
	var progress syncengine.TransferProgress
	for _, g := range r.Goals {
		result := syncengine.DestinationResult{ID: g.ID, Name: g.Name, Started: g.Started, Ended: g.Ended, State: g.State, Progress: g.Counts, Error: g.Error}
		if r.Outcome == appcore.OutcomeRunning {
			result.State = syncengine.DestinationInterrupted
			result.Error = "上次未记录结束，计数不是最终结果，请核对文件"
		}
		results[g.ID] = result
		progress = result.Progress
		progress.Stage = string(result.State)
	}
	return savedTaskResult{outcome: r.Outcome, message: message, progress: progress, destinations: results, started: r.Started, ended: r.Ended}
}

func (a *application) restoreRunResults() {
	if a.results == nil {
		a.results = map[taskconfig.ID]savedTaskResult{}
	}
	if a.tasks == nil {
		return
	}
	known := map[taskconfig.ID]bool{}
	for _, t := range a.tasks.Tasks {
		known[t.ID] = true
	}
	for id, r := range a.historyView.Latest {
		if !known[id] {
			continue
		}
		if _, current := a.results[id]; !current {
			a.results[id] = historyResult(r)
		}
	}
}

// openDestinationLog navigates directly to the unified History audit screen (Plan A),
// filtering by task and highlighting the chosen destination without opening a second window.
func (a *application) openDestinationLog(task, destination taskconfig.ID) {
	a.historyOpen = true
	a.settingsOpen = false
	a.topologyOpen = false
	a.historyFilter = historyFilterAll
	a.historyTaskFilter = task
	a.historyHighlightDest = destination
	a.logOpenError = nil

	if a.liveSnapshot.Active && a.liveSnapshot.TaskID == task {
		a.historySelected = liveHistoryRunID
	} else {
		selected := taskconfig.ID("")
		for i := len(a.historyView.Recent) - 1; i >= 0; i-- {
			r := a.historyView.Recent[i]
			if r.TaskID != task {
				continue
			}
			for _, g := range r.Goals {
				if g.ID == destination {
					selected = r.ID
					break
				}
			}
			if selected != "" {
				break
			}
		}
		if selected == "" {
			if latest, ok := a.historyView.Latest[task]; ok {
				selected = latest.ID
			}
		}
		if selected != "" {
			a.historySelected = selected
		}
	}
	a.checkDestinationLogFile()
}

func (a *application) destinationLogPath() string {
	return filepath.Join(a.installation.Data, appcore.RunHistoryFile)
}

func inspectDestinationLogFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !atomicfile.IsRegularFile(info) {
		return errors.ErrUnsupported
	}
	return nil
}

func (a *application) checkDestinationLogFile() {
	a.logFileAvailable = false
	path := a.destinationLogPath()
	if a.window == nil {
		a.logFileAvailable = inspectDestinationLogFile(path) == nil
		return
	}
	go func() {
		available := inspectDestinationLogFile(path) == nil
		a.window.Update(func() {
			a.logFileAvailable = available
		})
	}()
}

func (a *application) openDestinationLogFile(open func(string) error) {
	if !a.logFileAvailable {
		return
	}
	path := a.destinationLogPath()
	a.logOpenError = inspectDestinationLogFile(path)
	if a.logOpenError != nil {
		a.logFileAvailable = false
		return
	}
	// The Shell expects a DOS/UNC spelling. File inspection above uses the
	// original native path; the display spelling is only for this handoff.
	a.logOpenError = open(winutil.PathLabel(path))
}

func (a *application) refreshDestinationLog() {
	if a.logReading {
		return
	}
	a.logReading = true
	data := a.installation.Data
	path := a.destinationLogPath()
	go func() {
		history := appcore.LoadRunHistory(data)
		available := inspectDestinationLogFile(path) == nil
		a.window.Update(func() {
			a.logReading = false
			a.historyError = history.Err
			if history.Err == nil {
				a.historyView = history.Data
			}
			a.logFileAvailable = available
		})
	}()
}

func formatRunDuration(started, ended time.Time) string {
	if started.IsZero() || ended.IsZero() || ended.Before(started) {
		return ""
	}
	return ended.Sub(started).Round(time.Millisecond).String()
}

func (a *application) renderLogErrorBlock(c *ui.Context, raw string, color ui.Color) {
	summaryKey, hintKey := classifySyncError(raw)
	ui.Text(c, a.tr("原因: ")+a.tr(summaryKey)).FontSize(13).FontWeight(600).TextColor(color)
	ui.Text(c, a.tr("说明: ")+a.tr(hintKey)).FontSize(12).TextColor(porcelain.TextMuted)
	ui.Text(c, a.tr("技术详情: ")+raw).Font("Consolas").FontSize(12).TextColor(color)
}
