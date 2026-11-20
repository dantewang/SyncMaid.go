package main

import (
	"context"
	"errors"
	"log/slog"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
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
	request                 syncengine.ManualTaskRequest
	cancel                  context.CancelFunc
	progress                syncengine.TransferProgress
	message                 localizedMessage
	quitPending             bool
	id                      taskconfig.ID
	name                    string
	targets                 []syncengine.ManualDestination
	targetID                taskconfig.ID
	targetName              string
	dirty                   bool
	destinationResults      syncengine.MultipleResult
	filterEditor            appcore.FilterEditor
	filterDrafts            map[taskconfig.ID]appcore.FilterEditor
	destinationEdits        map[taskconfig.ID]destinationEditState
	mirrorPreview           *syncengine.MirrorPreview
	mirrorReply             chan error
}

type savedTaskResult struct {
	outcome        appcore.OutcomeState
	started, ended time.Time
	message        localizedMessage
	progress       syncengine.TransferProgress
	destinations   syncengine.MultipleResult
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
	s.progress = syncengine.TransferProgress{}
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
				if syncengine.ManualKind(s.kind) == taskconfig.Moving {
					ui.Text(c, a.tr("按目标顺序首个匹配；失败不转投后续目标。")).TextColor(porcelain.TextMuted)
				}
				porcelainPath(c, s.source)
				ui.Box(c).Height(12)
				a.manualTargetsView(c, locked)
				a.sourcePreviewView(c)
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

			ui.Text(c, a.tr("触发器"))
			a.triggerView(c, locked)
			if s.dirty || s.phase == manualDraft || s.phase == manualSaving {
				ui.Text(c, a.render(s.message))
			}
		})
	})
	ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Padding(12, 0, 0, 0).Children(func() {
		porcelainButton(c, a.tr("取消")).Disabled(s.busy()).OnClick(a.closeTaskEditor)
		saveLabel := "保存任务"
		if a.cards.destinations {
			saveLabel = "保存"
		}
		ui.PrimaryButton(c, a.tr(saveLabel)).Padding(5, 10).Disabled(locked || a.taskError != nil).OnClick(a.createManual)
	})
}
func runDialog[T any](a *application, prompt func() (T, error), onDone func(T, error)) {
	s := &a.manual
	previous := s.phase
	s.phase = manualChoosing
	go func() {
		result, err := prompt()
		a.window.Update(func() {
			s.phase = previous
			onDone(result, err)
			a.finishAsyncStep(true)
		})
	}()
}

func (a *application) finishAsyncStep(drainWatches bool) {
	if a.manual.quitPending {
		a.requestQuit()
		return
	}
	if drainWatches {
		a.drainWatches()
	}
}

func (a *application) hasModalOpen() bool {
	return a.importPreview != nil || a.regexSession != nil || a.manual.mirrorPreview != nil || a.manual.mirrorReply != nil
}

func (a *application) chooseFolder(source bool) {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil {
		return
	}
	title := a.tr("选择文件夹")
	runDialog(a, func() ([]string, error) {
		return e2eDialogOpen(mygo.OpenDialogOptions{Parent: a.window, Title: title, Directory: true})
	}, func(paths []string, err error) {
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
	})
}
func (a *application) startManual() {
	s := &a.manual
	if a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil || s.dirty || (s.phase != manualReady && s.phase != manualFinished) {
		return
	}
	a.startTaskRun(s.id, s.name, s.request, false)
}
func (a *application) startTaskRun(id taskconfig.ID, name string, q syncengine.ManualTaskRequest, automatic bool) {
	s := &a.manual
	if s.busy() || a.sourcePreview.job != nil || (automatic && a.batch.phase == batchServing) {
		return
	}
	if len(q.Destinations) == 0 {
		if a.results == nil {
			a.results = map[taskconfig.ID]savedTaskResult{}
		}
		msg := messagef("无目标，未执行文件操作。")
		a.results[id] = savedTaskResult{message: msg}
		if s.id == id && !s.dirty {
			s.phase = manualFinished
			s.message = msg
		}
		return
	}
	a.cards.running = id
	a.sourcePreview.invalidate()
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
		s.progress = syncengine.TransferProgress{}
		s.destinationResults = syncengine.InitialDestinationResults(q)
	}
	history := a.history
	active := []taskconfig.ID(nil)
	var taskSnapshot []taskconfig.Task
	if a.tasks != nil {
		taskSnapshot = a.tasks.Tasks
		active = taskIDs(taskSnapshot)
	}
	recordStart, recordStartErr := appcore.NewRunRecord(id, name, q, automatic, time.Now())
	go func() {
		last := time.Time{}
		var lastID taskconfig.ID
		var lastState syncengine.DestinationState
		var lastStage string
		notify := func(r syncengine.DestinationResult) {
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
				if r.State != syncengine.DestinationRunning {
					s.progress.Stage = string(r.State)
				}
			})
		}
		historyView, recordEnd, results, err, historyErr := appcore.ExecuteRecordedRun(history, recordStart, recordStartErr, active, func() (syncengine.MultipleResult, error) {
			var results syncengine.MultipleResult
			err := syncengine.InterruptibleIO(ctx, func(ctx context.Context) error {
				if err := syncengine.ValidateCrossTaskPaths(ctx, id, q, taskSnapshot); err != nil {
					return err
				}
				var e error
				results, e = syncengine.RunMultipleConfirmed(ctx, q, notify, a.confirmMirror, syncengine.RecycleWithShell(mygo.Shell.TrashItem))
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
				message = messagef("运行未完成: %s", appcore.HistoryText(err.Error(), appcore.HistoryErrorBytes))
			}
			a.watchActivity = messagef("%s: %s", name, message)
			if w := a.watches[id]; w != nil && w.ticket.phase == watchArmed {
				w.status = message
			}
			if !showResult {
				a.results[id] = savedTaskResult{outcome: recordEnd.Outcome, message: message, destinations: results, started: recordEnd.Started, ended: recordEnd.Ended}
				s.phase = previous
				a.finishAsyncStep(true)
				return
			}
			s.destinationResults = results
			s.phase = manualFinished
			switch {
			case err == nil:
				s.message = messagef("全部目标同步完成")
			case errors.Is(err, context.Canceled):
				s.message = messagef("任务已停止；已完成目标不回滚。详情: %s", appcore.HistoryText(err.Error(), appcore.HistoryErrorBytes))
			default:
				s.message = messagef("任务存在失败或未执行目标: %s", appcore.HistoryText(err.Error(), appcore.HistoryErrorBytes))
			}
			if err != nil {
				slog.Warn("multiple-sync-ended", "error", err, "targets", len(q.Destinations))
			}
			if id != "" {
				a.results[id] = savedTaskResult{outcome: recordEnd.Outcome, message: s.message, progress: s.progress, destinations: s.destinationResults, started: recordEnd.Started, ended: recordEnd.Ended}
			}
			a.finishAsyncStep(true)
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
func (a *application) tryPrepareQuit(showStatus bool) bool {
	a.batch.clear()
	a.stopWatches(watchesStopped)
	a.sourcePreview.invalidate()
	if a.sourcePreview.job != nil {
		a.manual.quitPending = true
		return false
	}
	if a.manual.phase == manualReviewing {
		a.discardRegexMigration()
	}
	if a.manual.phase == manualImporting {
		a.discardImportPreview()
	}
	s := &a.manual
	if a.languageBusy || a.commonBusy || s.busy() {
		s.quitPending = true
		if showStatus {
			if s.phase == manualChoosing {
				s.message = messagef("请关闭当前对话框以退出。")
			}
			if s.phase == manualLoading || s.phase == manualSaving {
				s.message = messagef("文件操作结束后退出…")
			}
		}
		a.stopManual()
		return false
	}
	return true
}
func (a *application) requestQuit() {
	if a.tryPrepareQuit(true) {
		a.lifecycle.exit()
	}
}
func (a *application) beforeQuit(e *mygo.QuitEvent) {
	if !a.tryPrepareQuit(false) {
		e.PreventDefault()
		return
	}
	a.lifecycle.beginQuit()
}
