// Package filefilter prepares immutable, filesystem-free file selectors.
// Match inputs are source-root-relative paths supplied by the caller, not OS
// paths. This package neither enumerates files nor authorizes deletion/execution.
//
// Nonempty Rust regex and full Unicode case mappings remain unsupported. All
// rules are prepared before matching, including branches that could short-circuit.
// Errors never produce a partial usable matcher or silently mean all/none.
package filefilter

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"syncmaid/internal/taskconfig"
)

var (
	ErrInvalidRule        = errors.New("invalid or noncanonical filter")
	ErrUnsupportedRegex   = errors.New("Rust regex compatibility not implemented")
	ErrUnsupportedUnicode = errors.New("Unicode case compatibility not verified for this character")
	ErrUnknownFields      = errors.New("unknown filter fields have unverified semantics")
	ErrInvalidInput       = errors.New("invalid filter path input")
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
	root      *node
	caseCheck bool
}
type node struct {
	kind, text string
	runes      []rune
	segments   []segment
	children   []*node
}

// Compile prepares destination filters as OR in their supplied order; no rules
// select nothing. Payloads must be canonical model values: JSON Decode or the
// taskconfig leaf constructors apply legacy normalization exactly once. Raw Go
// struct literals with leading/trailing separators or extension decorations are
// refused, not re-normalized (legacy extension normalization is not idempotent).
func Compile(rules []taskconfig.Rule) (*Matcher, error) {
	root := &node{kind: "anyOf"}
	m := &Matcher{root: root}
	count := 0
	for i, r := range rules {
		n, err := prepare(r, fmt.Sprintf("Filters[%d]", i), 0, &count, &m.caseCheck)
		if err != nil {
			return nil, err
		}
		root.children = append(root.children, n)
	}
	return m, nil
}
func prepare(r taskconfig.Rule, field string, depth int, count *int, caseCheck *bool) (*node, error) {
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
		if err == nil && (strings.HasPrefix(n.text, "*") || strings.HasPrefix(n.text, ".")) {
			err = fmt.Errorf("%w: decorated/ambiguous extension; use canonical constructor output", ErrInvalidRule)
		}
	case "wildcard":
		n.text, err = leaf(r.Pattern)
		if err == nil && (strings.Contains(n.text, `\`) || strings.Trim(n.text, "/") != n.text) {
			err = fmt.Errorf("%w: use taskconfig.WildcardFilter", ErrInvalidRule)
		}
		n.segments = prepareSegments(n.text)
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
			child, err := prepare(c, childField, depth+1, count, caseCheck)
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
		n.runes = []rune(n.text)
	}
	if r.Kind == "extension" {
		n.runes = []rune("." + n.text)
	}
	if n.text != "" && r.Kind != "regex" {
		for _, c := range n.text {
			if r.Kind == "wildcard" && (c == '*' || c == '?' || c == '/') {
				continue
			}
			if !supportedCase(c) {
				return fail(n.text, fmt.Errorf("%w: U+%04X", ErrUnsupportedUnicode, c))
			}
			*caseCheck = true
		}
	}
	return n, nil
}

// Match returns an error before evaluating any branch if a required case
// comparison would exceed the verified character repertoire. A caller must
// propagate errors rather than treating false as a safe filtering decision.
// Separators are matching syntax only; dot segments are not cleaned/resolved.
func (m *Matcher) Match(relativePath string) (bool, error) {
	if m == nil || m.root == nil {
		return false, &Error{Field: "Matcher", Err: ErrInvalidRule}
	}
	if !utf8.ValidString(relativePath) || strings.ContainsRune(relativePath, 0) {
		return false, &Error{Field: "RelativePath", Err: ErrInvalidInput}
	}
	if m.caseCheck {
		for _, c := range relativePath {
			if !supportedCase(c) {
				return false, &Error{Field: "RelativePath", Pattern: relativePath, Err: fmt.Errorf("%w: U+%04X", ErrUnsupportedUnicode, c)}
			}
		}
	}
	return m.root.match(relativePath), nil
}

// Explicit repertoire, not Go EqualFold masquerading as Rust's per-scalar FULL
// uppercase comparison. Latin-1 simple mappings (except expanding sharp-s),
// BMP CJK/kana and fullwidth ASCII have stable audited scalar mappings. Other
// characters are refused when case comparisons are needed, pending full tables.
func supportedCase(r rune) bool {
	return r < 128 || (r >= 0xc0 && r <= 0xff && r != 0xdf) || r == 0x178 || (r >= 0x3000 && r <= 0x30ff) || (r >= 0x3400 && r <= 0x4dbf) || (r >= 0x4e00 && r <= 0x9fff) || (r >= 0xff01 && r <= 0xff5e)
}
func split(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' })
}
func equal(a, b rune) bool { return unicode.ToUpper(a) == unicode.ToUpper(b) }
func (n *node) match(p string) bool {
	switch n.kind {
	case "all":
		return true
	case "path":
		if n.text == "" {
			return false
		}
		prefix := n.runes
		input := []rune(strings.TrimLeft(strings.ReplaceAll(p, `\`, "/"), "/"))
		if len(input) < len(prefix) {
			return false
		}
		for i, c := range prefix {
			if !equal(c, input[i]) {
				return false
			}
		}
		return len(input) == len(prefix) || input[len(prefix)] == '/'
	case "extension":
		if n.text == "" {
			return false
		}
		suffix := n.runes
		input := []rune(p)
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
		return len(n.segments) > 0 && matchSegments(n.segments, split(p))
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
