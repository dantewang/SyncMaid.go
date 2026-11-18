package main

import (
	"strings"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

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
		if syncengine.ObviousPathOverlap(s.source, d.Path) {
			s.message = messagef("错误: %s", syncengine.ErrTransferOverlap.Error())
			return false
		}
		for _, other := range s.targets {
			if other.ID != id && syncengine.ObviousPathOverlap(other.Path, d.Path) {
				s.message = messagef("错误: %s", syncengine.ErrTransferOverlap.Error())
				return false
			}
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
