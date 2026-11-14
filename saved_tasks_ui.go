package main

import (
	"context"
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
	_, err = requestFromSaved(t)
	return err
}
func (a *application) newManual() {
	if a.manual.busy() || a.tasks == nil || a.taskError != nil {
		return
	}
	a.manual = manualSession{phase: manualDraft, quitPending: a.manual.quitPending, message: "请输入名称与路径，然后保存任务。"}
}
func (a *application) selectManual(id taskconfig.ID) {
	if a.manual.busy() || a.tasks == nil || a.taskError != nil {
		return
	}
	t, err := a.tasks.find(id)
	if err != nil {
		a.manual.message = err.Error()
		return
	}
	s := manualSession{phase: manualDraft, id: id, name: t.Name, source: t.SourcePath, quitPending: a.manual.quitPending}
	if len(t.Destinations) == 1 {
		s.target = t.Destinations[0].Target.Path
	}
	s.request, err = requestFromSaved(t)
	if err != nil {
		s.message = "此任务不可运行或编辑: " + err.Error()
	} else {
		s.phase = manualReady
		s.message = "已载入，可以运行或编辑。"
		if result, ok := a.results[id]; ok {
			s.phase = manualFinished
			s.message = result.message
			s.progress = result.progress
		}
	}
	a.manual = s
}

func (a *application) reloadTasks() {
	s := &a.manual
	if s.busy() {
		return
	}
	keepDraft := s.phase == manualDraft && (s.name != "" || s.source != "" || s.target != "")
	id := s.id
	s.phase = manualLoading
	s.message = "正在载入 Go 专用任务文件…"
	data := a.installation.data
	go func() {
		library, err := loadSavedTasks(data)
		a.window.Update(func() {
			s.phase = manualDraft
			a.taskError = err
			if err != nil {
				s.message = "载入失败，已阻止写入: " + err.Error()
			} else {
				a.tasks = library
				a.results = nil
				switch {
				case keepDraft:
					if _, e := library.find(s.id); s.id != "" && e != nil {
						s.id = ""
					}
					s.message = "已重新载入；未保存编辑仍在，请核对任务列表后再保存。"
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
			if s.quitPending {
				a.requestQuit()
			}
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
		s.message = "请输入任务名称"
		return
	}
	id, name, q := s.id, s.name, manualRequest{Source: s.source, Target: s.target}
	library := a.tasks
	s.phase = manualSaving
	s.message = "检查路径并保存任务…"
	go func() {
		_, validationErr := validateManual(context.Background(), q)
		var writeErr error
		var next *savedTaskLibrary
		if validationErr == nil {
			next, id, writeErr = library.save(id, name, q)
		}
		a.window.Update(func() { a.finishTaskSave(next, id, validationErr, writeErr) })
	}()
}

func (a *application) finishTaskSave(next *savedTaskLibrary, id taskconfig.ID, validationErr, writeErr error) {
	s := &a.manual
	s.phase = manualDraft
	switch {
	case validationErr != nil:
		s.message = "未保存，编辑仍在: " + validationErr.Error()
	case writeErr != nil:
		// Retain the proposed identity if Save's post-commit check failed,
		// so explicit reload can find it instead of making a duplicate.
		s.id = id
		a.taskError = writeErr
		s.message = "保存失败或结果待核对，编辑仍在。请重新载入: " + writeErr.Error()
	default:
		a.tasks = next
		delete(a.results, id)
		a.selectManual(id)
		s.message = "任务已保存，可以运行。"
	}
	if validationErr != nil || writeErr != nil {
		a.pauseQuitAfterTaskError()
		return
	}
	if s.quitPending {
		a.requestQuit()
	}
}
func (a *application) pauseQuitAfterTaskError() {
	if a.manual.quitPending {
		a.manual.quitPending = false
		a.manual.message += "；退出已暂停，请先核对未保存的编辑。"
		a.lifecycle.show()
	}
}

func (a *application) confirmDeleteManual() {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.taskError != nil || s.id == "" || a.selectedTaskIssue() != nil {
		return
	}
	id, name, previous := s.id, s.name, s.phase
	s.phase = manualChoosing
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.window, Type: mygo.MessageWarning, Title: "删除任务", Message: "确认删除任务「" + name + "」？", Detail: "只删除任务记录，不删除源或目标文件。未保存编辑也会放弃。", Buttons: []string{"取消", "删除"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
		a.window.Update(func() {
			s.phase = previous
			if s.quitPending {
				a.requestQuit()
				return
			}
			if err != nil {
				s.message = "删除确认失败: " + err.Error()
				return
			}
			if result.Button == 1 {
				a.deleteManual(id)
			} else {
				s.message = "已取消删除"
			}
		})
	}()
}
func (a *application) deleteManual(id taskconfig.ID) {
	s := &a.manual
	if s.busy() || a.tasks == nil || a.taskError != nil || s.id != id || a.selectedTaskIssue() != nil {
		return
	}
	library := a.tasks
	s.phase = manualSaving
	s.message = "正在删除任务记录…"
	go func() {
		next, err := library.remove(id)
		a.window.Update(func() {
			s.phase = manualDraft
			if err != nil {
				a.taskError = err
				s.message = "删除未能确认，请重新载入核对: " + err.Error()
				a.pauseQuitAfterTaskError()
			} else {
				a.tasks = next
				delete(a.results, id)
				if len(next.tasks) > 0 {
					a.selectManual(next.tasks[0].ID)
				} else {
					a.newManual()
				}
				s.message = "任务已删除，源与目标文件保留。"
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
