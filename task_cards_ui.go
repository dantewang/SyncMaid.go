package main

import (
	"strings"
	"syncmaid/internal/taskconfig"
)

// Layout state is owned by the UI thread, keyed by configuration identity.
type taskCardLayout struct {
	destinations bool
	details      taskconfig.ID
	collapsed    map[taskconfig.ID]bool
	editor       bool
	running      taskconfig.ID
	focus        taskconfig.ID
}

func (a *application) cardActionAllowed() bool {
	return !a.manual.busy() && !a.manual.dirty && a.tasks != nil && a.taskError == nil
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
	if a.manual.dirty {
		a.manual.message = messagef("编辑尚未保存；请保存或明确放弃编辑后再切换任务。")
		return
	}
	a.cards.editor = false
}

func (a *application) toggleCard(id taskconfig.ID) {
	if a.cards.collapsed == nil {
		a.cards.collapsed = make(map[taskconfig.ID]bool)
	}
	a.cards.collapsed[id] = !a.cards.collapsed[id]
}

func (a *application) ruleSummary(rules []taskconfig.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.Kind == "all" {
			parts = append(parts, a.tr("全部文件"))
			continue
		}
		if r.Kind == "none" {
			parts = append(parts, a.tr("无匹配"))
			continue
		}
		if r.Rules != nil {
			parts = append(parts, a.tr(r.Kind)+" ("+a.ruleSummary(*r.Rules)+")")
			continue
		}
		if r.Rule != nil {
			parts = append(parts, a.tr(r.Kind)+" ("+a.ruleSummary([]taskconfig.Rule{*r.Rule})+")")
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
		parts = append(parts, a.tr(r.Kind)+" "+value)
	}
	return strings.Join(parts, " · ")
}
