package main

import (
	"errors"
	"os"
	"path/filepath"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *application) openDestinationLog(task, destination taskconfig.ID) {
	a.logTask = task
	a.logDestination = destination
	a.logOpenError = nil
	title := a.tr("运行记录")
	if a.tasks != nil {
		if t, e := a.tasks.find(task); e == nil {
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
		a.destinationLog.Update(func() {})
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
	return filepath.Join(a.installation.data, runHistoryFile)
}

func inspectDestinationLogFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !historyRegularFile(info) {
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
	a.logOpenError = open(pathLabel(path))
}
func (a *application) refreshDestinationLog() {
	if a.logReading {
		return
	}
	a.logReading = true
	data := a.installation.data
	path := a.destinationLogPath()
	go func() {
		history := loadRunHistory(data)
		available := inspectDestinationLogFile(path) == nil
		a.window.Update(func() {
			a.logReading = false
			a.historyError = history.err
			if history.err == nil {
				a.historyView = history.data
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
		if task, e := a.tasks.find(a.logTask); e == nil {
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
				porcelainPath(c, pathLabel(filepath.Join(a.installation.data, runHistoryFile)))
			}
		})
		if a.historyError != nil {
			ui.Text(c, a.tr("记录不可用或未保存：")+a.historyError.Error()).TextColor(porcelain.Danger)
		}
		if a.logOpenError != nil {
			ui.Text(c, a.tr("无法打开日志文件：%s", a.logOpenError.Error())).TextColor(porcelain.Danger)
		}
		ui.Scroll(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Background(rgb(0xE7EAEC)).Children(func() {
			ui.Column(c).FillWidth().Padding(10).Gap(6).Children(func() {
				count := 0
				for _, r := range records {
					for _, g := range r.Goals {
						if g.ID != a.logDestination {
							continue
						}
						count++
						ui.Row(c).FillWidth().Gap(10).Children(func() {
							ui.Text(c, r.Started.Local().Format("01-02 15:04:05")).Font("Consolas").FontSize(14).TextColor(porcelain.TextMuted)
							color := porcelain.Text
							if g.Error != "" || r.Error != "" {
								color = porcelain.Danger
							}
							if g.State == destinationCanceled || g.State == destinationInterrupted || r.Outcome == "stopped" {
								color = porcelain.Warning
							}
							ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
								ui.Text(c, a.tr(string(g.State))).Font("Consolas").FontSize(14).TextColor(color)
								if counts := a.runCounts(g, a.logDetails); counts != "" {
									ui.Text(c, counts).FontSize(13)
								}
								if g.Error != "" {
									ui.Text(c, g.Error).Font("Consolas").FontSize(14).TextColor(color)
								}
								if r.Error != "" && g.Error == "" {
									ui.Text(c, r.Error).Font("Consolas").FontSize(14).TextColor(color)
								}
								if r.Ended.IsZero() {
									ui.Text(c, a.tr("未记录结束；可能已有文件操作，请核对，不能视作成功或未执行。"))
								}
							})
						})
					}
				}
				if count == 0 {
					ui.Text(c, a.tr("暂无可用运行记录。")).TextColor(porcelain.TextMuted)
				}
			})
		})
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			porcelainButton(c, a.tr("诊断详情")).OnClick(func() { a.logDetails = !a.logDetails })
			ui.Box(c).Grow(1)
			porcelainButton(c, a.tr("刷新")).FontSize(14).Padding(3, 8).Disabled(a.logReading).OnClick(a.refreshDestinationLog)
			ui.PrimaryButton(c, a.tr("打开日志文件")).FontSize(14).Padding(3, 8).Disabled(!a.logFileAvailable || a.logReading).OnClick(func() {
				a.openDestinationLogFile(mygo.Shell.OpenPath)
			})
		})
	})
}

func (a *application) destinationLogRecords() []runRecord {
	out := make([]runRecord, 0, 20)
	seen := map[taskconfig.ID]bool{}
	add := func(r runRecord) {
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
