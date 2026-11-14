package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func (s *manualSession) flushTarget() {
	for i := range s.targets {
		if s.targets[i].ID == s.targetID {
			s.targets[i].Name = s.targetName
			s.targets[i].Path = s.target
			return
		}
	}
}
func (s *manualSession) draftRequest() manualTaskRequest {
	return manualTaskRequest{Kind: manualKind(s.kind), Trigger: s.trigger, Source: s.source, Destinations: append([]manualDestination(nil), s.targets...)}
}
func (s *manualSession) selectTarget(id taskconfig.ID) {
	if s.busy() {
		return
	}
	s.flushTarget()
	s.keepFilterDraft()
	for _, d := range s.targets {
		if d.ID == id {
			s.targetID = id
			s.targetName = d.Name
			s.target = d.Path
			s.loadFilterDraft(id)
			return
		}
	}
}
func (a *application) targetEditAllowed() bool {
	return !a.manual.busy() && a.tasks != nil && a.taskError == nil && a.selectedTaskIssue() == nil
}
func (a *application) addManualTarget() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.flushTarget()
	d := manualDestination{ID: taskconfig.NewID(), Name: fmt.Sprintf("目标 %d", len(s.targets)+1), Filters: allFileRules()}
	if manualKind(s.kind) == taskconfig.Moving {
		d.Strategy = taskconfig.Move
		d.Collision = taskconfig.Skip
	}
	s.targets = append(s.targets, d)
	s.selectTarget(d.ID)
	s.edited()
}
func (a *application) removeManualTarget() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	if s.filterEditor.RunError != "" {
		s.message = "此目标含不支持过滤，不能移除其配置。"
		return
	}
	if len(s.targets) <= 1 {
		s.message = "至少保留一个目标。移除目标不会删除文件。"
		return
	}
	for i, d := range s.targets {
		if d.ID == s.targetID {
			delete(s.filterDrafts, d.ID)
			s.targets = append(s.targets[:i], s.targets[i+1:]...)
			first := s.targets[0]
			s.targetID = first.ID
			s.targetName = first.Name
			s.target = first.Path
			s.loadFilterDraft(first.ID)
			s.edited()
			return
		}
	}
}
func (a *application) moveManualTarget(id taskconfig.ID, delta int) {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.flushTarget()
	for i, d := range s.targets {
		if d.ID == id {
			j := i + delta
			if j < 0 || j >= len(s.targets) {
				return
			}
			s.targets[i], s.targets[j] = s.targets[j], s.targets[i]
			s.edited()
			return
		}
	}
}
func (a *application) discardManual() {
	if a.manual.busy() || a.tasks == nil || a.taskError != nil {
		return
	}
	id := a.manual.id
	a.manual.dirty = false
	if id != "" {
		if _, err := a.tasks.find(id); err == nil {
			a.selectManual(id)
			a.syncWatches()
			return
		}
	}
	a.newManual()
	a.syncWatches()
}

func (a *application) manualTargetsView(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Text(c, "同步目标（按顺序执行）")
	ui.Scroll(c).Height(115).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			for i, d := range s.targets {
				id := d.ID
				label := "目标 " + d.Name
				if id == s.targetID {
					label = "● " + label
				}
				if r, ok := s.destinationResults[id]; ok {
					label += " · " + string(r.State)
				}
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					ui.Button(c, label).Grow(1).Disabled(s.busy()).OnClick(func() { s.selectTarget(id) })
					ui.Button(c, "上移").Label("上移 " + d.Name).Disabled(locked || i == 0).OnClick(func() { a.moveManualTarget(id, -1) })
					ui.Button(c, "下移").Label("下移 " + d.Name).Disabled(locked || i == len(s.targets)-1).OnClick(func() { a.moveManualTarget(id, 1) })
				})
				if r, ok := s.destinationResults[id]; ok {
					ui.Text(c, fmt.Sprintf("%s: %s · 原始文件 %d · 匹配 %d · 已发布 %d · 未变化 %d · 已移除 %d · 移动 %d · 保留 %d · 失败 %d · 源未匹配 %d", r.Name, r.State, r.Progress.SourceFiles, r.Progress.Total, r.Progress.Copied, r.Progress.Unchanged, r.Progress.Removed, r.Progress.Moved, r.Progress.Retained, r.Progress.Failed, r.Progress.Unmatched)).FontSize(12)
					if r.Error != "" {
						ui.Text(c, r.Error).FontSize(12)
					}
				}
			}
		})
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "添加目标").Disabled(locked).OnClick(a.addManualTarget)
		ui.Button(c, "移除目标（仅配置）").Disabled(locked || len(s.targets) <= 1 || s.filterEditor.RunError != "").OnClick(a.removeManualTarget)
	})
	ui.Text(c, "移除只修改配置，不删除文件；保存后生效。").FontSize(12)
	ui.TextInput(c, &s.targetName).Label("目标名称").Placeholder("目标名称").ReadOnly(locked).OnChange(s.edited)
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c, &s.target).Label("目标文件夹路径").Grow(1).Placeholder("现有目标文件夹绝对路径").ReadOnly(locked).OnChange(s.edited)
		ui.Button(c, "选择目标").Disabled(locked).OnClick(func() { a.chooseFolder(false) })
	})
	if manualKind(s.kind) == taskconfig.Moving {
		a.moveOptionsView(c, locked)
	} else {
		a.destinationModeView(c, locked)
	}
	a.filtersView(c, locked || a.selectedMirror())
}
