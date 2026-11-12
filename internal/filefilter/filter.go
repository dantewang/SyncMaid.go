// Package filefilter prepares immutable, filesystem-free file selectors.
// Match inputs are source-root-relative paths supplied by the caller, not OS
// paths. This package neither enumerates files nor authorizes deletion/execution.
//
// Nonempty Rust regex remains unsupported. Unicode17 full per-scalar mappings
// are version-checked and independently verified by an offline digest test. All
// rules are prepared before matching, including branches that could short-circuit.
// Errors never produce a partial usable matcher or silently mean all/none.
package filefilter

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"syncmaid/internal/taskconfig"
)

var (
	ErrInvalidRule      = errors.New("invalid or noncanonical filter")
	ErrUnsupportedRegex = errors.New("Rust regex compatibility not implemented")
	ErrUnicodeVersion   = errors.New("Unicode table version mismatch")
	ErrMapping          = errors.New("uppercase mapping failed")
	ErrUnknownFields    = errors.New("unknown filter fields have unverified semantics")
	ErrInvalidInput     = errors.New("invalid filter path input")
)

type Error struct {
	Field, Pattern string
	Err            error
}

func (e *Error) Error() string { return fmt.Sprintf("%s (%q): %v", e.Field, e.Pattern, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// Matcher is immutable after Compile and owns no mutable input references.
// Its zero value is not prepared. Successful matching covers only filtering,
// never taskconfig's other Unverified execution conditions.
type Matcher struct {
	root *node
}
type node struct {
	kind, text string
	runes      []scalar
	segments   []segment
	children   []*node
}

// Compile prepares destination filters as OR in their supplied order; no rules
// select nothing. Payloads must be canonical model values: JSON Decode or the
// taskconfig leaf constructors apply legacy normalization exactly once. Raw Go
// struct literals are canonical values, not raw user spelling. In particular,
// a canonical extension starting * is literal and is never normalized again.
func Compile(rules []taskconfig.Rule) (*Matcher, error) {
	if err := checkUnicodeTables(); err != nil {
		return nil, &Error{Field: "UnicodeTables", Err: err}
	}
	root := &node{kind: "anyOf"}
	m := &Matcher{root: root}
	count := 0
	for i, r := range rules {
		n, err := prepare(r, fmt.Sprintf("Filters[%d]", i), 0, &count)
		if err != nil {
			return nil, err
		}
		root.children = append(root.children, n)
	}
	return m, nil
}
func prepare(r taskconfig.Rule, field string, depth int, count *int) (*node, error) {
	*count = *count + 1
	fail := func(pattern string, err error) (*node, error) {
		return nil, &Error{Field: field, Pattern: pattern, Err: err}
	}
	if depth > 128 || *count > 4096 {
		return fail("", fmt.Errorf("%w: filter complexity limit", ErrInvalidRule))
	}
	if r.HasUnknownFields() {
		return fail("", ErrUnknownFields)
	}
	n := &node{kind: r.Kind}
	payloads := 0
	for _, present := range []bool{r.Prefix != nil, r.Extension != nil, r.Pattern != nil, r.Rules != nil, r.Rule != nil} {
		if present {
			payloads++
		}
	}
	leaf := func(s *string) (string, error) {
		if s == nil || payloads != 1 {
			return "", ErrInvalidRule
		}
		if !utf8.ValidString(*s) || strings.ContainsRune(*s, 0) {
			return "", ErrInvalidRule
		}
		return *s, nil
	}
	var err error
	switch r.Kind {
	case "all":
		if payloads != 0 {
			return fail("", ErrInvalidRule)
		}
	case "path":
		n.text, err = leaf(r.Prefix)
		if err == nil && (strings.Contains(n.text, `\`) || strings.Trim(n.text, "/") != n.text) {
			err = fmt.Errorf("%w: use taskconfig.PathFilter", ErrInvalidRule)
		}
	case "extension":
		n.text, err = leaf(r.Extension)
		if err == nil && strings.HasPrefix(n.text, ".") {
			err = fmt.Errorf("%w: extension must be canonical; use taskconfig.ExtensionFilter for raw input", ErrInvalidRule)
		}
	case "wildcard":
		n.text, err = leaf(r.Pattern)
		if err == nil && (strings.Contains(n.text, `\`) || strings.Trim(n.text, "/") != n.text) {
			err = fmt.Errorf("%w: use taskconfig.WildcardFilter", ErrInvalidRule)
		}
	case "regex":
		n.text, err = leaf(r.Pattern)
		if err == nil && n.text != "" {
			return fail(n.text, ErrUnsupportedRegex)
		} // Only the empty constant-none rule is proven without an engine.
	case "anyOf", "allOf", "not":
		var children []taskconfig.Rule
		if r.Kind == "not" {
			if r.Rule == nil || payloads != 1 {
				return fail("", ErrInvalidRule)
			}
			children = []taskconfig.Rule{*r.Rule}
		} else {
			if r.Rules == nil || *r.Rules == nil || payloads != 1 {
				return fail("", ErrInvalidRule)
			}
			children = *r.Rules
		}
		for i, c := range children {
			childField := fmt.Sprintf("%s.Rules[%d]", field, i)
			if r.Kind == "not" {
				childField = field + ".Rule"
			}
			child, err := prepare(c, childField, depth+1, count)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, child)
		}
	default:
		return fail(r.Kind, ErrInvalidRule)
	}
	if err != nil {
		return fail(n.text, err)
	}
	if r.Kind == "path" {
		n.runes, err = mapScalars(n.text)
	}
	if r.Kind == "extension" {
		n.runes, err = mapScalars("." + n.text)
	}
	if r.Kind == "wildcard" {
		n.segments, err = prepareSegments(n.text)
	}
	if err != nil {
		return fail(n.text, err)
	}
	return n, nil
}

// Match prepares each original scalar once for the path, without flattening
// uppercase expansions or retaining a cache across calls. A caller must
// propagate errors rather than treating false as a safe filtering decision.
// Separators are matching syntax only; dot segments are not cleaned/resolved.
func (m *Matcher) Match(relativePath string) (bool, error) {
	if m == nil || m.root == nil {
		return false, &Error{Field: "Matcher", Err: ErrInvalidRule}
	}
	if !utf8.ValidString(relativePath) || strings.ContainsRune(relativePath, 0) {
		return false, &Error{Field: "RelativePath", Err: ErrInvalidInput}
	}
	input, err := preparePath(relativePath)
	if err != nil {
		return false, &Error{Field: "RelativePath", Err: err}
	}
	return m.root.match(&input), nil
}

func (n *node) match(p *pathInput) bool {
	switch n.kind {
	case "all":
		return true
	case "path":
		if n.text == "" {
			return false
		}
		prefix := n.runes
		input := p.scalars
		for len(input) > 0 && separator(input[0].raw) {
			input = input[1:]
		}
		if len(input) < len(prefix) {
			return false
		}
		for i, c := range prefix {
			if !(separator(c.raw) && separator(input[i].raw)) && !equal(c, input[i]) {
				return false
			}
		}
		return len(input) == len(prefix) || separator(input[len(prefix)].raw)
	case "extension":
		if n.text == "" {
			return false
		}
		suffix := n.runes
		input := p.scalars
		if len(input) < len(suffix) {
			return false
		}
		input = input[len(input)-len(suffix):]
		for i, c := range suffix {
			if !equal(c, input[i]) {
				return false
			}
		}
		return true
	case "wildcard":
		return len(n.segments) > 0 && matchSegments(n.segments, p.segments)
	case "regex":
		return false
	case "not":
		return !n.children[0].match(p)
	case "anyOf":
		for _, c := range n.children {
			if c.match(p) {
				return true
			}
		}
		return false
	case "allOf":
		if len(n.children) == 0 {
			return false
		}
		for _, c := range n.children {
			if !c.match(p) {
				return false
			}
		}
		return true
	}
	return false // Unreachable: only private prepared nodes reach matching.
}
