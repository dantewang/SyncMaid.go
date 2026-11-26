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
	manualReady
	manualFinished
	manualLoading
	manualSaving
	manualImporting
	manualReviewing
	manualReviewWorking
)

type runPhase uint8

const (
	runIdle runPhase = iota
	runRunning
	runStopping
)

// activeRun tracks the single background sync/move execution independently of
// manualSession's configuration draft and modal state.
type activeRun struct {
	phase  runPhase
	id     uint64
	taskID taskconfig.ID
	cancel context.CancelFunc
	status localizedMessage
}

func (r activeRun) active() bool              { return r.phase != runIdle }
func (r activeRun) stopping() bool            { return r.phase == runStopping }
func (r activeRun) owns(id taskconfig.ID) bool { return r.active() && id != "" && r.taskID == id }

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
	progress                syncengine.TransferProgress
	message                 localizedMessage
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
	recycleProbes           map[taskconfig.ID]recycleProbeState
	recycleProbeSeq         uint64
	mirrorPreview           *syncengine.MirrorPreview
	mirrorReply             chan error
	mirrorShowAll           bool
	mirrorPreviewTabDirs    bool
	mirrorThresholdText     string
	mirrorThresholdHelpOpen bool
}

type savedTaskResult struct {
	outcome        appcore.OutcomeState
	started, ended time.Time
	message        localizedMessage
	progress       syncengine.TransferProgress
	destinations   syncengine.MultipleResult
}

// busy reports configuration/dialog/modal work that blocks switching or mutating drafts.
func (s *manualSession) busy() bool {
	return s.phase == manualChoosing || s.phase == manualLoading || s.phase == manualSaving || s.phase == manualImporting || s.phase == manualReviewing || s.phase == manualReviewWorking
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

func (a *application) configBusy() bool                 { return a.manual.busy() }
func (a *application) isRunning() bool                  { return a.run.active() }
func (a *application) isTaskRunning(id taskconfig.ID) bool { return a.run.owns(id) }

func (a *application) canSwitchTask() bool {
	return a.batch.phase == batchIdle && a.sourcePreview.job == nil && !a.configBusy() && !a.manual.dirty && !a.cards.editor && a.tasks != nil && a.taskError == nil
}

func (a *application) canCreateTask() bool                 { return a.canSwitchTask() }
func (a *application) canEditTask(id taskconfig.ID) bool   { return a.canSwitchTask() && !a.isTaskRunning(id) }
func (a *application) canStartRun() bool                   { return a.canSwitchTask() && !a.isRunning() }

func (a *application) taskEditorView(c *ui.Context, modalWidth float32) {
	s := &a.manual
	locked := s.busy() || a.isTaskRunning(s.id) || a.tasks == nil || a.selectedTaskIssue() != nil
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(0, 0, 10, 0).Children(func() {
		heading := "编辑任务"
		if s.id == "" {
			heading = "新建任务"
		}
		ui.Text(c, a.tr(heading)).FontSize(16).Bold().Grow(1)
		porcelainIcon(c, "×", a.tr("关闭编辑")).Border(0, ui.Transparent).OnClick(a.closeTaskEditor)
	})
	ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
	if a.taskError != nil {
		ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error()).FontSize(12).TextColor(porcelain.Danger).Padding(6, 0, 0, 0)
		ui.Text(c, a.tr("修复文件或备份问题后重新载入。不会以空列表覆盖。")).FontSize(12)
	}
	if issue := a.selectedTaskIssue(); issue != nil {
		ui.Text(c, a.tr("此任务不可运行或编辑: ")+issue.Error()).FontSize(12).TextColor(porcelain.Warning).Padding(6, 0, 0, 0)
	}
	sourceOverlap, hasSourceOverlap := a.sourceOverlapMessage()
	porcelainScroll(c, &a.scrolls.taskModal, func() {
		ui.Column(c).FillWidth().Gap(8).Padding(12, 0, 12, 0).Children(func() {
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
				if hasSourceOverlap {
					ui.Text(c, a.render(sourceOverlap)).FontSize(12).TextColor(porcelain.Danger)
				}
			})
			a.manualKindView(c, locked)

			ui.Text(c, a.tr("触发器"))
			a.triggerView(c, locked)
			if s.dirty || s.phase == manualDraft || s.phase == manualSaving {
				ui.Text(c, a.render(s.message)).FontSize(12).TextColor(porcelain.TextMuted)
			}
		})
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
	ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(10, 0, 0, 0).Children(func() {
		if s.id != "" {
			ribbonDangerButton(c, a.tr("删除任务")).Disabled(locked || a.taskError != nil).OnClick(a.confirmDeleteManual)
		}
		ui.Box(c).Grow(1)
		porcelainButton(c, a.tr("取消")).Disabled(s.busy()).OnClick(a.closeTaskEditor)
		ribbonInkButton(c, a.tr("保存任务")).Disabled(locked || a.taskError != nil || hasSourceOverlap).OnClick(a.createManual)
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
	if a.quitPending {
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
	if !a.targetEditAllowed() {
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
			if !source {
				a.triggerRecycleProbe(s.targetID, s.target)
			}
		}
	})
}
func (a *application) startManual() {
	s := &a.manual
	if a.isRunning() || a.configBusy() || a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil || s.dirty || (s.phase != manualReady && s.phase != manualFinished) {
		return
	}
	a.startTaskRun(s.id, s.name, s.request, false)
}
func (a *application) startTaskRun(id taskconfig.ID, name string, q syncengine.ManualTaskRequest, automatic bool) {
	s := &a.manual
	editingSelf := s.id == id && (a.cards.editor || s.dirty || s.phase == manualChoosing)
	if a.isRunning() || a.configBusy() || a.sourcePreview.job != nil || (automatic && a.batch.phase == batchServing) || editingSelf {
		return
	}
	if len(q.Destinations) == 0 {
		if a.results == nil {
			a.results = map[taskconfig.ID]savedTaskResult{}
		}
		msg := messagef("无目标，未执行文件操作。")
		a.results[id] = savedTaskResult{message: msg}
		if s.id == id && !s.dirty && !a.cards.editor {
			s.phase = manualFinished
			s.message = msg
		}
		return
	}
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
	showResult := s.id == id && !s.dirty && !a.cards.editor
	ctx, cancel := context.WithCancel(context.Background())
	a.run.id++
	runID := a.run.id
	a.run = activeRun{
		phase:  runRunning,
		id:     runID,
		taskID: id,
		cancel: cancel,
		status: messagef("%s: 正在运行", name),
	}
	a.watchActivity = a.run.status
	if showResult {
		s.message = messagef("正在运行…")
		s.progress = syncengine.TransferProgress{}
		s.destinationResults = syncengine.InitialDestinationResults(q)
	}
	batchIdx, batchTotal := 0, 0
	if !automatic && a.batch.phase == batchServing && a.batch.total > 1 {
		batchTotal = a.batch.total
		batchIdx = a.batch.total - len(a.batch.pending)
	}
	a.stopLiveTicker()
	tracker := newLiveRunTracker(id, name, q, automatic, batchIdx, batchTotal, time.Now())
	a.liveTracker = tracker
	a.liveSnapshot = tracker.Snapshot(time.Now())
	stopTicker := make(chan struct{})
	a.liveTickerStop = stopTicker
	if a.window != nil {
		go func() {
			ticker := time.NewTicker(150 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopTicker:
					return
				case <-ticker.C:
					a.window.Update(func() {
						if a.liveTracker != tracker || a.run.id != runID {
							return
						}
						a.liveSnapshot = tracker.Snapshot(time.Now())
						if a.manual.id == id && !a.manual.dirty && !a.cards.editor && a.liveSnapshot.CurrentDestID != "" {
							if res, ok := a.liveSnapshot.Results[a.liveSnapshot.CurrentDestID]; ok {
								a.manual.destinationResults[a.liveSnapshot.CurrentDestID] = res
								a.manual.progress = res.Progress
							}
						}
					})
				}
			}
		}()
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
		notify := func(r syncengine.DestinationResult) {
			if !tracker.UpdateDestination(r) {
				return
			}
			if a.window != nil {
				a.window.Update(func() {
					if a.liveTracker != tracker || a.run.id != runID {
						return
					}
					a.liveSnapshot = tracker.Snapshot(time.Now())
					if a.manual.id != id || a.manual.dirty || a.cards.editor {
						return
					}
					a.manual.destinationResults[r.ID] = r
					a.manual.progress = r.Progress
					if r.State != syncengine.DestinationRunning {
						a.manual.progress.Stage = string(r.State)
					}
				})
			}
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
			if a.liveTracker == tracker {
				a.stopLiveTicker()
				tracker.Finish(results, err, recordEnd.Outcome, time.Now())
				a.liveSnapshot = tracker.Snapshot(time.Now())
			}
			if a.run.id != runID {
				return
			}
			a.run = activeRun{phase: runIdle, id: runID}
			if errors.Is(err, context.Canceled) {
				a.batch.clear()
			}
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
			if err != nil {
				slog.Warn("multiple-sync-ended", "error", err, "targets", len(q.Destinations))
			}
			taskExists := id != "" && a.tasks != nil
			if taskExists {
				if _, findErr := a.tasks.Find(id); findErr != nil {
					taskExists = false
				}
			}
			uiMsg := message
			switch {
			case err == nil:
				uiMsg = messagef("全部目标同步完成")
			case errors.Is(err, context.Canceled):
				uiMsg = messagef("任务已停止；已完成目标不回滚。详情: %s", appcore.HistoryText(err.Error(), appcore.HistoryErrorBytes))
			default:
				uiMsg = messagef("任务存在失败或未执行目标: %s", appcore.HistoryText(err.Error(), appcore.HistoryErrorBytes))
			}
			if a.manual.id != id || a.manual.dirty || a.cards.editor || a.configBusy() {
				if taskExists {
					a.results[id] = savedTaskResult{outcome: recordEnd.Outcome, message: uiMsg, destinations: results, started: recordEnd.Started, ended: recordEnd.Ended}
				}
				a.syncWatches()
				a.finishAsyncStep(true)
				return
			}
			a.manual.destinationResults = results
			a.manual.phase = manualFinished
			a.manual.message = uiMsg
			if taskExists {
				a.results[id] = savedTaskResult{outcome: recordEnd.Outcome, message: a.manual.message, progress: a.manual.progress, destinations: a.manual.destinationResults, started: recordEnd.Started, ended: recordEnd.Ended}
			}
			a.syncWatches()
			a.finishAsyncStep(true)
		})
	}()
}
func (a *application) stopManual() {
	a.batch.clear()
	a.stopWatches(watchesPaused)
	if a.run.phase != runRunning {
		return
	}
	a.run.phase = runStopping
	a.run.status = messagef("正在停止并清理临时文件…")
	if a.manual.id == a.run.taskID && !a.manual.dirty && !a.cards.editor {
		a.manual.message = a.run.status
	}
	if a.liveTracker != nil {
		a.liveTracker.MarkStopping(time.Now())
		a.liveSnapshot = a.liveTracker.Snapshot(time.Now())
	}
	if a.run.cancel != nil {
		a.run.cancel()
	}
}
func (a *application) tryPrepareQuit(showStatus bool) bool {
	a.batch.clear()
	a.stopWatches(watchesStopped)
	a.sourcePreview.invalidate()
	if a.sourcePreview.job != nil {
		a.quitPending = true
		return false
	}
	if a.manual.phase == manualReviewing {
		a.discardRegexMigration()
	}
	if a.manual.phase == manualImporting {
		a.discardImportPreview()
	}
	s := &a.manual
	if a.languageBusy || a.commonBusy || a.configBusy() || a.isRunning() {
		a.quitPending = true
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
