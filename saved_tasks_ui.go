package main

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
	"syncmaid/internal/winutil"
)

func (a *application) selectedTaskIssue() error {
	if a.manual.id == "" {
		return nil
	}
	t, err := a.tasks.Find(a.manual.id)
	if err != nil {
		return err
	}
	_, err = appcore.MultipleRequestFromSaved(t)
	return err
}
func (a *application) newManual() {
	if a.manual.busy() || a.manual.dirty || a.tasks == nil || a.taskError != nil {
		return
	}
	a.sourcePreview.invalidate()
	a.manual = manualSession{onEdit: a.manualEdited, trigger: taskconfig.Trigger{Kind: "manual"}, quietText: "10", phase: manualDraft, quitPending: a.manual.quitPending}
}
func (a *application) selectManual(id taskconfig.ID) {
	if a.manual.busy() || a.manual.dirty || a.tasks == nil || a.taskError != nil {
		return
	}
	t, err := a.tasks.Find(id)
	if err != nil {
		a.manual.message = messagef("错误: %s", err.Error())
		return
	}
	a.sourcePreview.invalidate()
	s := manualSession{onEdit: a.manualEdited, trigger: t.Trigger, quietText: "10", kind: t.Kind, phase: manualDraft, id: id, name: t.Name, source: t.SourcePath, quitPending: a.manual.quitPending}
	for _, d := range t.Destinations {
		s.targets = append(s.targets, cloneManualDestination(syncengine.ManualDestination{ID: d.ID, Name: d.Name, Path: d.Target.Path, Filters: d.Filters, Strategy: d.Strategy, DeleteMode: d.DeleteMode, Flatten: d.FlattenStructure, Collision: d.CollisionPolicy}))
	}
	if len(s.targets) > 0 {
		d := s.targets[0]
		s.targetID = d.ID
		s.targetName = d.Name
		s.target = d.Path
		s.loadFilterDraft(d.ID)
	}
	if t.Trigger.SettleSeconds != nil {
		s.quietText = fmt.Sprint(*t.Trigger.SettleSeconds)
	}
	if t.Trigger.Kind == "scheduled" && t.Trigger.CronExpression != nil {
		s.cronText = *t.Trigger.CronExpression
		s.refreshCron()
	}
	s.request, err = a.tasks.Request(t)
	if err != nil {
		s.message = messagef("此任务不可运行或编辑: %s", err.Error())
	} else {
		s.phase = manualReady
		s.message = messagef("已载入，可以运行或编辑。")
		if result, ok := a.results[id]; ok {
			s.phase = manualFinished
			s.message = result.message
			s.progress = result.progress
			s.destinationResults = result.destinations
		}
	}
	a.manual = s
}

func (a *application) reloadTasks() {
	s := &a.manual
	if s.busy() {
		return
	}
	a.sourcePreview.invalidate()
	keepDraft := s.dirty
	id := s.id
	s.phase = manualLoading
	s.message = messagef("正在载入 Go 专用任务文件…")
	data := a.installation.Data
	go func() {
		library, err := appcore.LoadSavedTasks(data)
		history := appcore.LoadRunHistory(data)
		a.window.Update(func() {
			s.phase = manualDraft
			a.taskError = err
			a.history = history
			a.historyError = history.Err
			if history.Err == nil {
				a.historyView = history.Data
			}
			if err != nil {
				s.message = messagef("载入失败，已阻止写入: %s", err.Error())
			} else {
				a.tasks = library
				a.restoreRunResults()
				switch {
				case keepDraft:
					if _, e := library.Find(s.id); s.id != "" && e != nil {
						s.id = ""
					}
					s.message = messagef("已重新载入；未保存编辑仍在，请核对任务列表后再保存。")
				case id != "":
					if _, e := library.Find(id); e == nil {
						a.selectManual(id)
					} else {
						a.newManual()
					}
				case len(library.Tasks) > 0:
					a.selectManual(library.Tasks[0].ID)
				default:
					a.newManual()
				}
			}
			a.syncWatches()
			a.finishAsyncStep(false)
		})
	}()
}

// Save validation and disk I/O are serial background work. On any storage
// failure keep the editor, block another write, and require an explicit reload.
func (a *application) createManual() {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.taskError != nil || a.selectedTaskIssue() != nil {
		return
	}
	if strings.TrimSpace(s.name) == "" {
		s.message = messagef("请输入任务名称")
		return
	}
	if s.triggerError != "" {
		s.message = messagef("触发设置错误: %s", messagef(s.triggerError))
		return
	}
	if err := s.filterDraftError(); err != nil {
		s.message = messagef("过滤尚未保存: %s", err.Error())
		return
	}
	if a.cards.destinations && !a.acceptOpenDestinations() {
		return
	}
	id, name, q := s.id, s.name, s.draftRequest()
	library := a.tasks
	s.phase = manualSaving
	s.message = messagef("检查路径并保存任务…")
	go func() {
		tasks, nextID, validationErr := library.PrepareMultiple(id, name, q)
		var writeErr error
		var next *appcore.SavedTaskLibrary
		if validationErr == nil {
			id = nextID
			snapshot, err := library.Snapshot.Save(tasks)
			writeErr = err
			if err == nil {
				next, writeErr = appcore.SavedLibrary(snapshot)
			}
		}
		a.window.Update(func() { a.finishTaskSave(next, id, validationErr, writeErr) })
	}()
}

func (a *application) finishTaskSave(next *appcore.SavedTaskLibrary, id taskconfig.ID, validationErr, writeErr error) {
	s := &a.manual
	s.phase = manualDraft
	switch {
	case validationErr != nil:
		s.message = messagef("未保存，编辑仍在: %s", validationErr.Error())
	case writeErr != nil:
		// Retain the proposed identity if Save's post-commit check failed,
		// so explicit reload can find it instead of making a duplicate.
		s.id = id
		a.taskError = writeErr
		s.message = messagef("保存失败或结果待核对，编辑仍在。请重新载入: %s", writeErr.Error())
	default:
		a.tasks = next
		s.dirty = false
		a.selectManual(id)
		s.message = messagef("任务已保存，可以运行。")
		a.cards.editor = false
	}
	if validationErr != nil || writeErr != nil {
		a.pauseQuitAfterTaskError()
		return
	}
	a.syncWatches()
	a.finishAsyncStep(false)
}
func (a *application) pauseQuitAfterTaskError() {
	if a.manual.quitPending {
		a.manual.quitPending = false
		a.watchPhase = watchesPaused
		a.manual.message = messagef("%s；退出已暂停，请先核对未保存的编辑。", a.manual.message)
		a.lifecycle.show()
	}
}

func (a *application) confirmDeleteManual() {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.taskError != nil || s.id == "" || a.selectedTaskIssue() != nil {
		return
	}
	if err := a.taskDeletionIssue(); err != nil {
		s.message = messagef("错误: %s", err.Error())
		return
	}
	id, name, previous := s.id, s.name, s.phase
	a.pauseEditingWatch(id)
	s.phase = manualChoosing
	options := mygo.MessageOptions{Parent: a.window, Type: mygo.MessageWarning, Title: a.tr("删除任务"), Message: a.tr("确认删除任务「%s」？", name), Detail: a.tr("只删除任务记录，不删除源或目标文件。未保存编辑也会放弃。"), Buttons: []string{a.tr("取消"), a.tr("删除")}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}}
	go func() {
		result, err := e2eDialogMessage(options)
		a.window.Update(func() {
			s.phase = previous
			if s.quitPending {
				a.finishAsyncStep(false)
				return
			}
			if err != nil {
				s.message = messagef("删除确认失败: %s", err.Error())
				a.syncWatches()
				return
			}
			if result.Button == 1 {
				a.deleteManual(id)
			} else {
				s.message = messagef("已取消删除")
				a.syncWatches()
			}
		})
	}()
}
func (a *application) deleteManual(id taskconfig.ID) {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.taskError != nil || s.id != id || a.selectedTaskIssue() != nil {
		return
	}
	if err := a.taskDeletionIssue(); err != nil {
		s.message = messagef("错误: %s", err.Error())
		return
	}
	library := a.tasks
	s.phase = manualSaving
	s.message = messagef("正在删除任务记录…")
	go func() {
		next, err := library.Remove(id)
		a.window.Update(func() {
			s.phase = manualDraft
			if err != nil {
				a.taskError = err
				s.message = messagef("删除未能确认，请重新载入核对: %s", err.Error())
				a.pauseQuitAfterTaskError()
			} else {
				a.tasks = next
				delete(a.results, id)
				s.dirty = false
				if len(next.Tasks) > 0 {
					a.selectManual(next.Tasks[0].ID)
				} else {
					a.newManual()
				}
				s.message = messagef("任务已删除，源与目标文件保留。")
			}
			a.syncWatches()
			a.finishAsyncStep(false)
		})
	}()
}

func (a *application) taskRecovery() (*taskstore.LoadError, bool) {
	var load *taskstore.LoadError
	ok := errors.As(a.taskError, &load) && load.CanRecover()
	return load, ok
}

func (a *application) taskRecoveryAllowed() bool {
	return !a.manual.busy() && !a.manual.dirty && !a.cards.editor &&
		!a.manual.quitPending && a.sourcePreview.job == nil && !a.hasModalOpen() &&
		a.batch.phase == batchIdle && !a.languageBusy && !a.commonBusy
}

func (a *application) confirmTaskRecovery(backup *taskstore.LoadError) {
	current, ok := a.taskRecovery()
	if !ok || current != backup || !a.taskRecoveryAllowed() {
		return
	}
	s := &a.manual
	previous := s.phase
	s.phase = manualChoosing
	a.recoveryNotice = localizedMessage{}
	options := mygo.MessageOptions{
		Parent: a.window, Type: mygo.MessageWarning,
		Title:   a.tr("恢复任务备份"),
		Message: a.tr("用备份替换当前 Go 任务文件？"),
		Detail:  a.tr("恢复会保留原文件、清除正则确认并暂停本次会话的自动触发。"),
		Buttons: []string{a.tr("取消"), a.tr("恢复")}, DefaultButton: 0, CancelButton: 0,
		DestructiveButtons: []int{1},
	}
	go func() {
		tasks, err := backup.BackupTasks()
		if err == nil {
			err = appcore.UniqueSavedTasks(tasks)
		}
		if err != nil {
			a.window.Update(func() {
				s.phase = previous
				a.recoveryNotice = messagef("备份无法恢复：%s", err.Error())
				a.finishAsyncStep(false)
			})
			return
		}
		result, dialogErr := e2eDialogMessage(options)
		a.window.Update(func() {
			s.phase = previous
			if s.quitPending {
				a.finishAsyncStep(false)
				return
			}
			if dialogErr != nil {
				a.recoveryNotice = messagef("备份无法恢复：%s", dialogErr.Error())
			} else if result.Button == 1 {
				a.restoreTaskBackup(backup)
			}
		})
	}()
}

func (a *application) restoreTaskBackup(backup *taskstore.LoadError) {
	if !a.taskRecoveryAllowed() {
		return
	}
	a.stopWatches(watchesPaused)
	a.manual.phase = manualSaving
	go func() {
		recovered, err := backup.Recover()
		var library *appcore.SavedTaskLibrary
		if err == nil {
			library, err = appcore.SavedLibrary(recovered.Snapshot)
		}
		a.window.Update(func() {
			notice := messagef("任务备份已恢复。")
			if err != nil {
				notice = messagef("恢复未能确认，请重新载入任务核对：%s", err.Error())
				// Suppress repeat recovery from a stale load, even if nothing changed.
				a.taskError = err
			} else {
				a.tasks, a.taskError = library, nil
				a.results = nil
			}
			if recovered.PreservedPath != "" {
				notice = messagef("%s 原文件：%s", notice, winutil.PathLabel(recovered.PreservedPath))
			}
			a.recoveryNotice = notice
			a.manual.phase = manualDraft
			if err == nil {
				a.newManual()
			}
			a.finishAsyncStep(false)
		})
	}()
}
