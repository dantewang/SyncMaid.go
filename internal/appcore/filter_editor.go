package appcore

import (
	"errors"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

type FilterMode uint8

const (
	FilterAll FilterMode = iota
	FilterNone
	FilterReadOnly
	FilterGrouped
)

type FilterEditor struct {
	Groups                  []FilterDraftGroup
	MatchAll                bool
	Mode                    FilterMode
	Error, Reason, RunError string
}

func (e FilterEditor) Rules() ([]taskconfig.Rule, error) {
	switch e.Mode {
	case FilterGrouped:
		return e.GroupRules()
	case FilterAll:
		return syncengine.AllFileRules(), nil
	case FilterNone:
		return []taskconfig.Rule{}, nil
	case FilterReadOnly:
		return nil, errors.New("只读过滤规则不能改写")
	default:
		return nil, errors.New("未知过滤模式")
	}
}

type FilterDraftGroup struct {
	All   bool
	Rows  []taskconfig.Rule
	Kind  int
	Input string
}

func (e FilterEditor) GroupRules() ([]taskconfig.Rule, error) {
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

func projectFilterGroup(r taskconfig.Rule) (FilterDraftGroup, bool) {
	if filterLeaf(r) {
		return FilterDraftGroup{Rows: []taskconfig.Rule{r.Clone()}}, true
	}
	// De Morgan preserves even the empty excluded-any group (true).
	if r.Kind == "not" && !r.HasUnknownFields() && r.Rule != nil && r.PayloadCount() == 1 {
		inner := *r.Rule
		if inner.Kind == "anyOf" && !inner.HasUnknownFields() && inner.Rules != nil && inner.PayloadCount() == 1 {
			g := FilterDraftGroup{All: true}
			if len(*inner.Rules) == 0 {
				g.Rows = []taskconfig.Rule{{Kind: "all"}}
				return g, true
			}
			for _, x := range *inner.Rules {
				if !filterLeaf(x) || x.Kind == "not" {
					return FilterDraftGroup{}, false
				}
				v := x.Clone()
				g.Rows = append(g.Rows, taskconfig.Rule{Kind: "not", Rule: &v})
			}
			return g, true
		}
	}
	if r.HasUnknownFields() || r.Rules == nil || r.PayloadCount() != 1 || (r.Kind != "allOf" && r.Kind != "anyOf") {
		return FilterDraftGroup{}, false
	}
	g := FilterDraftGroup{All: r.Kind == "allOf"}
	for _, x := range *r.Rules {
		if !filterLeaf(x) {
			return FilterDraftGroup{}, false
		}
		g.Rows = append(g.Rows, x.Clone())
	}
	return g, true
}

func ProjectNativeFilter(rules []taskconfig.Rule) FilterEditor {
	if syncengine.IsAllFileRules(rules) {
		return FilterEditor{Mode: FilterAll}
	}
	if len(rules) == 0 {
		return FilterEditor{Mode: FilterNone}
	}
	ro := FilterEditor{Mode: FilterReadOnly, Reason: "此规则结构不能无损编辑，已保留原规则（只读）。"}
	if _, err := filefilter.Compile(rules); err != nil {
		ro.RunError = err.Error()
		return ro
	}
	e := FilterEditor{Mode: FilterGrouped}
	roots := rules
	if len(rules) == 1 && !rules[0].HasUnknownFields() && (rules[0].Kind == "allOf" || rules[0].Kind == "anyOf") && rules[0].Rules != nil {
		if g, ok := projectFilterGroup(rules[0]); ok {
			e.Groups = []FilterDraftGroup{g}
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
