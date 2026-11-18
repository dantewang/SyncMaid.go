package main

import (
	"errors"

	"github.com/egoist/mygo"
	"syncmaid/internal/taskstore"
)

func (a *application) taskRecovery() (*taskstore.LoadError, bool) {
	var load *taskstore.LoadError
	ok := errors.As(a.taskError, &load) && load.CanRecover()
	return load, ok
}

func (a *application) taskRecoveryAllowed() bool {
	return !a.manual.busy() && !a.manual.dirty && !a.cards.editor &&
		!a.manual.quitPending && a.sourcePreview.job == nil &&
		a.manual.mirrorPreview == nil && a.manual.mirrorReply == nil &&
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
			err = uniqueSavedTasks(tasks)
		}
		if err != nil {
			a.window.Update(func() {
				s.phase = previous
				a.recoveryNotice = messagef("备份无法恢复：%s", err.Error())
				if s.quitPending {
					a.requestQuit()
				}
			})
			return
		}
		result, dialogErr := mygo.Dialog.Message(options)
		a.window.Update(func() {
			s.phase = previous
			if s.quitPending {
				a.requestQuit()
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
		var library *savedTaskLibrary
		if err == nil {
			library, err = savedLibrary(recovered.Snapshot)
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
				notice = messagef("%s 原文件：%s", notice, pathLabel(recovered.PreservedPath))
			}
			a.recoveryNotice = notice
			a.manual.phase = manualDraft
			if err == nil {
				a.newManual()
			}
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
