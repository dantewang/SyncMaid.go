package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"syncmaid/internal/taskconfig"
)

func (a *application) selectedTaskIssue() error {
	if a.manual.id == "" {
		return nil
	}
	t, err := a.tasks.find(a.manual.id)
	if err != nil {
		return err
	}
	_, err = multipleRequestFromSaved(t)
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
	t, err := a.tasks.find(id)
	if err != nil {
		a.manual.message = messagef("错误: %s", err.Error())
		return
	}
	a.sourcePreview.invalidate()
	s := manualSession{onEdit: a.manualEdited, trigger: t.Trigger, quietText: "10", kind: t.Kind, phase: manualDraft, id: id, name: t.Name, source: t.SourcePath, quitPending: a.manual.quitPending}
	for _, d := range t.Destinations {
		s.targets = append(s.targets, cloneManualDestination(manualDestination{ID: d.ID, Name: d.Name, Path: d.Target.Path, Filters: d.Filters, Strategy: d.Strategy, DeleteMode: d.DeleteMode, Flatten: d.FlattenStructure, Collision: d.CollisionPolicy}))
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
	s.request, err = a.tasks.request(t)
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
	data := a.installation.data
	go func() {
		library, err := loadSavedTasks(data)
		history := loadRunHistory(data)
		a.window.Update(func() {
			s.phase = manualDraft
			a.taskError = err
			a.history = history
			a.historyError = history.err
			if history.err == nil {
				a.historyView = history.data
			}
			if err != nil {
				s.message = messagef("载入失败，已阻止写入: %s", err.Error())
			} else {
				a.tasks = library
				a.restoreRunResults()
				switch {
				case keepDraft:
					if _, e := library.find(s.id); s.id != "" && e != nil {
						s.id = ""
					}
					s.message = messagef("已重新载入；未保存编辑仍在，请核对任务列表后再保存。")
				case id != "":
					if _, e := library.find(id); e == nil {
						a.selectManual(id)
					} else {
						a.newManual()
					}
				case len(library.tasks) > 0:
					a.selectManual(library.tasks[0].ID)
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
		tasks, nextID, validationErr := library.prepareMultiple(id, name, q)
		var writeErr error
		var next *savedTaskLibrary
		if validationErr == nil {
			id = nextID
			snapshot, err := library.snapshot.Save(tasks)
			writeErr = err
			if err == nil {
				next, writeErr = savedLibrary(snapshot)
			}
		}
		a.window.Update(func() { a.finishTaskSave(next, id, validationErr, writeErr) })
	}()
}

func (a *application) finishTaskSave(next *savedTaskLibrary, id taskconfig.ID, validationErr, writeErr error) {
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
		result, err := mygo.Dialog.Message(options)
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
		next, err := library.remove(id)
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
				if len(next.tasks) > 0 {
					a.selectManual(next.tasks[0].ID)
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
