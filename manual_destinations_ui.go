package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/syncengine"
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
func (s *manualSession) draftRequest() syncengine.ManualTaskRequest {
	return syncengine.ManualTaskRequest{Kind: syncengine.ManualKind(s.kind), Trigger: s.trigger, Source: s.source, Destinations: append([]syncengine.ManualDestination(nil), s.targets...)}
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
	d := syncengine.ManualDestination{ID: taskconfig.NewID(), Name: a.tr("目标 %d", len(s.targets)+1), Filters: syncengine.AllFileRules()}
	if syncengine.ManualKind(s.kind) == taskconfig.Moving {
		d.Strategy = taskconfig.Move
		d.Collision = taskconfig.Skip
	}
	s.targets = append(s.targets, d)
	s.beginDestinationEdit(d.ID, nil)
	s.selectTarget(d.ID)
	a.cards.targetHidden = ""
	s.edited()
}
func (a *application) removeManualTarget() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	for _, d := range s.targets {
		if d.ID == s.targetID && syncengine.RulesHaveUnknownFields(d.Filters) {
			s.message = messagef("此目标含不支持过滤，不能移除其配置。")
			return
		}
	}
	for i, d := range s.targets {
		if d.ID == s.targetID {
			delete(s.filterDrafts, d.ID)
			delete(s.destinationEdits, d.ID)
			s.targets = append(s.targets[:i], s.targets[i+1:]...)
			s.targetID, s.targetName, s.target = "", "", ""
			if len(s.targets) > 0 {
				s.selectTarget(s.targets[0].ID)
			}
			a.cards.targetHidden = s.targetID
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
			if syncengine.ManualKind(s.kind) == taskconfig.Moving && (syncengine.IsAllFileRules(d.Filters) || syncengine.IsAllFileRules(s.targets[j].Filters)) {
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
		if _, err := a.tasks.Find(id); err == nil {
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
					ui.Text(c, d.Name).SingleLine().Ellipsis("…").Tooltip(d.Name).FontWeight(600)
					ui.Text(c, a.filterSummary(d.Filters)).FontSize(12).TextColor(porcelain.TextMuted).Tooltip(a.ruleSummary(d.Filters))
					porcelainPath(c, d.Path)
				})
				porcelainIcon(c, "copy", a.tr("复制目标（仅配置）")+" "+d.Name).Border(0, ui.Transparent).Disabled(locked).OnClick(func() { a.copyManualTarget(id) })
				glyph := "♢"
				label := a.tr("编辑或收起目标") + " " + d.Name
				if id == s.targetID && a.cards.targetHidden != id {
					glyph = "check"
					label = a.tr("应用修改") + " " + d.Name
				}
				porcelainIcon(c, glyph, label).Border(0, ui.Transparent).Disabled(locked).OnClick(func() { a.toggleDestinationEditor(id) })
				if id == s.targetID && a.cards.targetHidden != id {
					porcelainIcon(c, "×", a.tr("取消目标编辑")+" "+d.Name).Border(0, ui.Transparent).Disabled(locked).OnClick(func() { a.cancelDestination(id) })
				}
				porcelainIcon(c, "up", a.tr("上移")).Border(0, ui.Transparent).Disabled(locked || !s.canMoveTarget(i, -1)).OnClick(func() { a.moveManualTarget(id, -1) })
				porcelainIcon(c, "⌄", a.tr("下移")).Border(0, ui.Transparent).Disabled(locked || !s.canMoveTarget(i, 1)).OnClick(func() { a.moveManualTarget(id, 1) })
				porcelainIcon(c, "⌫", a.tr("移除目标（仅配置）")).Border(0, ui.Transparent).Disabled(locked).OnClick(func() { s.selectTarget(id); a.confirmRemoveManualTarget() })
			})
			a.sourcePreviewTargetView(c, id)
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
		a.acceptDestination(id)
		return
	}
	a.manual.selectTarget(id)
	for _, d := range a.manual.targets {
		if d.ID == id {
			a.manual.beginDestinationEdit(id, &d)
		}
	}
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
			rules := make([]taskconfig.Rule, len(d.Filters))
			for i, rule := range d.Filters {
				rules[i] = rule.Clone()
			}
			d.Filters = rules
			d.Name = a.tr("%s（副本）", d.Name)
			d.Matcher = nil
			d.FilterError = nil
			s.targets = append(s.targets, d)
			s.beginDestinationEdit(d.ID, nil)
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

	if syncengine.ManualKind(s.kind) == taskconfig.Moving {
		a.moveOptionsView(c, locked)
	} else {
		a.destinationModeView(c, locked)
	}
	a.filtersView(c, locked || a.selectedMirror())
}
