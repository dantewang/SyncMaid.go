package main

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func (s *manualSession) keepFilterDraft() {
	if s.targetID == "" {
		return
	}
	if s.filterDrafts == nil {
		s.filterDrafts = map[taskconfig.ID]filterEditor{}
	}
	s.filterDrafts[s.targetID] = s.filterEditor
}
func (s *manualSession) loadFilterDraft(id taskconfig.ID) {
	if e, ok := s.filterDrafts[id]; ok {
		s.filterEditor = e
		return
	}
	for _, d := range s.targets {
		if d.ID == id {
			s.filterEditor = projectNativeFilter(d.Filters)
			s.keepFilterDraft()
			return
		}
	}
}
func (s *manualSession) filterDraftError() error {
	for _, d := range s.targets {
		if e, ok := s.filterDrafts[d.ID]; ok && e.Error != "" {
			return fmt.Errorf("%s: %s", d.Name, e.Error)
		}
	}
	return nil
}
func (a *application) commitFilterDraft(rules []taskconfig.Rule, err error) {
	s := &a.manual
	s.filterEditor.Error = ""
	if err != nil {
		s.filterEditor.Error = err.Error()
	} else {
		for i := range s.targets {
			if s.targets[i].ID == s.targetID {
				s.targets[i].Filters = rules
				break
			}
		}
	}
	s.keepFilterDraft()
	s.edited()
}
func (a *application) filterEdited() {
	s := &a.manual
	if !a.targetEditAllowed() || a.selectedMirror() || s.filterEditor.Mode == filterReadOnly {
		return
	}
	rules, err := s.filterEditor.rules()
	a.commitFilterDraft(rules, err)
}
func (a *application) setFilterMode(mode filterMode) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode == filterReadOnly || a.manual.filterEditor.Mode == mode {
		return
	}
	a.manual.filterEditor.Mode = mode
	if mode == filterGrouped && len(a.manual.filterEditor.Groups) == 0 {
		a.manual.filterEditor.Groups = []filterDraftGroup{{}}
	}
	a.filterEdited()
}
func (a *application) taskDeletionIssue() error {
	if a.manual.id == "" {
		return errors.New("未选择任务")
	}
	t, err := a.tasks.find(a.manual.id)
	if err != nil {
		return err
	}
	return taskConfigurationDeletionIssue(t)
}

func (a *application) filtersView(c *ui.Context, locked bool) {
	e := &a.manual.filterEditor
	ui.Text(c, a.tr("此目标的文件过滤")).FontSize(16).FontWeight(500)
	readOnly := e.Mode == filterReadOnly
	ui.Row(c).Gap(0).Children(func() {
		for _, item := range []struct {
			label string
			mode  filterMode
		}{{"所有文件", filterAll}, {"无匹配", filterNone}, {"仅匹配", filterGrouped}} {
			mode := item.mode
			glyph := ""
			if mode == filterGrouped {
				glyph = "filter"
			}
			porcelainSegment(c, a.tr(item.label), e.Mode == mode, glyph).Disabled(locked || readOnly).OnClick(func() { a.setFilterMode(mode) })
		}
	})
	if e.Mode == filterGrouped {
		a.filterGroupsView(c, locked)
	}
	if readOnly {
		ui.Text(c, a.tr("复杂或不支持的规则已原样保留，只读。"))
		ui.Text(c, a.tr("旧正则迁移")).Tooltip(a.tr("更多操作"))
		if a.tasks != nil {
			if bundle, ok := a.tasks.restored[a.manual.id][a.manual.targetID]; ok {
				ui.Text(c, a.tr("已有正则确认记录；执行仍会检查全部规则、绑定及版本。"))
				if bundle.Err != nil {
					ui.Text(c, a.tr("确认无效，需重新预览：")+bundle.Err.Error())
				}
				return
			}
		}
		ui.Text(c, a.tr("诊断: ")+e.Reason)
		return
	}
	if e.Mode == filterAll {
		ui.Text(c, a.tr("此目标接收所有源文件。")).FontSize(12)
	}
	if e.Mode == filterNone {
		ui.Text(c, a.tr("此目标不接收文件；不会删除任何已有文件。")).FontSize(12)
	}

	if e.Error != "" {
		ui.Text(c, a.tr("过滤错误: ")+a.tr(e.Error))
	}
}
