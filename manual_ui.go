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
	phase              manualPhase
	source, target     string
	request            manualTaskRequest
	cancel             context.CancelFunc
	progress           transferProgress
	message            string
	quitPending        bool
	id                 taskconfig.ID
	name               string
	targets            []manualDestination
	targetID           taskconfig.ID
	targetName         string
	dirty              bool
	destinationResults multipleResult
	filterEditor       filterEditor
	filterDrafts       map[taskconfig.ID]filterEditor
}

type savedTaskResult struct {
	message      string
	progress     transferProgress
	destinations multipleResult
}

func (s *manualSession) busy() bool {
	return s.phase == manualChecking || s.phase == manualRunning || s.phase == manualStopping || s.phase == manualChoosing || s.phase == manualLoading || s.phase == manualSaving
}
func (s *manualSession) edited() {
	if s.busy() {
		return
	}
	s.phase = manualDraft
	s.flushTarget()
	s.dirty = true
	s.message = "编辑尚未保存；请保存或明确放弃编辑后再切换任务。"
	s.progress = transferProgress{}
	s.destinationResults = nil
}

func (a *application) manualView(c *ui.Context) {
	s := &a.manual
	locked := s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(210).FillHeight().Gap(10).Padding(16).Children(func() {
			ui.Text(c, "任务列表").FontSize(22).Bold()
			ui.Button(c, "新建任务").Disabled(s.busy() || s.dirty || a.tasks == nil || a.taskError != nil).OnClick(a.newManual)
			ui.Button(c, "放弃编辑").Disabled(s.busy() || !s.dirty || a.tasks == nil || a.taskError != nil).OnClick(a.discardManual)
			ui.Button(c, "重新载入").Disabled(s.busy()).OnClick(a.reloadTasks)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					if a.tasks != nil {
						for _, t := range a.tasks.tasks {
							label := t.Name
							if _, err := multipleRequestFromSaved(t); err != nil {
								label += "（不支持）"
							}
							if t.ID == s.id {
								label = "● " + label
							}
							id := t.ID
							ui.Button(c, label).FillWidth().Disabled(s.busy() || s.dirty || a.taskError != nil).OnClick(func() { a.selectManual(id) })
						}
					}
				})
			})
		})
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			ui.Column(c).FillWidth().Gap(8).Padding(16).Children(func() {
				ui.Text(c, "SyncMaid").FontSize(26).Bold()
				a.manualActions(c, locked)
			})
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Padding(20).Children(func() {
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
					a.manualTargetsView(c, locked)
					ui.Text(c, "更新大小或整秒修改时间不同的文件；不删除目标额外文件。")
					ui.Text(c, s.message)
					p := s.progress
					ui.Text(c, fmt.Sprintf("当前/最后目标 %s · 已发布 %d / %d · 未变化 %d · 已传输 %d 字节", p.Stage, p.Copied, p.Total, p.Unchanged, p.Bytes))
					ui.Text(c, p.Path).FontSize(12)
					ui.Text(c, "关闭窗口隐藏到托盘，不停止任务。停止保留已发布文件。")
					ui.Text(c, "Data: "+pathLabel(a.installation.data)).FontSize(12)
				})
			})
		})
	})
}
func (a *application) chooseFolder(source bool) {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil {
		return
	}
	previous := s.phase
	s.phase = manualChoosing
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择文件夹", Directory: true})
		a.window.Update(func() {
			s.phase = previous
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
	if a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil || s.dirty || (s.phase != manualReady && s.phase != manualFinished) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.phase = manualRunning
	s.message = "正在运行…"
	s.progress = transferProgress{}
	q := s.request
	s.destinationResults = initialDestinationResults(q)
	go func() {
		last := time.Time{}
		var lastID taskconfig.ID
		var lastState destinationState
		notify := func(r destinationResult) {
			if r.ID == lastID && r.State == lastState && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			lastID = r.ID
			lastState = r.State
			a.window.Update(func() {
				s.destinationResults[r.ID] = r
				s.progress = r.Progress
				if r.State != destinationRunning {
					s.progress.Stage = string(r.State)
				}
			})
		}
		var results multipleResult
		err := interruptibleIO(ctx, func(ctx context.Context) error { var e error; results, e = runMultiple(ctx, q, notify); return e })
		a.window.Update(func() {
			cancel()
			s.cancel = nil
			s.destinationResults = results
			s.phase = manualFinished
			switch {
			case err == nil:
				s.message = "全部目标同步完成"
			case errors.Is(err, context.Canceled):
				s.message = "任务已停止；已完成目标不回滚。详情: " + err.Error()
			default:
				s.message = "任务存在失败或未执行目标: " + err.Error()
			}
			if err != nil {
				slog.Warn("multiple-sync-ended", "error", err, "targets", len(q.Destinations))
			}
			if s.id != "" {
				if a.results == nil {
					a.results = make(map[taskconfig.ID]savedTaskResult)
				}
				a.results[s.id] = savedTaskResult{message: s.message, progress: s.progress, destinations: s.destinationResults}
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

func (a *application) manualActions(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "保存任务").Disabled(locked || a.taskError != nil).OnClick(a.createManual)
		ui.Button(c, "删除任务").Disabled(locked || a.taskError != nil || s.id == "").OnClick(a.confirmDeleteManual)
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "运行").Disabled(a.taskError != nil || (s.phase != manualReady && s.phase != manualFinished)).OnClick(a.startManual)
		ui.Button(c, "停止").Disabled(s.phase != manualRunning && s.phase != manualChecking).OnClick(a.stopManual)
		ui.Button(c, "Exit").OnClick(a.requestQuit)
	})

}
