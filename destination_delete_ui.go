package main

import "github.com/egoist/mygo"

func (a *application) confirmRemoveManualTarget() {
	if !a.targetEditAllowed() || a.manual.targetID == "" {
		return
	}
	s := &a.manual
	id, previous := s.targetID, s.phase
	s.phase = manualChoosing
	options := mygo.MessageOptions{Parent: a.window, Type: mygo.MessageWarning, Title: a.tr("移除目标（仅配置）"), Message: a.tr("确认移除目标「%s」？", s.targetName), Detail: a.tr("只移除此目标配置，不删除文件；其他目标的编辑保留。"), Buttons: []string{a.tr("取消"), a.tr("删除")}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}}
	go func() {
		answer, err := mygo.Dialog.Message(options)
		a.window.Update(func() {
			s.phase = previous
			if !s.quitPending && err == nil && answer.Button == 1 && s.targetID == id {
				a.removeManualTarget()
			}
			if err != nil {
				s.message = messagef("错误: %s", err.Error())
			}
			if s.quitPending {
				a.requestQuit()
			} else {
				a.drainWatches()
			}
		})
	}()
}
