package main

import (
	"strings"
	"syncmaid/internal/taskconfig"
)

// Layout state is owned by the UI thread, keyed by configuration identity.
type taskCardLayout struct {
	navigation   taskconfig.ID
	targetHidden taskconfig.ID
	destinations bool
	collapsed    map[taskconfig.ID]bool
	editor       bool
	running      taskconfig.ID
	focus        taskconfig.ID
}

func (a *application) cardActionAllowed() bool {
	return a.batch.phase == batchIdle && a.sourcePreview.job == nil && !a.manual.busy() && !a.manual.dirty && a.tasks != nil && a.taskError == nil
}

func (a *application) editCard(id, destination taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id != id {
		return
	}
	if destination != "" {
		a.manual.selectTarget(destination)
		for _, d := range a.manual.targets {
			if d.ID == destination {
				a.manual.beginDestinationEdit(destination, &d)
			}
		}
		a.cards.targetHidden = ""
	}
	a.cards.destinations = destination != ""
	a.cards.editor = true
}

func (a *application) runCard(id taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id == id {
		a.startManual()
	}
}

func (a *application) closeTaskEditor() {
	if a.manual.busy() {
		return
	}
	a.sourcePreview.invalidate()
	if a.taskError != nil {
		a.manual = manualSession{onEdit: a.manualEdited, phase: manualDraft, quitPending: a.manual.quitPending}
	} else {
		a.discardManual()
	}
	a.cards.editor = false
}

func (a *application) toggleCard(id taskconfig.ID) {
	if a.cards.collapsed == nil {
		a.cards.collapsed = make(map[taskconfig.ID]bool)
	}
	if a.cards.collapsed[id] {
		delete(a.cards.collapsed, id)
		return
	}
	a.cards.collapsed[id] = true
}

func (a *application) hasCollapsedCards() bool {
	if a.tasks != nil {
		for _, t := range a.tasks.Tasks {
			if a.cards.collapsed[t.ID] {
				return true
			}
		}
	}
	return false
}

func (a *application) filterSummary(rules []taskconfig.Rule) string {
	if len(rules) == 0 {
		return a.tr("无规则")
	}
	if len(rules) == 1 {
		switch rules[0].Kind {
		case "all":
			return a.tr("所有文件")
		case "none":
			return a.tr("无匹配")
		}
	}
	return a.tr("%d 条过滤规则", len(rules))
}

func (a *application) ruleSummary(rules []taskconfig.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		label := map[string]string{"path": "路径前缀", "extension": "扩展名", "wildcard": "通配符", "regex": "正则表达式", "allOf": "全部满足", "anyOf": "任一满足", "not": "排除"}[r.Kind]
		if label == "" {
			label = r.Kind
		}
		if r.Kind == "all" {
			parts = append(parts, a.tr("所有文件"))
			continue
		}
		if r.Kind == "none" {
			parts = append(parts, a.tr("无匹配"))
			continue
		}
		if r.Rules != nil {
			if len(*r.Rules) == 0 {
				parts = append(parts, a.tr("无匹配"))
				continue
			}
			parts = append(parts, a.tr(label)+" ("+a.ruleSummary(*r.Rules)+")")
			continue
		}
		if r.Rule != nil {
			parts = append(parts, a.tr(label)+" ("+a.ruleSummary([]taskconfig.Rule{*r.Rule})+")")
			continue
		}
		value := ""
		switch {
		case r.Prefix != nil:
			value = *r.Prefix
		case r.Extension != nil:
			value = *r.Extension
		case r.Pattern != nil:
			value = *r.Pattern
		}
		parts = append(parts, a.tr(label)+" "+value)
	}
	return strings.Join(parts, " · ")
}
