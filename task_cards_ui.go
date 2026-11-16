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
	a.sourcePreview.invalidate()
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
