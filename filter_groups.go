package main

import (
	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

type filterDraftGroup struct {
	All   bool
	Rows  []taskconfig.Rule
	Kind  int
	Input string
}

func (e filterEditor) groupRules() ([]taskconfig.Rule, error) {
	groups := make([]taskconfig.Rule, 0, len(e.Groups))
	for _, g := range e.Groups {
		kind := "anyOf"
		if g.All {
			kind = "allOf"
		}
		groups = append(groups, syncengine.FilterGroup(kind, g.Rows))
	}
	var rules []taskconfig.Rule
	if e.MatchAll {
		rules = []taskconfig.Rule{syncengine.FilterGroup("allOf", groups)}
	} else {
		rules = groups
	}
	if _, err := filefilter.Compile(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func filterLeaf(r taskconfig.Rule) bool {
	if r.Kind == "not" {
		if r.HasUnknownFields() || r.Rule == nil || r.PayloadCount() != 1 {
			return false
		}
		r = *r.Rule
	}
	return !r.HasUnknownFields() && r.Rules == nil && r.Rule == nil && (r.Kind == "path" || r.Kind == "extension" || r.Kind == "wildcard" || r.Kind == "all")
}
func projectFilterGroup(r taskconfig.Rule) (filterDraftGroup, bool) {
	if filterLeaf(r) {
		return filterDraftGroup{Rows: []taskconfig.Rule{r.Clone()}}, true
	}
	// De Morgan preserves even the empty excluded-any group (true).
	if r.Kind == "not" && !r.HasUnknownFields() && r.Rule != nil && r.PayloadCount() == 1 {
		inner := *r.Rule
		if inner.Kind == "anyOf" && !inner.HasUnknownFields() && inner.Rules != nil && inner.PayloadCount() == 1 {
			g := filterDraftGroup{All: true}
			if len(*inner.Rules) == 0 {
				g.Rows = []taskconfig.Rule{{Kind: "all"}}
				return g, true
			}
			for _, x := range *inner.Rules {
				if !filterLeaf(x) || x.Kind == "not" {
					return filterDraftGroup{}, false
				}
				v := x.Clone()
				g.Rows = append(g.Rows, taskconfig.Rule{Kind: "not", Rule: &v})
			}
			return g, true
		}
	}
	if r.HasUnknownFields() || r.Rules == nil || r.PayloadCount() != 1 || (r.Kind != "allOf" && r.Kind != "anyOf") {
		return filterDraftGroup{}, false
	}
	g := filterDraftGroup{All: r.Kind == "allOf"}
	for _, x := range *r.Rules {
		if !filterLeaf(x) {
			return filterDraftGroup{}, false
		}
		g.Rows = append(g.Rows, x.Clone())
	}
	return g, true
}
func projectNativeFilter(rules []taskconfig.Rule) filterEditor {
	if syncengine.IsAllFileRules(rules) {
		return filterEditor{Mode: filterAll}
	}
	if len(rules) == 0 {
		return filterEditor{Mode: filterNone}
	}
	ro := filterEditor{Mode: filterReadOnly, Reason: "此规则结构不能无损编辑，已保留原规则（只读）。"}
	if _, err := filefilter.Compile(rules); err != nil {
		ro.RunError = err.Error()
		return ro
	}
	e := filterEditor{Mode: filterGrouped}
	roots := rules
	if len(rules) == 1 && !rules[0].HasUnknownFields() && (rules[0].Kind == "allOf" || rules[0].Kind == "anyOf") && rules[0].Rules != nil {
		if g, ok := projectFilterGroup(rules[0]); ok {
			e.Groups = []filterDraftGroup{g}
			return e
		}
		e.MatchAll = rules[0].Kind == "allOf"
		roots = *rules[0].Rules
	}
	for _, r := range roots {
		g, ok := projectFilterGroup(r)
		if !ok {
			return ro
		}
		e.Groups = append(e.Groups, g)
	}
	return e
}
func (a *application) addFilterRule(group int) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode != filterGrouped {
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
	rules, err := e.groupRules()
	if err != nil {
		g.Rows = g.Rows[:len(g.Rows)-1]
		e.Error = err.Error()
		a.manual.keepFilterDraft()
		return
	}
	g.Input = ""
	a.commitFilterDraft(rules, nil)
}
