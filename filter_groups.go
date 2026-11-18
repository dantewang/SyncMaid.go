package main

import (
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
)

func (a *application) addFilterRule(group int) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode != appcore.FilterGrouped {
		return
	}
	e := &a.manual.filterEditor
	if group < 0 || group >= len(e.Groups) {
		return
	}
	g := &e.Groups[group]
	if len(g.Rows) >= 128 {
		return
	}
	var r taskconfig.Rule
	switch g.Kind {
	case 0:
		r = taskconfig.PathFilter(g.Input)
	case 1:
		r = taskconfig.ExtensionFilter(g.Input)
	case 2:
		r = taskconfig.WildcardFilter(g.Input)
	}
	if g.Input == "" {
		return
	}
	if r.Prefix != nil && *r.Prefix == "" || r.Extension != nil && *r.Extension == "" || r.Pattern != nil && *r.Pattern == "" {
		e.Error = "过滤规则不能为空"
		a.manual.keepFilterDraft()
		return
	}
	g.Rows = append(g.Rows, r)
	rules, err := e.GroupRules()
	if err != nil {
		g.Rows = g.Rows[:len(g.Rows)-1]
		e.Error = err.Error()
		a.manual.keepFilterDraft()
		return
	}
	g.Input = ""
	a.commitFilterDraft(rules, nil)
}
