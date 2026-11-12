package filefilter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func text(s string) *string { return &s }
func group(kind string, rs ...taskconfig.Rule) taskconfig.Rule {
	if rs == nil {
		rs = []taskconfig.Rule{}
	}
	return taskconfig.Rule{Kind: kind, Rules: &rs}
}
func not(r taskconfig.Rule) taskconfig.Rule { return taskconfig.Rule{Kind: "not", Rule: &r} }
func regex(s string) taskconfig.Rule        { return taskconfig.Rule{Kind: "regex", Pattern: &s} }
func match(t *testing.T, r taskconfig.Rule, path string, want bool) {
	t.Helper()
	m, e := Compile([]taskconfig.Rule{r})
	if e != nil {
		t.Fatal(e)
	}
	got, e := m.Match(path)
	if e != nil || got != want {
		t.Fatalf("path %q: got %v %v, want %v", path, got, e, want)
	}
}

// Expected results derive from fixed Rust 1750c7e filtering.rs tests and the
// text.rs scalar-uppercase comparison, not from this Go implementation.
func TestBaselineLeafCases(t *testing.T) {
	cases := []struct {
		name    string
		rule    taskconfig.Rule
		yes, no []string
	}{
		{"path boundary", taskconfig.PathFilter(`\Photos\2024\`), []string{"photos/2024", "PHOTOS/2024/a.jpg", `/photos\2024\raw/a`, "//photos/2024/a"}, []string{"photos/2024b/a", "photos/20", "old/photos/2024"}},
		{"empty path", taskconfig.PathFilter(`///\`), nil, []string{"", "anything.txt"}},
		{"literal internal separators", taskconfig.PathFilter("photos//2024"), []string{"photos//2024/a"}, []string{"photos/2024/a"}},
		{"extension", taskconfig.ExtensionFilter("*.JPG"), []string{"a.jpg", "a/b/C.JpG", ".jpg"}, []string{"jpg", "a.jpeg", "a.jpg/b", "a.jpgx"}},
		{"compound extension", taskconfig.ExtensionFilter(".tar.gz"), []string{"a.tar.gz"}, []string{"a.gz", "a.tar.gzx"}},
		{"empty extension", taskconfig.ExtensionFilter("*."), nil, []string{"a.jpg", "a.", ""}},
		{"literal extension separator", taskconfig.ExtensionFilter("foo/bar"), []string{"a.foo/bar"}, []string{`a.foo\bar`}},
		{"star stays in segment", taskconfig.WildcardFilter("photos/*.raw"), []string{"photos/a.raw", "photos/.raw"}, []string{"photos/2024/a.raw", "old/photos/a.raw"}},
		{"deep star", taskconfig.WildcardFilter("photos/**/*.RAW"), []string{"photos/a.raw", "PHOTOS/2024/05/a.raw"}, []string{"videos/a.raw"}},
		{"trailing deep star", taskconfig.WildcardFilter("photos/**"), []string{"photos", "photos/a", "photos//a/"}, []string{"photos2"}},
		{"leading deep star", taskconfig.WildcardFilter("**/ChatGPT*.png"), []string{"ChatGPT1.png", "a/b/ChatGPT-x.png"}, []string{"a/Claude.png"}},
		{"question rune", taskconfig.WildcardFilter("IMG_????.jpg"), []string{"IMG_0042.jpg", "IMG_中文日本.jpg"}, []string{"IMG_042.jpg", "IMG_00042.jpg"}},
		{"glob separators", taskconfig.WildcardFilter(`\Photos\**\*.RAW\`), []string{`PHOTOS\2024\a.raw`, "photos//a.raw"}, []string{"videos/a.raw"}},
		{"empty wildcard", taskconfig.WildcardFilter("/"), nil, []string{"", "a"}},
		{"backtracking fixture", taskconfig.WildcardFilter("*a*b.txt"), []string{"xaybzb.txt"}, []string{"xayz.txt"}},
		{"double-star only", taskconfig.WildcardFilter("**"), []string{"", "a", "a/b"}, nil},
		{"single-star only", taskconfig.WildcardFilter("*"), []string{"a", ".x"}, []string{"", "a/b"}},
		{"triple star is not deep", taskconfig.WildcardFilter("foo/***/x"), []string{"foo/a/x"}, []string{"foo/x", "foo/a/b/x"}},
		{"literal class syntax", taskconfig.WildcardFilter("[a].txt"), []string{"[a].txt"}, []string{"a.txt"}},
		{"blank regex proven constant", regex(""), nil, []string{"", "a.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, s := range c.yes {
				match(t, c.rule, s, true)
			}
			for _, s := range c.no {
				match(t, c.rule, s, false)
			}
		})
	}
}
func TestCompositionAndDestinationOr(t *testing.T) {
	pdf, jpg := taskconfig.ExtensionFilter("pdf"), taskconfig.ExtensionFilter("jpg")
	match(t, group("allOf", taskconfig.PathFilter("docs"), pdf), "docs/a.pdf", true)
	match(t, group("allOf", taskconfig.PathFilter("docs"), pdf), "photos/a.pdf", false)
	match(t, group("anyOf", pdf, jpg), "a.jpg", true)
	match(t, group("allOf"), "a.txt", false)
	match(t, group("anyOf"), "a.txt", false)
	match(t, not(group("allOf")), "a.txt", true)
	match(t, not(taskconfig.ExtensionFilter("tmp")), "a.txt", true)
	match(t, not(not(jpg)), "a.jpg", true)
	match(t, not(regex("")), "a.txt", true)
	m, e := Compile([]taskconfig.Rule{pdf, jpg})
	if e != nil {
		t.Fatal(e)
	}
	got, e := m.Match("a.pdf")
	if e != nil || !got {
		t.Fatal(got, e)
	}
	m, e = Compile(nil)
	if e != nil {
		t.Fatal(e)
	}
	got, e = m.Match("a.txt")
	if e != nil || got {
		t.Fatal(got, e)
	}
}
func TestDirectConstructorsAndJSONHaveOneNormalization(t *testing.T) {
	cases := []struct {
		direct     taskconfig.Rule
		json, path string
	}{
		{taskconfig.PathFilter(`\Photos\2024\`), `{"kind":"path","Prefix":"\\Photos\\2024\\"}`, "PHOTOS/2024/a"},
		{taskconfig.ExtensionFilter("*.JPG"), `{"kind":"extension","Extension":"*.JPG"}`, "a.jpg"},
		{taskconfig.WildcardFilter(`\Photos\**\*.raw\`), `{"kind":"wildcard","Pattern":"\\Photos\\**\\*.raw\\"}`, "photos/a.raw"},
	}
	for _, c := range cases {
		var decoded taskconfig.Rule
		if e := json.Unmarshal([]byte(c.json), &decoded); e != nil {
			t.Fatal(e)
		}
		match(t, c.direct, c.path, true)
		match(t, decoded, c.path, true)
	}
	// Canonical residual stars remain literal. Constructors/JSON normalize once;
	// direct Rule values are already canonical and must not be normalized again.
	var decoded taskconfig.Rule
	if e := json.Unmarshal([]byte(`{"kind":"extension","Extension":"*.*.jpg"}`), &decoded); e != nil {
		t.Fatal(e)
	}
	for _, r := range []taskconfig.Rule{decoded, taskconfig.ExtensionFilter("*.*.jpg"), {Kind: "extension", Extension: text("*.jpg")}} {
		match(t, r, "a.*.jpg", true)
		match(t, r, "a.jpg", false)
	}
	if m, e := Compile([]taskconfig.Rule{{Kind: "path", Prefix: text(`\raw\`)}}); m != nil || !errors.Is(e, ErrInvalidRule) {
		t.Fatal(m, e)
	}
}
func TestCompileEagerlyRejectsUnsupportedAndUnknown(t *testing.T) {
	for _, r := range []taskconfig.Rule{
		group("anyOf", taskconfig.Rule{Kind: "all"}, regex("[")),
		group("allOf", taskconfig.PathFilter(""), regex("^a")),
		not(group("anyOf", regex(`\d+`))),
	} {
		m, e := Compile([]taskconfig.Rule{r})
		var fe *Error
		if m != nil || !errors.Is(e, ErrUnsupportedRegex) || !errors.As(e, &fe) || !strings.Contains(fe.Field, "Filters[0]") {
			t.Fatal(m, e)
		}
	}
	var unknown taskconfig.Rule
	if e := json.Unmarshal([]byte(`{"kind":"all","FuturePolicy":9007199254740993}`), &unknown); e != nil {
		t.Fatal(e)
	}
	if m, e := Compile([]taskconfig.Rule{group("anyOf", taskconfig.Rule{Kind: "all"}, unknown)}); m != nil || !errors.Is(e, ErrUnknownFields) {
		t.Fatal(m, e)
	}
	for _, r := range []taskconfig.Rule{{Kind: "future"}, {Kind: "not"}, {Kind: "path"}, {Kind: "all", Pattern: text("unused")}} {
		if m, e := Compile([]taskconfig.Rule{r}); m != nil || !errors.Is(e, ErrInvalidRule) {
			t.Fatal(m, e)
		}
	}
	var nullRules []taskconfig.Rule
	if m, e := Compile([]taskconfig.Rule{{Kind: "allOf", Rules: &nullRules}}); m != nil || !errors.Is(e, ErrInvalidRule) {
		t.Fatal(m, e)
	}
	cyclic := taskconfig.Rule{Kind: "not"}
	cyclic.Rule = &cyclic
	if m, e := Compile([]taskconfig.Rule{cyclic}); m != nil || !errors.Is(e, ErrInvalidRule) {
		t.Fatal(m, e)
	}
}
func TestUnicodeScalarMatching(t *testing.T) {
	match(t, taskconfig.PathFilter("Ärger/照片"), "ärger/照片/a", true)
	match(t, taskconfig.WildcardFilter("日本/写真?.ＴＸＴ"), "日本/写真中.ｔｘｔ", true)
	match(t, taskconfig.WildcardFilter("?"), "😀", true)
	match(t, taskconfig.WildcardFilter("?"), "a\u0308", false) // Scalars, not grapheme clusters.
	match(t, taskconfig.Rule{Kind: "all"}, "ß/😀", true)        // No case comparison is needed.
	match(t, taskconfig.PathFilter("ß"), "SS/a", false)
	match(t, taskconfig.PathFilter("ﬅ"), "ﬆ/a", true)
	m, e := Compile([]taskconfig.Rule{{Kind: "all"}, taskconfig.ExtensionFilter("txt")})
	if e != nil {
		t.Fatal(e)
	}
	if got, e := m.Match("ß.txt"); !got || e != nil {
		t.Fatal(got, e)
	}
}
func TestMatcherOwnsInputsAndRejectsInvalidInput(t *testing.T) {
	r := taskconfig.ExtensionFilter("jpg")
	m, e := Compile([]taskconfig.Rule{r})
	if e != nil {
		t.Fatal(e)
	}
	*r.Extension = "txt"
	if got, e := m.Match("a.jpg"); e != nil || !got {
		t.Fatal("matcher retained mutable input", got, e)
	}
	for _, p := range []string{"a\x00.jpg", string([]byte{0xff})} {
		if got, e := m.Match(p); got || !errors.Is(e, ErrInvalidInput) {
			t.Fatal(got, e)
		}
	}
	var zero Matcher
	if _, e := zero.Match("a"); !errors.Is(e, ErrInvalidRule) {
		t.Fatal(e)
	}
}

func TestAllNonemptyRegexRemainUnverified(t *testing.T) {
	for _, pattern := range []string{`\d+`, `\w+`, `\b中文\b`, `(?i)k`, `(?-i)A`, `(?x)a b`, `(?mR)^foo$`, `(?s).`, `^a$`, `a\z`, `a*`, `[`, `(?<=a)b`, `(a)\1`, `[a-z&&[^aeiou]]`} {
		if m, e := Compile([]taskconfig.Rule{regex(pattern)}); m != nil || !errors.Is(e, ErrUnsupportedRegex) {
			t.Fatal(pattern, m, e)
		}
	}
}

func TestCompileNodeCountBoundary(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, count := range []int{4096, 4097} {
			t.Run(fmt.Sprintf("nested=%v/nodes=%d", nested, count), func(t *testing.T) {
				leafCount := count
				if nested {
					leafCount--
				} // The explicit group counts; the implicit destination OR does not.
				rules := make([]taskconfig.Rule, leafCount)
				for i := range rules {
					rules[i] = taskconfig.Rule{Kind: "all"}
				}
				if nested {
					rules = []taskconfig.Rule{group("anyOf", rules...)}
				}
				m, e := Compile(rules)
				if count > 4096 {
					var fe *Error
					if m != nil || !errors.Is(e, ErrInvalidRule) || !errors.As(e, &fe) {
						t.Fatal(m, e)
					}
					want := "Filters[4096]"
					if nested {
						want = "Filters[0].Rules[4095]"
					}
					if fe.Field != want {
						t.Fatalf("got field %q, want %q", fe.Field, want)
					}
				} else {
					if e != nil || m == nil {
						t.Fatal(m, e)
					}
					if got, e := m.Match("file.txt"); e != nil || !got {
						t.Fatal(got, e)
					}
				}
			})
		}
	}
}
