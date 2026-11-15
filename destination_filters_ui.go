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
			s.filterEditor = projectFilterEditor(d.Filters)
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
func (a *application) filterEdited() {
	s := &a.manual
	if !a.targetEditAllowed() || a.selectedMirror() || s.filterEditor.Mode == filterReadOnly {
		return
	}
	rules, err := s.filterEditor.rules()
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
func (a *application) setFilterMode(mode filterMode) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode == filterReadOnly || a.manual.filterEditor.Mode == mode {
		return
	}
	a.manual.filterEditor.Mode = mode
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
	return filterDeletionIssue(t)
}

func (a *application) filtersView(c *ui.Context, locked bool) {
	e := &a.manual.filterEditor
	ui.Text(c, a.tr("此目标的文件过滤")).Bold()
	readOnly := e.Mode == filterReadOnly
	ui.Row(c).Gap(6).Children(func() {
		for _, item := range []struct {
			label string
			mode  filterMode
		}{{"全部文件", filterAll}, {"无匹配", filterNone}, {"包含/排除", filterCustom}} {
			mode := item.mode
			ui.Button(c, a.tr(item.label)).Selected(e.Mode == mode).Disabled(locked || readOnly).OnClick(func() { a.setFilterMode(mode) })
		}
	})
	if readOnly {
		ui.Text(c, a.tr("复杂或不支持的规则已原样保留，只读。"))
		if a.tasks != nil {
			if bundle, ok := a.tasks.restored[a.manual.id][a.manual.targetID]; ok {
				ui.Text(c, a.tr("已有正则确认记录；执行仍会检查全部规则、绑定及版本。"))
				if bundle.err != nil {
					ui.Text(c, a.tr("确认无效，需重新预览：")+bundle.err.Error())
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
	if e.Mode == filterCustom {
		ui.Text(c, a.tr("每行一项，忽略空行和首尾空白。包含任一条件命中即可；包含全空表示全部，再排除任一命中项。")).FontSize(12)
		ui.Text(c, a.tr("路径相对源目录（含子目录）；扩展名如 txt；通配符支持 *、?、**。不支持正则输入。")).FontSize(12)
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			for _, column := range []struct {
				title                    string
				paths, extensions, globs *string
			}{{"包含", &e.IncludePaths, &e.IncludeExtensions, &e.IncludeGlobs}, {"排除", &e.ExcludePaths, &e.ExcludeExtensions, &e.ExcludeGlobs}} {
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					for _, field := range []struct {
						label string
						value *string
					}{{column.title + "路径前缀", column.paths}, {column.title + "扩展名", column.extensions}, {column.title + "通配符", column.globs}} {
						ui.Text(c, a.tr(field.label)).FontSize(12)
						ui.TextArea(c, field.value).Label(a.tr(field.label)).MinHeight(45).Height(55).MaxLength(8192).ReadOnly(locked).OnChange(a.filterEdited)
					}
				})
			}
		})
	}
	if e.Error != "" {
		ui.Text(c, a.tr("过滤错误: ")+e.Error)
	}
}
