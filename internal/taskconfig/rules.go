package taskconfig

import (
	"bytes"
	"strings"
)

// PathFilter, ExtensionFilter and WildcardFilter apply the same one-time
// normalization as legacy JSON decoding. Direct Go callers should use these
// constructors instead of filling unnormalized payloads themselves.
func PathFilter(prefix string) Rule {
	s := normalizeFilterPath(prefix)
	return Rule{Kind: "path", Prefix: &s}
}
func ExtensionFilter(extension string) Rule {
	s := normalizeExtension(extension)
	r := Rule{Kind: "extension", Extension: &s}
	if normalizeExtension(s) != s {
		r.extensionWire = extension
	}
	return r
}
func WildcardFilter(pattern string) Rule {
	s := normalizeFilterPath(pattern)
	return Rule{Kind: "wildcard", Pattern: &s}
}
func normalizeFilterPath(s string) string { return strings.Trim(strings.ReplaceAll(s, `\`, "/"), "/") }
func normalizeExtension(s string) string  { return strings.TrimLeft(strings.TrimLeft(s, "*"), ".") }

// HasUnknownFields reports preserved members whose execution semantics are not
// known. A compiler must check every nested rule, even in short-circuited trees.
func (r Rule) HasUnknownFields() bool { return len(r.extra) != 0 }

// PayloadCount returns the number of populated payload pointer fields on r.
func (r Rule) PayloadCount() int {
	n := 0
	for _, set := range []bool{r.Prefix != nil, r.Extension != nil, r.Pattern != nil, r.Rules != nil, r.Rule != nil} {
		if set {
			n++
		}
	}
	return n
}

func (r Rule) effectiveWireExtension() string {
	if r.Kind != "extension" || r.Extension == nil {
		return ""
	}
	canonical := *r.Extension
	if normalizeExtension(canonical) != canonical {
		if r.extensionWire != "" && normalizeExtension(r.extensionWire) == canonical {
			return r.extensionWire
		}
		return "*." + canonical
	}
	return canonical
}

// Equal reports whether two rules have identical wire/semantic contents,
// including unknown JSON members and non-idempotent extension wire spellings.
func (r Rule) Equal(other Rule) bool {
	if r.Kind != other.Kind || !equalStringPtr(r.Prefix, other.Prefix) || !equalStringPtr(r.Pattern, other.Pattern) {
		return false
	}
	if !equalStringPtr(r.Extension, other.Extension) || r.effectiveWireExtension() != other.effectiveWireExtension() {
		return false
	}
	if (r.Rule == nil) != (other.Rule == nil) {
		return false
	}
	if r.Rule != nil && !r.Rule.Equal(*other.Rule) {
		return false
	}
	if (r.Rules == nil) != (other.Rules == nil) {
		return false
	}
	if r.Rules != nil {
		if (*r.Rules == nil) != (*other.Rules == nil) || !RulesEqual(*r.Rules, *other.Rules) {
			return false
		}
	}
	if len(r.extra) != len(other.extra) {
		return false
	}
	for k, v := range r.extra {
		ov, ok := other.extra[k]
		if !ok || !bytes.Equal(v, ov) {
			return false
		}
	}
	return true
}

// RulesEqual reports whether two rule slices are element-wise Equal (and share nilness).
func RulesEqual(a, b []Rule) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
