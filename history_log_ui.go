package main

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/atomicfile"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
	"time"
)

func (a *application) toggleHistory() {
	a.settingsOpen = false
	a.topologyOpen = false
	a.historyOpen = !a.historyOpen
	if r, ok := a.historyView.Latest[a.manual.id]; ok {
		a.historySelected = r.ID
	}
}
func (a *application) selectedHistory() (appcore.RunRecord, bool) {
	for _, r := range a.historyView.Recent {
		if r.ID == a.historySelected {
			return r, true
		}
	}
	for _, r := range a.historyView.Latest {
		if r.ID == a.historySelected {
			return r, true
		}
	}
	if n := len(a.historyView.Recent); n > 0 {
		return a.historyView.Recent[n-1], true
	}
	return appcore.RunRecord{}, false
}
func (a *application) historyViewContent(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Gap(10).Padding(16).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("近期运行记录")).FontSize(18).FontWeight(600).Grow(1)
			porcelainButton(c, a.tr("诊断详情")).OnClick(func() { a.historyDetails = !a.historyDetails })
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
		if a.historyError != nil {
			ui.Text(c, a.tr("记录不可用或未保存：")+appcore.HistoryText(a.historyError.Error(), appcore.HistoryErrorBytes)).TextColor(porcelain.Danger)
			ui.Text(c, a.tr("已发生的文件执行结果不变。请保留并修复记录文件后重新载入。")).FontSize(13).TextColor(porcelain.TextMuted)
		}
		porcelainScroll(c, &a.scrolls.historyList, func() {
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				for i := len(a.historyView.Recent) - 1; i >= 0; i-- {
					r := a.historyView.Recent[i]
					id := r.ID
					selected := a.historySelected == id
					label := a.tr("查看 ") + r.Name + " · " + r.Started.Local().Format("01-02 15:04:05") + " · " + a.tr(appcore.RunOutcome(r))
					btn := ui.ButtonBase(c).Label(label).Justify(ui.Start).FillWidth().Padding(8, 12).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border)
					if selected {
						btn = paintRibbonStripe(btn.Background(porcelain.Surface))
					}
					btn.Children(func() {
						ui.Text(c, label).FontSize(13).SingleLine().Ellipsis("…")
					}).OnClick(func() { a.historySelected = id })
				}
			})
		}).Height(165).Border(1, porcelain.Border).Background(ribbonSidebarBg)
		r, ok := a.selectedHistory()
		if !ok {
			ui.Text(c, a.tr("暂无可用运行记录。")).TextColor(porcelain.TextMuted)
			return
		}
		porcelainScroll(c, &a.scrolls.historyDetail, func() {
			ui.Column(c).FillWidth().Padding(14).Gap(8).Children(func() {
				ui.Text(c, r.Name+a.tr(" · ")+a.tr(appcore.RunOutcome(r))).FontSize(15).Bold()
				if r.SourcePath != "" {
					ui.Text(c, a.tr("源目录: ")+r.SourcePath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
				}
				if a.historyDetails {
					ui.Text(c, fmt.Sprintf(a.tr("任务 %s · 运行 %s · %s / %s"), r.TaskID, r.ID, r.Kind, a.tr(string(r.Origin)))).FontSize(12).TextColor(porcelain.TextMuted)
				}
				ui.Text(c, a.tr("开始: ")+r.Started.Local().Format("2006-01-02 15:04:05 MST")).FontSize(13)
				if !r.Ended.IsZero() {
					endedText := a.tr("结束: ") + r.Ended.Local().Format("2006-01-02 15:04:05 MST")
					if dur := formatRunDuration(r.Started, r.Ended); dur != "" {
						endedText += " (" + dur + ")"
					}
					ui.Text(c, endedText).FontSize(13)
				} else {
					ui.Text(c, a.tr("未记录结束；可能已有文件操作，请核对，不能视作成功或未执行。")).FontSize(13).TextColor(porcelain.Warning)
				}
				if r.Error != "" {
					a.renderLogErrorBlock(c, r.Error, porcelain.Danger)
				}
				for _, g := range r.Goals {
					ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
					ui.Text(c, g.Name+a.tr(" · ")+a.tr(string(g.State))).Bold()
					if g.TargetPath != "" {
						ui.Text(c, a.tr("目标目录: ")+g.TargetPath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
					}
					if g.Stage != "" {
						ui.Text(c, a.tr("阶段: ")+a.tr(g.Stage)).FontSize(12).TextColor(porcelain.TextMuted)
					}
					if a.historyDetails {
						ui.Text(c, a.tr("目标ID: ")+string(g.ID)).FontSize(12).TextColor(porcelain.TextMuted)
					}
					if a.historyDetails && !g.Started.IsZero() {
						ui.Text(c, a.tr("目标开始: ")+g.Started.Local().Format("01-02 15:04:05")).FontSize(12).TextColor(porcelain.TextMuted)
					}
					if a.historyDetails && !g.Ended.IsZero() {
						ui.Text(c, a.tr("目标结束: ")+g.Ended.Local().Format("01-02 15:04:05")).FontSize(12).TextColor(porcelain.TextMuted)
					}
					if r.Outcome != "running" {
						if counts := a.runCounts(g, a.historyDetails); counts != "" {
							ui.Text(c, counts).FontSize(13)
						}
					}
					if g.Error != "" {
						a.renderLogErrorBlock(c, g.Error, porcelain.Danger)
					}
				}
			})
		}).Grow(1).FillWidth().Border(1, porcelain.Border).Background(porcelain.Surface)
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

func (a *application) openDestinationLog(task, destination taskconfig.ID) {
	a.logTask = task
	a.logDestination = destination
	a.logOpenError = nil
	title := a.tr("运行记录")
	if a.tasks != nil {
		if t, e := a.tasks.Find(task); e == nil {
			for _, d := range t.Destinations {
				if d.ID == destination {
					title += " · " + d.Name
				}
			}
		}
	}
	if a.destinationLog != nil {
		a.destinationLog.SetTitle(title)
		a.destinationLog.Show()
		a.destinationLog.Focus()
		redrawWindow(a.destinationLog)
		a.checkDestinationLogFile(a.destinationLog)
		return
	}
	window, err := createStartupWindow(func() *mygo.Window {
		return mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 720, Height: 420, UseContentSize: true, MinWidth: 420, MinHeight: 260, Content: ui.View(a.destinationLogView)})
	})
	if err != nil {
		a.historyError = err
		return
	}
	a.destinationLog = window
	_ = window.SetIcon(porcelainAppIcon)
	window.OnClose(func(*mygo.CloseEvent) { a.destinationLog = nil })
	a.checkDestinationLogFile(window)
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

func (a *application) checkDestinationLogFile(window *mygo.Window) {
	a.logFileAvailable = false
	path := a.destinationLogPath()
	go func() {
		available := inspectDestinationLogFile(path) == nil
		a.window.Update(func() {
			if a.destinationLog == window {
				window.Update(func() { a.logFileAvailable = available })
			}
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
			if a.destinationLog != nil {
				a.destinationLog.Update(func() { a.logFileAvailable = available })
			}
		})
	}()
}
func (a *application) destinationLogView(c *ui.Context) {
	c.SetTheme(&porcelain)
	records := a.destinationLogRecords()
	name := ""
	if a.tasks != nil {
		if task, e := a.tasks.Find(a.logTask); e == nil {
			name = task.Name
			for _, d := range task.Destinations {
				if d.ID == a.logDestination {
					name += " → " + d.Name
				}
			}
		}
	}
	if name == "" && len(records) > 0 {
		name = records[0].Name
		for _, g := range records[0].Goals {
			if g.ID == a.logDestination {
				name += " → " + g.Name
			}
		}
	}
	ui.Column(c).Fill().Gap(12).Padding(20).Children(func() {
		ui.Column(c).Gap(2).LineHeight(1.5).Children(func() {
			ui.Text(c, a.tr("同步日志")).FontSize(18).Bold().Tooltip(a.tr("显示最近20条已记录运行结果；刷新读取当前诊断快照，不重试同步。"))
			ui.Text(c, name).FontSize(16).MaxLines(2).TextColor(porcelain.TextMuted).Tooltip(name)
			if a.logDetails {
				ui.Text(c, string(a.logTask)).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
				porcelainPath(c, winutil.PathLabel(filepath.Join(a.installation.Data, appcore.RunHistoryFile)))
			}
		})
		if a.historyError != nil {
			ui.Text(c, a.tr("记录不可用或未保存：")+a.historyError.Error()).TextColor(porcelain.Danger)
		}
		if a.logOpenError != nil {
			ui.Text(c, a.tr("无法打开日志文件：%s", a.logOpenError.Error())).TextColor(porcelain.Danger)
		}
		porcelainScroll(c, &a.scrolls.destLog, func() {
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				count := 0
				for _, r := range records {
					for _, g := range r.Goals {
						if g.ID != a.logDestination {
							continue
						}
						rowBg := porcelain.Surface
						if count%2 == 1 {
							rowBg = ribbonRailBg
						}
						count++
						ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(12).Padding(10, 12).Background(rowBg).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
							ui.Text(c, r.Started.Local().Format("01-02 15:04:05")).Font("Consolas").FontSize(14).TextColor(porcelain.TextMuted).Shrink(0)
							color := porcelain.Text
							if g.Error != "" || r.Error != "" {
								color = porcelain.Danger
							}
							if g.State == syncengine.DestinationCanceled || g.State == syncengine.DestinationInterrupted || r.Outcome == "stopped" {
								color = porcelain.Warning
							}
							ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
								stateText := a.tr(string(g.State))
								if dur := formatRunDuration(r.Started, r.Ended); dur != "" {
									stateText += " · " + dur
								}
								ui.Text(c, stateText).Font("Consolas").FontSize(14).FontWeight(600).TextColor(color)
								if r.SourcePath != "" {
									ui.Text(c, a.tr("源目录: ")+r.SourcePath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
								}
								if g.TargetPath != "" {
									ui.Text(c, a.tr("目标目录: ")+g.TargetPath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted)
								}
								if g.Stage != "" {
									ui.Text(c, a.tr("阶段: ")+a.tr(g.Stage)).FontSize(12).TextColor(porcelain.TextMuted)
								}
								if counts := a.runCounts(g, a.logDetails); counts != "" {
									ui.Text(c, counts).FontSize(13)
								}
								if g.Error != "" {
									a.renderLogErrorBlock(c, g.Error, color)
								}
								if r.Error != "" && g.Error == "" {
									a.renderLogErrorBlock(c, r.Error, color)
								}
								if r.Ended.IsZero() {
									ui.Text(c, a.tr("未记录结束；可能已有文件操作，请核对，不能视作成功或未执行。"))
								}
							})
						})
					}
				}
				if count == 0 {
					ui.Box(c).Padding(12).Children(func() {
						ui.Text(c, a.tr("暂无可用运行记录。")).TextColor(porcelain.TextMuted)
					})
				}
			})
		}).Grow(1).Shrink(1).MinHeight(0).FillWidth().Border(1, porcelain.Border).Background(porcelain.Surface)
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			porcelainButton(c, a.tr("诊断详情")).Height(32).MinWidth(96).OnClick(func() { a.logDetails = !a.logDetails })
			ui.Box(c).Grow(1)
			porcelainButton(c, a.tr("刷新")).Height(32).MinWidth(96).Disabled(a.logReading).OnClick(a.refreshDestinationLog)
			porcelainPrimaryButton(c, a.tr("打开日志文件")).Height(32).MinWidth(96).Disabled(!a.logFileAvailable || a.logReading).OnClick(func() {
				a.openDestinationLogFile(mygo.Shell.OpenPath)
			})
		})
	})
}

func (a *application) destinationLogRecords() []appcore.RunRecord {
	out := make([]appcore.RunRecord, 0, 20)
	seen := map[taskconfig.ID]bool{}
	add := func(r appcore.RunRecord) {
		if r.TaskID != a.logTask || seen[r.ID] || len(out) >= 20 {
			return
		}
		for _, g := range r.Goals {
			if g.ID == a.logDestination {
				out = append(out, r)
				seen[r.ID] = true
				return
			}
		}
	}
	for i := len(a.historyView.Recent) - 1; i >= 0 && len(out) < 20; i-- {
		add(a.historyView.Recent[i])
	}
	if latest, ok := a.historyView.Latest[a.logTask]; ok {
		add(latest)
	}
	return out
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
