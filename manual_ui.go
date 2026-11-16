package main

import (
	"context"
	"errors"
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
	manualReviewing
	manualReviewWorking
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
	return s.phase == manualChecking || s.phase == manualRunning || s.phase == manualStopping || s.phase == manualChoosing || s.phase == manualLoading || s.phase == manualSaving || s.phase == manualImporting || s.phase == manualReviewing || s.phase == manualReviewWorking
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

func (a *application) taskEditorView(c *ui.Context) {
	s := &a.manual
	locked := s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil
	ui.Row(c).Gap(8).Children(func() {
		heading := "任务"
		if a.cards.destinations {
			heading = "目标"
		}
		ui.Text(c, a.tr(heading)).Bold().Grow(1)
		porcelainIcon(c, "×", a.tr("关闭编辑")).Border(0, ui.Transparent).OnClick(a.closeTaskEditor)
	})
	ui.Scroll(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(8).Padding(12, 16, 12, 0).Children(func() {
			if a.taskError != nil {
				ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error())
				ui.Text(c, a.tr("修复文件或备份问题后重新载入。不会以空列表覆盖。"))
			}
			if issue := a.selectedTaskIssue(); issue != nil {
				ui.Text(c, a.tr("此任务不可运行或编辑: ")+issue.Error())
			}
			if a.cards.destinations {
				key := "每个目标各自同步源，因此同一个文件可以同时进入多个目标。"
				if manualKind(s.kind) == taskconfig.Moving {
					key = "按目标顺序首个匹配；失败不转投后续目标。"
				}
				ui.Text(c, a.tr(key)).TextColor(porcelain.TextMuted)
				porcelainPath(c, s.source)
				ui.Box(c).Height(12)
				a.manualTargetsView(c, locked)
				ui.Text(c, a.render(s.message)).FontSize(12)
				return
			}
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, a.tr("名称"))
				ui.TextInput(c, &s.name).Height(32).Label(a.tr("任务名称")).ReadOnly(locked).OnChange(s.edited)
			})
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, a.tr("源文件夹"))
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c, &s.source).Height(32).Label(a.tr("源文件夹路径")).Grow(1).Placeholder(a.tr("源文件夹绝对路径")).ReadOnly(locked).OnChange(s.edited)
					porcelainButton(c, a.tr("浏览")).Label(a.tr("选择源")).Disabled(locked).OnClick(func() { a.chooseFolder(true) })
				})
			})
			a.manualKindView(c, locked)
			if s.id != "" {
				ui.Text(c, a.tr("已保存任务不能切换类型。")).FontSize(14).Padding(12).FillWidth().Border(1, porcelain.Border)
			}
			ui.Text(c, a.tr("触发器"))
			a.triggerView(c, locked)
			porcelainButton(c, a.tr("同步目标（按顺序执行）")).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(12).TextColor(porcelain.TextMuted).Disabled(s.busy()).OnClick(func() { a.cards.destinations = true })
			if s.dirty || s.phase == manualDraft || s.phase == manualSaving {
				ui.Text(c, a.render(s.message))
			}
		})
	})
	ui.Row(c).FillWidth().Gap(8).Padding(12, 0, 0, 0).Children(func() {
		ui.Box(c).Grow(1)
		if a.cards.destinations {
			porcelainButton(c, a.tr("编辑任务")).Disabled(s.busy()).OnClick(func() { a.cards.destinations = false })
		}
		if s.dirty {
			porcelainButton(c, a.tr("放弃编辑")).Disabled(s.busy()).OnClick(a.discardManual)
		}
		porcelainButton(c, a.tr("关闭编辑")).OnClick(a.closeTaskEditor)
		ui.PrimaryButton(c, a.tr("保存任务")).Padding(5, 10).Disabled(locked || a.taskError != nil).OnClick(a.createManual)
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
	if s.busy() || (automatic && a.batch.phase == batchServing) {
		return
	}
	a.cards.running = id
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
				if !showResult || a.cards.running != id || s.id != id {
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
			if errors.Is(err, context.Canceled) {
				a.batch.clear()
			}
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
	a.batch.clear()
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
	a.batch.clear()
	a.stopWatches(watchesStopped)
	if a.manual.phase == manualReviewing {
		a.discardRegexMigration()
	}
	if a.manual.phase == manualImporting {
		a.discardImportPreview()
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
	a.batch.clear()
	a.stopWatches(watchesStopped)
	if a.manual.phase == manualReviewing {
		a.discardRegexMigration()
	}
	if a.manual.phase == manualImporting {
		a.discardImportPreview()
	}
	if a.manual.busy() || a.languageBusy || a.commonBusy {
		e.PreventDefault()
		a.manual.quitPending = true
		a.stopManual()
		return
	}
	a.lifecycle.beginQuit()
}
