package taskconfig

import "strings"

// PathFilter, ExtensionFilter and WildcardFilter apply the same one-time
// normalization as legacy JSON decoding. Direct Go callers should use these
// constructors instead of filling unnormalized payloads themselves.
func PathFilter(prefix string) Rule {
	s := normalizeFilterPath(prefix)
	return Rule{Kind: "path", Prefix: &s}
}
func ExtensionFilter(extension string) Rule {
	s := normalizeExtension(extension)
	return Rule{Kind: "extension", Extension: &s}
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
