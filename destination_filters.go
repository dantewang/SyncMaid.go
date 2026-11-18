package main

import (
	"context"
	"errors"
	"fmt"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

func rulesHaveUnknownFields(rules []taskconfig.Rule) bool {
	for _, r := range rules {
		if r.HasUnknownFields() || (r.Rules != nil && rulesHaveUnknownFields(*r.Rules)) || (r.Rule != nil && rulesHaveUnknownFields([]taskconfig.Rule{*r.Rule})) {
			return true
		}
	}
	return false
}

func taskConfigurationDeletionIssue(t taskconfig.Task) error {
	if _, err := multipleRequestFromSaved(t); err != nil {
		return err
	}
	for _, d := range t.Destinations {
		if rulesHaveUnknownFields(d.Filters) {
			return fmt.Errorf("%s 含未支持字段，不能删除其配置", d.Name)
		}
	}
	return nil
}

type filterMode uint8

const (
	filterAll filterMode = iota
	filterNone
	filterReadOnly
	filterGrouped
)

type filterEditor struct {
	Groups                 []filterDraftGroup
	MatchAll               bool
	Mode                   filterMode
	Error, Reason, RunError string
}

func filterGroup(kind string, rules []taskconfig.Rule) taskconfig.Rule {
	if rules == nil {
		rules = []taskconfig.Rule{}
	}
	return taskconfig.Rule{Kind: kind, Rules: &rules}
}
func allFileRules() []taskconfig.Rule { return []taskconfig.Rule{{Kind: "all"}} }

func (e filterEditor) rules() ([]taskconfig.Rule, error) {
	switch e.Mode {
	case filterGrouped:
		return e.groupRules()
	case filterAll:
		return allFileRules(), nil
	case filterNone:
		return []taskconfig.Rule{}, nil
	case filterReadOnly:
		return nil, errors.New("只读过滤规则不能改写")
	default:
		return nil, errors.New("未知过滤模式")
	}
}

func emptyRulePayload(r taskconfig.Rule) bool {
	return r.PayloadCount() == 0 && !r.HasUnknownFields()
}
func isAllFileRules(rules []taskconfig.Rule) bool {
	return len(rules) == 1 && rules[0].Kind == "all" && emptyRulePayload(rules[0])
}

func sameFilterRules(a, b []taskconfig.Rule) bool {
	return taskconfig.RulesEqual(a, b)
}

// Complete selection is materialized before the first destination write. Any
// compile/match/cancellation error discards the selection, never returns a prefix.
func selectFilteredFiles(ctx context.Context, raw []fileinventory.File, rules []taskconfig.Rule) ([]fileinventory.File, error) {
	matcher, err := filefilter.Compile(rules)
	if err != nil {
		return nil, err
	}
	return selectMatchedFiles(ctx, raw, matcher)
}
func selectMatchedFiles(ctx context.Context, raw []fileinventory.File, matcher *filefilter.Matcher) ([]fileinventory.File, error) {
	var err error
	selected := make([]fileinventory.File, 0, len(raw))
	for _, f := range raw {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		yes, e := matcher.Match(f.Path)
		if e != nil {
			return nil, fmt.Errorf("过滤 %q: %w", f.Path, e)
		}
		if yes {
			selected = append(selected, f)
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return selected, nil
}
