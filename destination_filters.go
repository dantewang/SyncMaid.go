package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	filterCustom
	filterReadOnly
	filterGrouped
)

type filterEditor struct {
	Groups                                        []filterDraftGroup
	MatchAll                                      bool
	Mode                                          filterMode
	IncludePaths, IncludeExtensions, IncludeGlobs string
	ExcludePaths, ExcludeExtensions, ExcludeGlobs string
	Error, Reason, RunError                       string
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
	case filterCustom:
	default:
		return nil, errors.New("未知过滤模式")
	}
	parse := func(paths, extensions, globs, section string) ([]taskconfig.Rule, error) {
		var out []taskconfig.Rule
		for _, field := range []struct {
			name, text string
			makeRule   func(string) taskconfig.Rule
		}{{"路径", paths, taskconfig.PathFilter}, {"扩展名", extensions, taskconfig.ExtensionFilter}, {"通配符", globs, taskconfig.WildcardFilter}} {
			for i, line := range strings.Split(strings.ReplaceAll(field.text, "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				r := field.makeRule(line)
				if r.Prefix != nil && *r.Prefix == "" || r.Extension != nil && *r.Extension == "" || r.Pattern != nil && *r.Pattern == "" {
					return nil, fmt.Errorf("%s%s第%d行为空规则，请使用全部/无匹配模式", section, field.name, i+1)
				}
				out = append(out, r)
			}
		}
		return out, nil
	}
	include, err := parse(e.IncludePaths, e.IncludeExtensions, e.IncludeGlobs, "包含")
	if err != nil {
		return nil, err
	}
	exclude, err := parse(e.ExcludePaths, e.ExcludeExtensions, e.ExcludeGlobs, "排除")
	if err != nil {
		return nil, err
	}
	i := taskconfig.Rule{Kind: "all"}
	if len(include) > 0 {
		i = filterGroup("anyOf", include)
	}
	x := filterGroup("anyOf", exclude)
	rules := []taskconfig.Rule{filterGroup("allOf", []taskconfig.Rule{i, {Kind: "not", Rule: &x}})}
	if _, err = filefilter.Compile(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func emptyRulePayload(r taskconfig.Rule) bool {
	return r.Prefix == nil && r.Extension == nil && r.Pattern == nil && r.Rules == nil && r.Rule == nil && !r.HasUnknownFields()
}
func isAllFileRules(rules []taskconfig.Rule) bool {
	return len(rules) == 1 && rules[0].Kind == "all" && emptyRulePayload(rules[0])
}

// Only our small, explicit include-any AND NOT exclude-any shape is editable.
// Projection never changes the source rules. Unrepresentable input stays raw.
func projectFilterEditor(rules []taskconfig.Rule) filterEditor {
	if isAllFileRules(rules) {
		return filterEditor{Mode: filterAll}
	}
	if len(rules) == 0 {
		return filterEditor{Mode: filterNone}
	}
	readOnly := func() filterEditor {
		reason := "此规则结构不能无损编辑，已保留原规则（只读）。"
		issue := ""
		if _, err := filefilter.Compile(rules); err != nil {
			issue = err.Error()
			reason += " 该目标运行将失败: " + issue
		}
		return filterEditor{Mode: filterReadOnly, Reason: reason, RunError: issue}
	}
	if len(rules) != 1 {
		return readOnly()
	}
	root := rules[0]
	if _, err := filefilter.Compile(rules); err != nil {
		return readOnly()
	}
	groupOK := func(r taskconfig.Rule, kind string) bool {
		return r.Kind == kind && !r.HasUnknownFields() && r.Rules != nil && *r.Rules != nil && r.Rule == nil && r.Pattern == nil && r.Extension == nil && r.Prefix == nil
	}
	if !groupOK(root, "allOf") || len(*root.Rules) != 2 {
		return readOnly()
	}
	i, n := (*root.Rules)[0], (*root.Rules)[1]
	if n.Kind != "not" || n.HasUnknownFields() || n.Rule == nil || n.Rules != nil || n.Pattern != nil || n.Extension != nil || n.Prefix != nil || !groupOK(*n.Rule, "anyOf") {
		return readOnly()
	}
	e := filterEditor{Mode: filterCustom}
	project := func(rs []taskconfig.Rule, paths, extensions, globs *string) bool {
		for _, r := range rs {
			if r.HasUnknownFields() || r.Rule != nil || r.Rules != nil {
				return false
			}
			var raw string
			var field *string
			switch {
			case r.Kind == "path" && r.Prefix != nil && r.Extension == nil && r.Pattern == nil:
				raw = *r.Prefix
				field = paths
			case r.Kind == "wildcard" && r.Pattern != nil && r.Prefix == nil && r.Extension == nil:
				raw = *r.Pattern
				field = globs
			case r.Kind == "extension" && r.Extension != nil && r.Prefix == nil && r.Pattern == nil:
				// Use the model's one-pass-safe wire spelling, including residual
				// stars such as *.*.jpg. Canonical text is not necessarily raw UI input.
				b, err := json.Marshal(r)
				if err != nil {
					return false
				}
				var wire struct{ Extension string }
				if json.Unmarshal(b, &wire) != nil {
					return false
				}
				raw = wire.Extension
				field = extensions
			default:
				return false
			}
			if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n") {
				return false
			}
			if *field != "" {
				*field += "\n"
			}
			*field += raw
		}
		return true
	}
	if !(i.Kind == "all" && emptyRulePayload(i)) {
		if !groupOK(i, "anyOf") || len(*i.Rules) == 0 || !project(*i.Rules, &e.IncludePaths, &e.IncludeExtensions, &e.IncludeGlobs) {
			return readOnly()
		}
	}
	if !project(*n.Rule.Rules, &e.ExcludePaths, &e.ExcludeExtensions, &e.ExcludeGlobs) {
		return readOnly()
	}
	if _, err := e.rules(); err != nil {
		return readOnly()
	}
	return e
}

func sameFilterRules(a, b []taskconfig.Rule) bool {
	aa, e1 := json.Marshal(a)
	bb, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && bytes.Equal(aa, bb)
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
