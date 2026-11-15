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
	manualImporting
)

// All session state belongs to the native main thread. Workers only receive
// immutable requests and post value copies. Persistence also runs off-thread.
type manualSession struct {
	cronText, cronNext      string
	trigger                 taskconfig.Trigger
	quietText, triggerError string
	onEdit                  func()
	kind                    taskconfig.TaskKind
	phase                   manualPhase
	source, target          string
	request                 manualTaskRequest
	cancel                  context.CancelFunc
	progress                transferProgress
	message                 localizedMessage
	quitPending             bool
	id                      taskconfig.ID
	name                    string
	targets                 []manualDestination
	targetID                taskconfig.ID
	targetName              string
	dirty                   bool
	destinationResults      multipleResult
	filterEditor            filterEditor
	filterDrafts            map[taskconfig.ID]filterEditor
	mirrorPreview           *mirrorPreview
	mirrorReply             chan error
}

type savedTaskResult struct {
	started, ended time.Time
	message        localizedMessage
	progress       transferProgress
	destinations   multipleResult
}

func (s *manualSession) busy() bool {
	return s.phase == manualChecking || s.phase == manualRunning || s.phase == manualStopping || s.phase == manualChoosing || s.phase == manualLoading || s.phase == manualSaving || s.phase == manualImporting
}
func (s *manualSession) edited() {
	if s.busy() {
		return
	}
	if s.onEdit != nil {
		s.onEdit()
	}
	s.phase = manualDraft
	s.flushTarget()
	s.dirty = true
	s.message = messagef("编辑尚未保存；请保存或明确放弃编辑后再切换任务。")
	s.progress = transferProgress{}
	s.destinationResults = nil
}

func (a *application) manualView(c *ui.Context) {
	s := &a.manual
	locked := s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(210).FillHeight().Gap(10).Padding(16).Children(func() {
			ui.Text(c, a.tr("任务列表")).FontSize(22).Bold()
			ui.Button(c, a.tr("新建任务")).Disabled(s.busy() || s.dirty || a.tasks == nil || a.taskError != nil).OnClick(a.newManual)
			ui.Button(c, a.tr("放弃编辑")).Disabled(s.busy() || !s.dirty || a.tasks == nil || a.taskError != nil).OnClick(a.discardManual)
			ui.Button(c, a.tr("重新载入")).Disabled(s.busy()).OnClick(a.reloadTasks)
			ui.Button(c, a.tr("导入旧任务")).Disabled(s.busy() || s.dirty || a.tasks == nil || a.taskError != nil).OnClick(a.beginImport)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					if a.tasks != nil {
						for _, t := range a.tasks.tasks {
							label := t.Name
							if _, err := multipleRequestFromSaved(t); err != nil {
								label += a.tr("（不支持）")
							}
							if t.ID == s.id {
								label = "● " + label
							}
							id := t.ID
							if w := a.watches[id]; w != nil {
								ui.Text(c, a.render(w.status)).FontSize(11)
							}
							if result, ok := a.results[id]; ok {
								ui.Text(c, a.render(result.message)).FontSize(11)
								if !result.started.IsZero() {
									ui.Text(c, a.tr("最近开始: ")+result.started.Local().Format("01-02 15:04:05")).FontSize(11)
								}
							}
							ui.Button(c, label).FillWidth().Disabled(s.busy() || s.dirty || a.taskError != nil).OnClick(func() { a.selectManual(id) })
						}
					}
				})
			})
		})
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			ui.Column(c).FillWidth().Gap(8).Padding(16).Children(func() {
				ui.Text(c, "SyncMaid").FontSize(26).Bold()
				settingsLabel := a.tr("设置")
				if a.settingsOpen {
					settingsLabel = a.tr("返回任务编辑")
				}
				ui.Button(c, settingsLabel).Disabled(a.importPreview != nil).OnClick(func() {
					a.settingsOpen = !a.settingsOpen
					if a.settingsOpen {
						a.historyOpen = false
					}
				})
				a.manualActions(c, locked)
				a.watchView(c)
				if a.importError != nil && a.importPreview == nil {
					ui.Text(c, a.tr("导入拒绝，旧文件和任务库未修改：%s", a.importError.Error()))
				}
				historyLabel := "查看运行记录"
				if a.historyOpen {
					historyLabel = "返回任务编辑"
				}
				ui.Button(c, a.tr(historyLabel)).Disabled(a.importPreview != nil).OnClick(a.toggleHistory)
				if a.historyError != nil {
					ui.Text(c, a.tr("运行记录不可用或未保存；文件执行结果仍有效：")+historyText(a.historyError.Error(), historyErrorBytes)).FontSize(12)
				}
				a.mirrorPreviewView(c)
			})
			if a.importPreview != nil {
				a.importView(c)
				return
			}
			if a.settingsOpen {
				a.settingsView(c)
				return
			}
			if a.historyOpen {
				a.historyViewContent(c)
				return
			}
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Padding(20).Children(func() {
					ui.Text(c, a.tr("手动同步 · 任务保存在 tasks-go.json"))
					if a.taskError != nil {
						ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error())
						ui.Text(c, a.tr("修复文件或备份问题后重新载入。不会以空列表覆盖。"))
					}
					if issue := a.selectedTaskIssue(); issue != nil {
						ui.Text(c, a.tr("此任务不可运行或编辑: ")+issue.Error())
					}
					ui.Text(c, a.tr("任务名称"))
					ui.TextInput(c, &s.name).Label(a.tr("任务名称")).ReadOnly(locked).OnChange(s.edited)
					ui.Text(c, a.tr("源文件夹"))
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &s.source).Label(a.tr("源文件夹路径")).Grow(1).Placeholder(a.tr("源文件夹绝对路径")).ReadOnly(locked).OnChange(s.edited)
						ui.Button(c, a.tr("选择源")).Disabled(locked).OnClick(func() { a.chooseFolder(true) })
					})
					a.triggerView(c, locked)
					a.manualKindView(c, locked)
					a.manualTargetsView(c, locked)
					ui.Text(c, a.tr("新增/更新依大小和整秒修改时间判断；Mirror移除另行确认。"))
					ui.Text(c, a.render(s.message))
					p := s.progress
					ui.Text(c, fmt.Sprintf(a.tr("当前/最后目标 %s · 已发布 %d / %d · 未变化 %d · 已传输 %d 字节"), a.tr(p.Stage), p.Copied, p.Total, p.Unchanged, p.Bytes))
					ui.Text(c, p.Path).FontSize(12)
					ui.Text(c, a.tr("关闭行为可在设置中选择；停止保留已发布文件。"))
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
	title := a.tr("选择文件夹")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: title, Directory: true})
		a.window.Update(func() {
			s.phase = previous
			if err != nil {
				s.message = messagef("选择失败: %s", err.Error())
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
			} else {
				a.drainWatches()
			}
		})
	}()
}
func (a *application) startManual() {
	s := &a.manual
	if a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil || s.dirty || (s.phase != manualReady && s.phase != manualFinished) {
		return
	}
	a.startTaskRun(s.id, s.name, s.request, false)
}
func (a *application) startTaskRun(id taskconfig.ID, name string, q manualTaskRequest, automatic bool) {
	s := &a.manual
	if s.busy() {
		return
	}
	if !automatic {
		if w := a.watches[id]; w != nil {
			w.ticket.invalidate(watchArmed)
			if w.timer != nil {
				w.timer.Stop()
			}
			if w.schedule != nil {
				a.armScheduled(w, time.Now())
			}
		}
	}
	previous := s.phase
	showResult := s.id == id && !s.dirty
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.phase = manualRunning
	a.watchActivity = messagef("%s: 正在运行", name)
	if showResult {
		s.message = messagef("正在运行…")
	}
	if showResult {
		s.progress = transferProgress{}
		s.destinationResults = initialDestinationResults(q)
	}
	history := a.history
	active := []taskconfig.ID(nil)
	if a.tasks != nil {
		active = taskIDs(a.tasks.tasks)
	}
	recordStart, recordStartErr := newRunRecord(id, name, q, automatic, time.Now())
	go func() {
		last := time.Time{}
		var lastID taskconfig.ID
		var lastState destinationState
		var lastStage string
		notify := func(r destinationResult) {
			if r.ID == lastID && r.State == lastState && r.Progress.Stage == lastStage && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			lastID = r.ID
			lastState = r.State
			lastStage = r.Progress.Stage
			a.window.Update(func() {
				if !showResult {
					return
				}
				s.destinationResults[r.ID] = r
				s.progress = r.Progress
				if r.State != destinationRunning {
					s.progress.Stage = string(r.State)
				}
			})
		}
		historyView, recordEnd, results, err, historyErr := executeRecordedRun(history, recordStart, recordStartErr, active, func() (multipleResult, error) {
			var results multipleResult
			err := interruptibleIO(ctx, func(ctx context.Context) error {
				var e error
				results, e = runMultipleConfirmed(ctx, q, notify, a.confirmMirror)
				return e
			})
			return results, err
		})
		a.window.Update(func() {
			cancel()
			s.cancel = nil
			a.historyError = historyErr
			if historyView.Version == 1 {
				a.historyView = historyView
			}
			if a.results == nil {
				a.results = map[taskconfig.ID]savedTaskResult{}
			}
			message := messagef("运行完成")
			if err != nil {
				message = messagef("运行未完成: %s", historyText(err.Error(), historyErrorBytes))
			}
			a.results[id] = savedTaskResult{message: message, destinations: results, started: recordEnd.Started, ended: recordEnd.Ended}
			a.watchActivity = messagef("%s: %s", name, message)
			if w := a.watches[id]; w != nil && w.ticket.phase == watchArmed {
				w.status = message
			}
			if !showResult {
				s.phase = previous
				if s.quitPending {
					a.requestQuit()
				} else {
					a.drainWatches()
				}
				return
			}
			s.destinationResults = results
			s.phase = manualFinished
			switch {
			case err == nil:
				s.message = messagef("全部目标同步完成")
			case errors.Is(err, context.Canceled):
				s.message = messagef("任务已停止；已完成目标不回滚。详情: %s", historyText(err.Error(), historyErrorBytes))
			default:
				s.message = messagef("任务存在失败或未执行目标: %s", historyText(err.Error(), historyErrorBytes))
			}
			if err != nil {
				slog.Warn("multiple-sync-ended", "error", err, "targets", len(q.Destinations))
			}
			if id != "" {
				if a.results == nil {
					a.results = make(map[taskconfig.ID]savedTaskResult)
				}
				a.results[id] = savedTaskResult{message: s.message, progress: s.progress, destinations: s.destinationResults, started: recordEnd.Started, ended: recordEnd.Ended}
			}
			if s.quitPending {
				a.requestQuit()
			} else {
				a.drainWatches()
			}
		})
	}()
}
func (a *application) stopManual() {
	a.stopWatches(watchesPaused)
	s := &a.manual
	if s.phase != manualRunning && s.phase != manualChecking {
		return
	}
	s.phase = manualStopping
	s.message = messagef("正在停止并清理临时文件…")
	s.cancel()
}
func (a *application) requestQuit() {
	a.stopWatches(watchesStopped)
	if a.manual.phase == manualImporting {
		a.cancelImport()
	}
	s := &a.manual
	if a.languageBusy || a.commonBusy {
		s.quitPending = true
		return
	}
	if s.busy() {
		s.quitPending = true
		if s.phase == manualChoosing {
			s.message = messagef("请关闭当前对话框以退出。")
		}
		if s.phase == manualLoading || s.phase == manualSaving {
			s.message = messagef("文件操作结束后退出…")
		}
		a.stopManual()
		return
	}
	a.lifecycle.exit()
}
func (a *application) beforeQuit(e *mygo.QuitEvent) {
	a.stopWatches(watchesStopped)
	if a.manual.phase == manualImporting {
		a.cancelImport()
	}
	if a.manual.busy() || a.languageBusy || a.commonBusy {
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
		ui.Button(c, a.tr("保存任务")).Disabled(locked || a.taskError != nil).OnClick(a.createManual)
		ui.Button(c, a.tr("删除任务")).Disabled(locked || a.taskError != nil || s.id == "").OnClick(a.confirmDeleteManual)
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, a.tr("运行")).Disabled(a.taskError != nil || (s.phase != manualReady && s.phase != manualFinished)).OnClick(a.startManual)
		ui.Button(c, a.tr("停止")).Disabled(s.phase != manualRunning && s.phase != manualChecking).OnClick(a.stopManual)
		ui.Button(c, a.tr("Exit")).OnClick(a.requestQuit)
	})

}
