package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/windowsfs"
	"syncmaid/internal/winutil"
)

type recycleProbeState struct {
	path        string
	unsupported bool
	known       bool
	token       uint64
}

var probeRecycleSupport = defaultProbeRecycleSupport

func defaultProbeRecycleSupport(dirPath string) (unsupported, known bool) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" || !filepath.IsAbs(dirPath) {
		return false, false
	}
	d, err := winutil.OpenDirectoryAccess(dirPath, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		if windowsfs.Network(dirPath) {
			return true, true
		}
		return false, false
	}
	unsupported = d.Network || d.Removable || d.Fat
	_ = d.Close()
	return unsupported, true
}

func (a *application) triggerRecycleProbe(destID taskconfig.ID, targetPath string) {
	targetPath = strings.TrimSpace(targetPath)
	if destID == "" {
		return
	}
	if targetPath == "" {
		delete(a.manual.recycleProbes, destID)
		return
	}
	if prev, ok := a.manual.recycleProbes[destID]; ok && prev.known && prev.path == targetPath {
		return
	}
	if a.manual.recycleProbes == nil {
		a.manual.recycleProbes = map[taskconfig.ID]recycleProbeState{}
	}
	a.manual.recycleProbeSeq++
	token := a.manual.recycleProbeSeq
	a.manual.recycleProbes[destID] = recycleProbeState{path: targetPath, token: token}
	if a.window == nil {
		unsupported, known := probeRecycleSupport(targetPath)
		a.manual.recycleProbes[destID] = recycleProbeState{path: targetPath, unsupported: unsupported, known: known, token: token}
		return
	}
	win := a.window
	go func() {
		unsupported, known := probeRecycleSupport(targetPath)
		win.Update(func() {
			cur, ok := a.manual.recycleProbes[destID]
			if !ok || cur.token != token || cur.path != targetPath {
				return
			}
			a.manual.recycleProbes[destID] = recycleProbeState{path: targetPath, unsupported: unsupported, known: known, token: token}
		})
	}()
}

func (a *application) destinationRecycleUnsupported(d syncengine.ManualDestination) bool {
	p := strings.TrimSpace(d.Path)
	if p == "" {
		return false
	}
	if windowsfs.Network(p) {
		return true
	}
	st, ok := a.manual.recycleProbes[d.ID]
	return ok && st.known && st.path == p && st.unsupported
}

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

func (a *application) destinationWorkspaceView(c *ui.Context) {
	s := &a.manual
	locked := s.busy() || a.tasks == nil || a.selectedTaskIssue() != nil
	_, hasDestOverlap := a.destinationOverlapMessage(s.targetID, s.target)

	ui.Column(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Gap(0).Children(func() {
		// Breadcrumb Header Strip
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Padding(8, 16).Background(ribbonBlueSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			porcelainCompactButton(c, "← "+a.tr("返回目标列表")).Disabled(s.busy()).OnClick(func() {
				a.cancelDestination(s.targetID)
				a.closeTaskEditor()
			})
		})

		if a.taskError != nil {
			ui.Column(c).FillWidth().Padding(8, 16, 0, 16).Gap(2).Children(func() {
				ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error()).FontSize(12).TextColor(porcelain.Danger)
				ui.Text(c, a.tr("修复文件或备份问题后重新载入。不会以空列表覆盖。")).FontSize(12)
			})
		}
		if issue := a.selectedTaskIssue(); issue != nil {
			ui.Text(c, a.tr("此任务不可运行或编辑: ")+issue.Error()).FontSize(12).TextColor(porcelain.Warning).Padding(8, 16, 0, 16)
		}
		if syncengine.ManualKind(s.kind) == taskconfig.Moving {
			ui.Text(c, a.tr("按目标顺序首个匹配；失败不转投后续目标。")).FontSize(12).TextColor(porcelain.TextMuted).Padding(8, 16, 0, 16)
		}

		// Scrollable Editor Workspace Body
		porcelainScroll(c, &a.scrolls.destModal, func() {
			ui.Column(c).FillWidth().Padding(14, 16).Gap(10).Children(func() {
				ui.Column(c).FillWidth().Background(porcelain.Surface).Border(1, porcelain.Border).Padding(14, 16).Gap(8).Children(func() {
					a.selectedTargetEditor(c, locked)
					if s.message.key != "" {
						ui.Text(c, a.render(s.message)).FontSize(12).TextColor(porcelain.Danger)
					}
				})
			})
		}).Grow(1).Shrink(1).MinHeight(0).FillWidth()

		// Sticky Bottom Action Footer
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(10, 16).Background(porcelain.Surface).BorderWidth(1, 0, 0, 0).BorderColor(porcelain.Border).Children(func() {
			if s.targetID != "" {
				porcelainButton(c, a.tr("复制配置")).Disabled(locked).OnClick(func() { a.copyManualTarget(s.targetID) })
			}
			ui.Box(c).Grow(1)
			porcelainButton(c, a.tr("取消")).Disabled(s.busy()).OnClick(func() {
				a.cancelDestination(s.targetID)
				a.closeTaskEditor()
			})
			ribbonInkButton(c, a.tr("保存")).Disabled(locked || a.taskError != nil || hasDestOverlap).OnClick(func() {
				if a.acceptDestination(s.targetID) {
					a.createManual()
				}
			})
		})
	})
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
	a.triggerRecycleProbe(id, a.manual.target)
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
	ui.Text(c, a.tr("名称")).FontSize(13).FontWeight(600)
	ui.TextInput(c, &s.targetName).Height(32).FontSize(13).Label(a.tr("目标名称")).Placeholder(a.tr("目标名称")).ReadOnly(locked).OnChange(s.edited)
	ui.Text(c, a.tr("目标文件夹")).FontSize(13).FontWeight(600)
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c, &s.target).Height(32).FontSize(13).Label(a.tr("目标文件夹路径")).Grow(1).Placeholder(a.tr("现有目标文件夹绝对路径")).ReadOnly(locked).OnChange(s.edited)
		porcelainButton(c, a.tr("浏览")).Label(a.tr("选择目标")).Disabled(locked).OnClick(func() { a.chooseFolder(false) })
	})
	if overlap, hasOverlap := a.destinationOverlapMessage(s.targetID, s.target); hasOverlap {
		ui.Text(c, a.render(overlap)).FontSize(12).TextColor(porcelain.Danger)
	}

	if syncengine.ManualKind(s.kind) == taskconfig.Moving {
		a.moveOptionsView(c, locked)
	} else {
		a.destinationModeView(c, locked)
	}
	a.filtersView(c, locked || a.selectedMirror())
}

func cloneManualDestination(d syncengine.ManualDestination) syncengine.ManualDestination {
	d.Filters = append([]taskconfig.Rule(nil), d.Filters...)
	for i := range d.Filters {
		d.Filters[i] = d.Filters[i].Clone()
	}
	return d
}

type destinationEditState struct {
	isNew    bool
	original syncengine.ManualDestination
}

func (s *manualSession) beginDestinationEdit(id taskconfig.ID, original *syncengine.ManualDestination) {
	if s.destinationEdits == nil {
		s.destinationEdits = map[taskconfig.ID]destinationEditState{}
	}
	if _, open := s.destinationEdits[id]; open {
		return
	}
	if original == nil {
		s.destinationEdits[id] = destinationEditState{isNew: true}
		return
	}
	s.destinationEdits[id] = destinationEditState{original: cloneManualDestination(*original)}
}

func (a *application) acceptDestination(id taskconfig.ID) bool {
	if !a.targetEditAllowed() {
		return false
	}
	s := &a.manual
	s.flushTarget()
	s.keepFilterDraft()
	if edit, open := s.filterDrafts[id]; open && edit.Error != "" {
		s.message = messagef("过滤尚未保存: %s", edit.Error)
		return false
	}
	for _, d := range s.targets {
		if d.ID != id {
			continue
		}
		if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Path) == "" {
			s.message = messagef("目标名称与文件夹不能为空")
			return false
		}
		if overlap, hasOverlap := a.destinationOverlapMessage(id, d.Path); hasOverlap {
			s.message = overlap
			return false
		}
		delete(s.destinationEdits, id)
		if syncengine.ManualKind(s.kind) == taskconfig.Moving && syncengine.IsAllFileRules(d.Filters) {
			for i, item := range s.targets {
				if item.ID == id {
					s.targets = append(s.targets[:i], s.targets[i+1:]...)
					s.targets = append(s.targets, item)
					break
				}
			}
		}
		a.cards.targetHidden = id
		a.sourcePreview.invalidate()
		return true
	}
	return false
}

func (s *manualSession) canMoveTarget(i, delta int) bool {
	j := i + delta
	if i < 0 || i >= len(s.targets) || j < 0 || j >= len(s.targets) {
		return false
	}
	return syncengine.ManualKind(s.kind) != taskconfig.Moving || (!syncengine.IsAllFileRules(s.targets[i].Filters) && !syncengine.IsAllFileRules(s.targets[j].Filters))
}

func (a *application) acceptOpenDestinations() bool {
	ids := make([]taskconfig.ID, 0, len(a.manual.destinationEdits))
	for _, d := range a.manual.targets {
		if _, open := a.manual.destinationEdits[d.ID]; open {
			ids = append(ids, d.ID)
		}
	}
	for _, id := range ids {
		if !a.acceptDestination(id) {
			return false
		}
	}
	return true
}

func (a *application) cancelDestination(id taskconfig.ID) {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	edit, open := s.destinationEdits[id]
	if !open {
		return
	}
	for i, d := range s.targets {
		if d.ID != id {
			continue
		}
		if edit.isNew {
			s.targets = append(s.targets[:i], s.targets[i+1:]...)
		} else {
			s.targets[i] = cloneManualDestination(edit.original)
		}
		break
	}
	delete(s.destinationEdits, id)
	delete(s.filterDrafts, id)
	s.targetID, s.target, s.targetName = "", "", ""
	if len(s.targets) > 0 {
		s.selectTarget(s.targets[0].ID)
	}
	a.cards.targetHidden = s.targetID
	s.edited()
}

func (a *application) confirmRemoveManualTarget() {
	if !a.targetEditAllowed() || a.manual.targetID == "" {
		return
	}
	s := &a.manual
	id := s.targetID
	autoSave := !a.cards.editor
	options := mygo.MessageOptions{Parent: a.window, Type: mygo.MessageWarning, Title: a.tr("移除目标（仅配置）"), Message: a.tr("确认移除目标「%s」？", s.targetName), Detail: a.tr("只移除此目标配置，不删除文件；其他目标的编辑保留。"), Buttons: []string{a.tr("取消"), a.tr("删除")}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}}
	runDialog(a, func() (mygo.MessageResult, error) {
		return e2eDialogMessage(options)
	}, func(answer mygo.MessageResult, err error) {
		if !s.quitPending && err == nil && answer.Button == 1 && s.targetID == id {
			a.removeManualTarget()
			if autoSave && a.manual.dirty {
				a.createManual()
			}
		}
		if err != nil {
			s.message = messagef("错误: %s", err.Error())
		}
	})
}

func (a *application) sourceOverlapMessage() (localizedMessage, bool) {
	src := strings.TrimSpace(a.manual.source)
	if src == "" {
		return localizedMessage{}, false
	}
	if a.tasks != nil {
		for _, other := range a.tasks.Tasks {
			if other.ID == a.manual.id {
				continue
			}
			if syncengine.ObviousPathOverlap(src, other.SourcePath) {
				return messagef("此文件夹与任务「%s」的源重叠 — 任务之间不能共享源。", other.Name), true
			}
		}
	}
	for _, d := range a.manual.targets {
		if syncengine.ObviousPathOverlap(src, d.Path) {
			return messagef("源文件夹不能与本任务的目标「%s」重叠。", d.Name), true
		}
	}
	return localizedMessage{}, false
}

func (a *application) destinationOverlapMessage(destID taskconfig.ID, path string) (localizedMessage, bool) {
	dst := strings.TrimSpace(path)
	if dst == "" {
		return localizedMessage{}, false
	}
	if syncengine.ObviousPathOverlap(a.manual.source, dst) {
		return messagef("此文件夹与本任务的源文件夹重叠。"), true
	}
	for _, other := range a.manual.targets {
		if other.ID != destID && syncengine.ObviousPathOverlap(other.Path, dst) {
			return messagef("此文件夹与本任务中的「%s」重叠 — 目标之间不能相互重叠。", other.Name), true
		}
	}
	if a.tasks != nil {
		for _, t := range a.tasks.Tasks {
			if t.ID == a.manual.id {
				continue
			}
			for _, existing := range t.Destinations {
				if syncengine.ObviousPathOverlap(dst, existing.Target.Path) {
					return messagef("此文件夹与任务「%s」的一个目标重叠 — 任务之间不能共享目标。", t.Name), true
				}
			}
		}
	}
	return localizedMessage{}, false
}
