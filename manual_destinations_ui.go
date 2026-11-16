package main

import (
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
	return a.batch.phase == batchIdle && !a.manual.busy() && a.tasks != nil && a.taskError == nil && a.selectedTaskIssue() == nil
}
func (a *application) addManualTarget() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.flushTarget()
	d := manualDestination{ID: taskconfig.NewID(), Name: a.tr("目标 %d", len(s.targets)+1), Filters: allFileRules()}
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
		s.message = messagef("此目标含不支持过滤，不能移除其配置。")
		return
	}
	if len(s.targets) <= 1 {
		s.message = messagef("至少保留一个目标。移除目标不会删除文件。")
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
	for i, d := range s.targets {
		id := d.ID
		ui.Column(c).FillWidth().Gap(8).Border(1, porcelain.Border).Padding(12).Children(func() {
			ui.Row(c).MinHeight(60).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, a.ruleSummary(d.Filters)+"  →  "+d.Name).SingleLine()
					porcelainPath(c, d.Path)
				})
				porcelainIcon(c, "copy", a.tr("复制目标（仅配置）")+" "+d.Name).Border(0, ui.Transparent).Disabled(locked).OnClick(func() { a.copyManualTarget(id) })
				glyph := "♢"
				if id == s.targetID && a.cards.targetHidden != id {
					glyph = "check"
				}
				porcelainIcon(c, glyph, a.tr("编辑或收起目标")+" "+d.Name).Border(0, ui.Transparent).Disabled(s.busy()).OnClick(func() { a.toggleDestinationEditor(id) })
				if manualKind(s.kind) == taskconfig.Moving {
					porcelainButton(c, a.tr("上移")).Disabled(locked || i == 0).OnClick(func() { a.moveManualTarget(id, -1) })
					porcelainButton(c, a.tr("下移")).Disabled(locked || i == len(s.targets)-1).OnClick(func() { a.moveManualTarget(id, 1) })
				}
				porcelainIcon(c, "⌫", a.tr("移除目标（仅配置）")).Border(0, ui.Transparent).Disabled(locked || len(s.targets) <= 1 || s.filterEditor.RunError != "").OnClick(func() { s.selectTarget(id); a.removeManualTarget() })
			})
			if id != s.targetID || a.cards.targetHidden == id {
				return
			}
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			a.selectedTargetEditor(c, locked)
		})
	}
	porcelainButton(c, a.tr("添加目标")).Disabled(locked).OnClick(a.addManualTarget)
}

func (a *application) toggleDestinationEditor(id taskconfig.ID) {
	if a.manual.busy() {
		return
	}
	if a.manual.targetID == id && a.cards.targetHidden != id {
		a.manual.flushTarget()
		a.manual.keepFilterDraft()
		a.cards.targetHidden = id
		return
	}
	a.manual.selectTarget(id)
	a.cards.targetHidden = ""
}
func (a *application) copyManualTarget(id taskconfig.ID) {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.flushTarget()
	s.keepFilterDraft()
	for _, d := range s.targets {
		if d.ID == id {
			d.ID = taskconfig.NewID()
			d.Name = a.tr("%s（副本）", d.Name)
			d.matcher = nil
			d.filterError = nil
			s.targets = append(s.targets, d)
			s.selectTarget(d.ID)
			a.cards.targetHidden = ""
			s.edited()
			return
		}
	}
}

func (a *application) selectedTargetEditor(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Text(c, a.tr("名称"))
	ui.TextInput(c, &s.targetName).Height(32).Label(a.tr("目标名称")).Placeholder(a.tr("目标名称")).ReadOnly(locked).OnChange(s.edited)
	ui.Text(c, a.tr("目标文件夹"))
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c, &s.target).Height(32).Label(a.tr("目标文件夹路径")).Grow(1).Placeholder(a.tr("现有目标文件夹绝对路径")).ReadOnly(locked).OnChange(s.edited)
		porcelainButton(c, a.tr("浏览")).Label(a.tr("选择目标")).Disabled(locked).OnClick(func() { a.chooseFolder(false) })
	})
	ui.Text(c, a.tr("请选择现有目标文件夹；本版本不会创建目标根目录。")).FontSize(14).Padding(12).FillWidth().Border(1, porcelain.Border)
	if manualKind(s.kind) == taskconfig.Moving {
		a.moveOptionsView(c, locked)
	} else {
		a.destinationModeView(c, locked)
	}
	a.filtersView(c, locked || a.selectedMirror())
}
