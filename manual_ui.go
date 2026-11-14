package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"syncmaid/internal/taskconfig"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type manualPhase uint8

const (
	manualDraft manualPhase = iota
	manualChoosing
	manualChecking
	manualReady
	manualRunning
	manualStopping
	manualFinished
	manualLoading
	manualSaving
)

// All session state belongs to the native main thread. Workers only receive
// immutable requests and post value copies. Persistence also runs off-thread.
type manualSession struct {
	phase          manualPhase
	source, target string
	request        manualRequest
	cancel         context.CancelFunc
	progress       transferProgress
	message        string
	quitPending    bool
	id             taskconfig.ID
	name           string
}

type savedTaskResult struct {
	message  string
	progress transferProgress
}

func (s *manualSession) busy() bool {
	return s.phase == manualChecking || s.phase == manualRunning || s.phase == manualStopping || s.phase == manualChoosing || s.phase == manualLoading || s.phase == manualSaving
}
func (s *manualSession) edited() {
	if s.busy() {
		return
	}
	s.phase = manualDraft
	s.message = "编辑尚未保存；保存后才能运行。切换任务前请保存。"
	s.progress = transferProgress{}
}

func (a *application) manualView(c *ui.Context) {
	s := &a.manual
	locked := s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(210).FillHeight().Gap(10).Padding(16).Children(func() {
			ui.Text(c, "任务列表").FontSize(22).Bold()
			ui.Button(c, "新建任务").Disabled(s.busy() || a.tasks == nil || a.taskError != nil).OnClick(a.newManual)
			ui.Button(c, "重新载入").Disabled(s.busy()).OnClick(a.reloadTasks)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					if a.tasks != nil {
						for _, t := range a.tasks.tasks {
							label := t.Name
							if _, err := requestFromSaved(t); err != nil {
								label += "（不支持）"
							}
							if t.ID == s.id {
								label = "● " + label
							}
							id := t.ID
							ui.Button(c, label).FillWidth().Disabled(s.busy() || a.taskError != nil).OnClick(func() { a.selectManual(id) })
						}
					}
				})
			})
		})
		ui.Scroll(c).Grow(1).FillHeight().Children(func() {
			ui.Column(c).FillWidth().Gap(12).Padding(20).Children(func() {
				ui.Text(c, "SyncMaid").FontSize(26).Bold()
				ui.Text(c, "手动仅添加 · 任务保存在 tasks-go.json")
				if a.taskError != nil {
					ui.Text(c, "任务文件不可用，已阻止保存与运行: "+a.taskError.Error())
					ui.Text(c, "修复文件或备份问题后重新载入。不会以空列表覆盖。")
				}
				if issue := a.selectedTaskIssue(); issue != nil {
					ui.Text(c, "此任务不可运行或编辑: "+issue.Error())
				}
				ui.Text(c, "任务名称")
				ui.TextInput(c, &s.name).Label("任务名称").ReadOnly(locked).OnChange(s.edited)
				ui.Text(c, "源文件夹")
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c, &s.source).Label("源文件夹路径").Grow(1).Placeholder("源文件夹绝对路径").ReadOnly(locked).OnChange(s.edited)
					ui.Button(c, "选择源").Disabled(locked).OnClick(func() { a.chooseFolder(true) })
				})
				ui.Text(c, "目标文件夹（须已存在）")
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c, &s.target).Label("目标文件夹路径").Grow(1).Placeholder("目标文件夹绝对路径").ReadOnly(locked).OnChange(s.edited)
					ui.Button(c, "选择目标").Disabled(locked).OnClick(func() { a.chooseFolder(false) })
				})
				ui.Text(c, "更新大小或整秒修改时间不同的文件；不删除目标额外文件。")
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "保存任务").Disabled(locked || a.taskError != nil).OnClick(a.createManual)
					ui.Button(c, "删除任务").Disabled(locked || a.taskError != nil || s.id == "").OnClick(a.confirmDeleteManual)
				})
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "运行").Disabled(a.taskError != nil || (s.phase != manualReady && s.phase != manualFinished)).OnClick(a.startManual)
					ui.Button(c, "停止").Disabled(s.phase != manualRunning && s.phase != manualChecking).OnClick(a.stopManual)
					ui.Button(c, "Exit").OnClick(a.requestQuit)
				})
				ui.Text(c, s.message)
				p := s.progress
				ui.Text(c, fmt.Sprintf("%s · 已发布 %d / %d · 未变化 %d · 已传输 %d 字节", p.Stage, p.Copied, p.Total, p.Unchanged, p.Bytes))
				ui.Text(c, p.Path).FontSize(12)
				ui.Text(c, "关闭窗口隐藏到托盘，不停止任务。停止保留已发布文件。")
				ui.Text(c, "Data: "+pathLabel(a.installation.data)).FontSize(12)
			})
		})
	})
}
func (a *application) chooseFolder(source bool) {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil {
		return
	}
	s.phase = manualChoosing
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择文件夹", Directory: true})
		a.window.Update(func() {
			s.phase = manualDraft
			if err != nil {
				s.message = "选择失败: " + err.Error()
			} else if len(paths) > 0 {
				if source {
					s.source = paths[0]
				} else {
					s.target = paths[0]
				}
				s.edited()
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) startManual() {
	s := &a.manual
	if a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil || (s.phase != manualReady && s.phase != manualFinished) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.phase = manualRunning
	s.message = "正在运行…"
	s.progress = transferProgress{}
	q := s.request
	go func() {
		last := time.Time{}
		notify := func(p transferProgress) {
			if time.Since(last) < 100*time.Millisecond && p.Stage != "已完成" {
				return
			}
			last = time.Now()
			a.window.Update(func() { s.progress = p })
		}
		var p transferProgress
		err := interruptibleIO(ctx, func(ctx context.Context) error { var e error; p, e = runManual(ctx, q, notify); return e })
		a.window.Update(func() {
			cancel()
			s.cancel = nil
			s.progress = p
			s.phase = manualFinished
			switch {
			case err == nil:
				s.message = "同步完成"
			case errors.Is(err, context.Canceled):
				s.message = "已停止；已发布文件保留。详情: " + err.Error()
				s.progress.Stage = "已停止"
			default:
				s.message = "同步失败（可能已有部分文件完成）: " + err.Error()
				s.progress.Stage = "失败"
			}
			if err != nil {
				slog.Warn("manual-sync-stopped", "error", err, "copied", p.Copied, "unchanged", p.Unchanged)
			}
			if s.id != "" {
				if a.results == nil {
					a.results = make(map[taskconfig.ID]savedTaskResult)
				}
				a.results[s.id] = savedTaskResult{s.message, s.progress}
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) stopManual() {
	s := &a.manual
	if s.phase != manualRunning && s.phase != manualChecking {
		return
	}
	s.phase = manualStopping
	s.message = "正在停止并清理临时文件…"
	s.cancel()
}
func (a *application) requestQuit() {
	s := &a.manual
	if s.busy() {
		s.quitPending = true
		if s.phase == manualChoosing {
			s.message = "请关闭当前对话框以退出。"
		}
		if s.phase == manualLoading || s.phase == manualSaving {
			s.message = "文件操作结束后退出…"
		}
		a.stopManual()
		return
	}
	a.lifecycle.exit()
}
func (a *application) beforeQuit(e *mygo.QuitEvent) {
	if a.manual.busy() {
		e.PreventDefault()
		a.manual.quitPending = true
		a.stopManual()
		return
	}
	a.lifecycle.beginQuit()
}
