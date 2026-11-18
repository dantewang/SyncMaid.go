package syncengine

import (
	"context"
	"fmt"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

func RulesHaveUnknownFields(rules []taskconfig.Rule) bool {
	for _, r := range rules {
		if r.HasUnknownFields() || (r.Rules != nil && RulesHaveUnknownFields(*r.Rules)) || (r.Rule != nil && RulesHaveUnknownFields([]taskconfig.Rule{*r.Rule})) {
			return true
		}
	}
	return false
}

func FilterGroup(kind string, rules []taskconfig.Rule) taskconfig.Rule {
	if rules == nil {
		rules = []taskconfig.Rule{}
	}
	return taskconfig.Rule{Kind: kind, Rules: &rules}
}

func AllFileRules() []taskconfig.Rule { return []taskconfig.Rule{{Kind: "all"}} }

func EmptyRulePayload(r taskconfig.Rule) bool {
	return r.PayloadCount() == 0 && !r.HasUnknownFields()
}

func IsAllFileRules(rules []taskconfig.Rule) bool {
	return len(rules) == 1 && rules[0].Kind == "all" && EmptyRulePayload(rules[0])
}

func SameFilterRules(a, b []taskconfig.Rule) bool {
	return taskconfig.RulesEqual(a, b)
}

// Complete selection is materialized before the first destination write. Any
// compile/match/cancellation error discards the selection, never returns a prefix.
func SelectFilteredFiles(ctx context.Context, raw []fileinventory.File, rules []taskconfig.Rule) ([]fileinventory.File, error) {
	matcher, err := filefilter.Compile(rules)
	if err != nil {
		return nil, err
	}
	return SelectMatchedFiles(ctx, raw, matcher)
}

func SelectMatchedFiles(ctx context.Context, raw []fileinventory.File, matcher *filefilter.Matcher) ([]fileinventory.File, error) {
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
