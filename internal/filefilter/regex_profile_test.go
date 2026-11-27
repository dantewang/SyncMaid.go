package filefilter

import (
	"encoding/json"
	"errors"
	"regexp/syntax"
	"strings"
	"sync"
	"testing"

	"syncmaid/internal/taskconfig"
)

func reviewFixture(tb testing.TB, rules ...taskconfig.Rule) taskconfig.Task {
	tb.Helper()
	ts, e := taskconfig.Decode([]byte(`[{"Name":"Synthetic","SourcePath":"C:\\Synthetic","Trigger":{"kind":"manual"},"Destinations":[{"Name":"Target","Target":{"kind":"local","Path":"D:\\Synthetic"},"Filters":[],"Strategy":"AddOnly"}]}]`))
	if e != nil {
		tb.Fatal(e)
	}
	ts[0].Destinations[0].Filters = rules
	return ts[0]
}
func proposal(tb testing.TB, task taskconfig.Task) RegexSpec {
	tb.Helper()
	ss, e := Proposals(task, task.Destinations[0].ID)
	if e != nil || len(ss) != 1 {
		tb.Fatalf("proposals: %v %v", ss, e)
	}
	return ss[0]
}
func confirmed(tb testing.TB, s RegexSpec) ReviewedRegex {
	tb.Helper()
	p, e := PrepareRegexPreview(s, []string{"sample"})
	if e != nil {
		tb.Fatal(e)
	}
	r, e := p.Confirm()
	if e != nil {
		tb.Fatal(e)
	}
	return r
}
func reviewedMatcher(tb testing.TB, pattern string) *Matcher {
	tb.Helper()
	task := reviewFixture(tb, regex("old"))
	s := proposal(tb, task)
	s.Pattern = pattern
	m, e := CompileReviewedTarget(task, task.Destinations[0].ID, []ReviewedRegex{confirmed(tb, s)})
	if e != nil {
		tb.Fatal(e)
	}
	return m
}

// Expectations describe the NEW profile only, not unmeasured Rust results.
func TestGoRegexProfileSemantics(t *testing.T) {
	cases := []struct {
		pattern, input string
		want           bool
	}{
		{`^a/b$`, `A\B`, true}, {`(?-i)^a$`, "A", false}, {`^a$`, "A", true},
		{`^\d+$`, "١", false}, {`^\w+$`, "中文", false}, {`\b中文\b`, "中文", false},
		{`^\p{Han}+$`, "中文", true}, {`^k$`, "K", true}, {`^ß$`, "SS", false},
		{`^σ$`, "ς", true}, {`^é$`, "e\u0301", false}, {`^a$`, "a\n", false},
		{`(?m)^a$`, "a\nb", true}, {`a.b`, "a\nb", false}, {`(?s)a.b`, "a\nb", true},
		{``, "", false}, {``, "abc", false}, {`a*`, "", true}, {`a*`, "zzz", true},
		{`^[a&&b]$`, "&", true},
	}
	for _, c := range cases {
		t.Run(c.pattern+"/"+c.input, func(t *testing.T) {
			m := reviewedMatcher(t, c.pattern)
			got, e := m.Match(c.input)
			if e != nil || got != c.want {
				t.Fatalf("got %v %v want %v", got, e, c.want)
			}
		})
	}
	// Non-regex scalar uppercase and Go's simple regex folding stay distinct.
	match(t, taskconfig.ExtensionFilter("k"), "a.K", false)
}

func TestRegexPreviewAndErrors(t *testing.T) {
	task := reviewFixture(t, regex("old"))
	s := proposal(t, task)
	for _, pattern := range []string{"[", `(?x)a`, `(?u)a`, `(?R)a`, `(?=a)`, `(a)\1`} {
		s.Pattern = pattern
		p, e := PrepareRegexPreview(s, []string{"a"})
		var fe *Error
		var se *syntax.Error
		if p != nil || !errors.Is(e, ErrRegexSyntax) || !errors.As(e, &fe) || !errors.As(e, &se) || !strings.Contains(fe.Field, "NewPattern") {
			t.Fatalf("%q: %v %v", pattern, p, e)
		}
	}
	s.Pattern = "^a/b$"
	p, e := PrepareRegexPreview(s, []string{`A\B`, "other"})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Warnings()) < 4 || !p.Samples()[0].Matched || p.Samples()[0].NormalizedInput != "A/B" {
		t.Fatal(p.Samples(), p.Warnings())
	}
	samples := p.Samples()
	samples[0].Matched = false
	warnings := p.Warnings()
	warnings[0] = "changed"
	if !p.Samples()[0].Matched || p.Warnings()[0] == "changed" {
		t.Fatal("preview aliases caller data")
	}
	copySpec := p.Spec()
	copySpec.Pattern = "other"
	r, e := p.Confirm()
	if e != nil || r.Spec.Pattern != "^a/b$" {
		t.Fatal(r, e)
	}
	for _, v := range []any{s, r} {
		if _, e := json.Marshal(v); !errors.Is(e, ErrProfilePersistence) {
			t.Fatal(e)
		}
	}
	if _, e := (*RegexPreview)(nil).Confirm(); !errors.Is(e, ErrNeedsReview) {
		t.Fatal(e)
	}
	if _, e := PrepareRegexPreview(RegexSpec{Profile: GoRegexpV1, Pattern: "a"}, []string{"a"}); !errors.Is(e, ErrNeedsReview) {
		t.Fatal(e)
	}
	for _, c := range []struct {
		p             RegexProfile
		engine, table string
	}{{"future", "go1.27.4", "17.0.0"}, {GoRegexpV1, "go1.27.3", "17.0.0"}, {GoRegexpV1, "go1.28.0", "17.0.0"}, {GoRegexpV1, "go1.27.4", "16.0.0"}} {
		if !errors.Is(checkRegexVersion(c.p, c.engine, c.table), ErrRegexProfile) {
			t.Fatal(c)
		}
	}
}

func TestRegexConfirmationBinding(t *testing.T) {
	task := reviewFixture(t, regex("old"))
	id := task.Destinations[0].ID
	s := proposal(t, task)
	s.Pattern = "^new$"
	r := confirmed(t, s)
	if m, e := CompileReviewedTarget(task, id, nil); m != nil || !errors.Is(e, ErrNeedsReview) {
		t.Fatal(m, e)
	}
	if m, e := Compile(task.Destinations[0].Filters); m != nil || !errors.Is(e, ErrUnsupportedRegex) {
		t.Fatal(m, e)
	}
	for _, edit := range []func(*taskconfig.Task){
		func(x *taskconfig.Task) { x.Name += "changed" }, func(x *taskconfig.Task) { x.SourcePath += "\\changed" },
		func(x *taskconfig.Task) { x.Destinations[0].Filters = []taskconfig.Rule{regex("edited")} },
		func(x *taskconfig.Task) { x.Destinations[0].Filters = []taskconfig.Rule{{Kind: "all"}, regex("old")} },
	} {
		copyTask := task
		copyTask.Destinations = append([]taskconfig.Destination(nil), task.Destinations...)
		edit(&copyTask)
		if m, e := CompileReviewedTarget(copyTask, id, []ReviewedRegex{r}); m != nil || !errors.Is(e, ErrStaleReview) {
			t.Fatal(m, e)
		}
	}
	for _, edit := range []func(*ReviewedRegex){func(x *ReviewedRegex) { x.Spec.Pattern = "edited" }, func(x *ReviewedRegex) { x.Spec.Profile = "future" }} {
		copyReceipt := r
		edit(&copyReceipt)
		if m, e := CompileReviewedTarget(task, id, []ReviewedRegex{copyReceipt}); m != nil || !errors.Is(e, ErrStaleReview) {
			t.Fatal(m, e)
		}
	}
	if m, e := CompileReviewedTarget(task, id, []ReviewedRegex{{Spec: s}}); m != nil || !errors.Is(e, ErrNeedsReview) {
		t.Fatal(m, e)
	}
	if m, e := CompileReviewedTarget(task, id, []ReviewedRegex{r, r}); m != nil || !errors.Is(e, ErrStaleReview) {
		t.Fatal(m, e)
	}
	m, e := CompileReviewedTarget(task, id, []ReviewedRegex{r})
	if e != nil {
		t.Fatal(e)
	}
	// Prepared matcher survives later input edits and supports concurrent reads.
	*task.Destinations[0].Filters[0].Pattern = "mutated"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				got, e := m.Match("NEW")
				if e != nil || !got {
					t.Errorf("%v %v", got, e)
				}
			}
		})
	}
	wg.Wait()
}

func TestRegexWholeTreeAndMoveBoundary(t *testing.T) {
	for _, rs := range [][]taskconfig.Rule{
		{{Kind: "all"}, regex("a")}, {group("allOf", regex(""), regex("a"))}, {not(group("anyOf", taskconfig.Rule{Kind: "all"}, not(regex("a"))))},
	} {
		task := reviewFixture(t, rs...)
		if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, nil); m != nil || !errors.Is(e, ErrNeedsReview) {
			t.Fatal(m, e)
		}
		ss, e := Proposals(task, task.Destinations[0].ID)
		if e != nil {
			t.Fatal(e)
		}
		var receipts []ReviewedRegex
		for _, s := range ss {
			receipts = append(receipts, confirmed(t, s))
		}
		if _, e := CompileReviewedTarget(task, task.Destinations[0].ID, receipts); e != nil {
			t.Fatal(e)
		}
	}
	task := reviewFixture(t, regex("a"))
	second := task.Destinations[0]
	second.ID = taskconfig.NewID()
	second.Filters = []taskconfig.Rule{{Kind: "all"}}
	task.Destinations = append(task.Destinations, second)
	if _, e := CompileReviewedTarget(task, second.ID, nil); e != nil {
		t.Fatal(e)
	}
	if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, nil); m != nil || !errors.Is(e, ErrNeedsReview) {
		t.Fatal(m, e)
	}
	task.Kind = taskconfig.Moving
	for i := range task.Destinations {
		task.Destinations[i].Strategy = taskconfig.Move
	}
	if ms, e := CompileReviewedMove(task, nil); ms != nil || !errors.Is(e, ErrNeedsReview) {
		t.Fatal(ms, e)
	}
	if m, e := CompileReviewedTarget(task, second.ID, nil); m != nil || !errors.Is(e, ErrInvalidRule) {
		t.Fatal("Move bypass", m, e)
	}
	r := confirmed(t, proposal(t, task))
	rs := map[taskconfig.ID][]ReviewedRegex{task.Destinations[0].ID: {r}}
	if ms, e := CompileReviewedMove(task, rs); e != nil || len(ms) != 2 {
		t.Fatal(ms, e)
	}
	task.Destinations[0].Strategy = taskconfig.Mirror
	task.Kind = taskconfig.Sync
	r = confirmed(t, proposal(t, task))
	if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, []ReviewedRegex{r}); m != nil || !errors.Is(e, ErrInvalidRule) {
		t.Fatal(m, e)
	}
}

func TestRegexOldSchemaIsolation(t *testing.T) {
	var rule taskconfig.Rule
	if e := json.Unmarshal([]byte(`{"kind":"regex","Pattern":"a","Engine":"go-v1"}`), &rule); e != nil {
		t.Fatal(e)
	}
	if m, e := Compile([]taskconfig.Rule{rule}); m != nil || !errors.Is(e, ErrUnknownFields) {
		t.Fatal(m, e)
	}
	task := reviewFixture(t, rule)
	r := confirmed(t, proposal(t, task))
	if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, []ReviewedRegex{r}); m != nil || !errors.Is(e, ErrUnknownFields) {
		t.Fatal(m, e)
	}
	if e := json.Unmarshal([]byte(`{"kind":"goRegexV1","Pattern":"a"}`), &rule); !errors.Is(e, taskconfig.ErrUnsupported) {
		t.Fatal(e)
	}
	match(t, regex(""), "anything", false)
	task = reviewFixture(t, regex(""))
	if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, nil); e != nil {
		t.Fatal(e)
	} else if got, e := m.Match(""); got || e != nil {
		t.Fatal(got, e)
	}
	// Direct Go cycles cannot overflow codec recursion.
	cyc := taskconfig.Rule{Kind: "not"}
	cyc.Rule = &cyc
	task = reviewFixture(t, cyc)
	if _, e := Proposals(task, task.Destinations[0].ID); !errors.Is(e, ErrRegexBudget) {
		t.Fatal(e)
	}
}

func TestReviewCanonicalInputAndExplicitEmpty(t *testing.T) {
	for _, r := range []taskconfig.Rule{{Kind: "path", Prefix: text(`\raw\`)}, {Kind: "wildcard", Pattern: text(`/raw/`)}} {
		task := reviewFixture(t, r, regex("old"))
		if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, nil); m != nil || !errors.Is(e, ErrInvalidRule) {
			t.Fatal(m, e)
		}
	}
	task := reviewFixture(t, regex("old"))
	s := proposal(t, task)
	s.Pattern = ""
	receipt := confirmed(t, s)
	m, e := CompileReviewedTarget(task, task.Destinations[0].ID, []ReviewedRegex{receipt})
	if e != nil {
		t.Fatal(e)
	}
	if yes, e := m.Match("anything"); yes || e != nil {
		t.Fatal(yes, e)
	}
}

func TestRegexBudgets(t *testing.T) {
	task := reviewFixture(t, regex(""))
	s := proposal(t, task)
	pattern := strings.Repeat("中", 2730) + "ab" // 8192 UTF-8 bytes, <4096 instructions.
	for _, c := range []struct {
		pattern string
		ok      bool
	}{
		{pattern, true}, {pattern + "x", false}, {strings.Repeat("a", 4094), true}, {strings.Repeat("a", 4095), false}, {strings.Repeat("a{1000}", 8), false},
	} {
		s.Pattern = c.pattern
		p, e := PrepareRegexPreview(s, []string{""})
		if c.ok {
			if e != nil || p.Cost().ProgramInstructions > MaxRegexProgramInstructions {
				t.Fatal(e)
			}
		} else if p != nil || !errors.Is(e, ErrRegexBudget) {
			t.Fatal(p, e)
		}
	}
	s.Pattern = "a"
	samples := make([]string, MaxPreviewSamples)
	if _, e := PrepareRegexPreview(s, samples); e != nil {
		t.Fatal(e)
	}
	if p, e := PrepareRegexPreview(s, append(samples, "")); p != nil || !errors.Is(e, ErrRegexBudget) {
		t.Fatal(p, e)
	}
	for _, ss := range [][]string{nil, {strings.Repeat("a", MaxPreviewBytes+1)}, {strings.Repeat("a", MaxRegexInputBytes+1)}, {"\x00"}, {string([]byte{0xff})}} {
		if p, e := PrepareRegexPreview(s, ss); p != nil || e == nil {
			t.Fatal(p, e)
		}
	}
	if _, e := PrepareRegexPreview(s, []string{strings.Repeat("a", MaxPreviewBytes)}); e != nil {
		t.Fatal(e)
	}
	// Input budget applies before OR short-circuit, even if all would match.
	task = reviewFixture(t, taskconfig.Rule{Kind: "all"}, regex("a"))
	r := confirmed(t, proposal(t, task))
	m, e := CompileReviewedTarget(task, task.Destinations[0].ID, []ReviewedRegex{r})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := m.Match(strings.Repeat("a", MaxRegexInputBytes)); e != nil {
		t.Fatal(e)
	}
	if got, e := m.Match(strings.Repeat("a", MaxRegexInputBytes+1)); got || !errors.Is(e, ErrRegexBudget) {
		t.Fatal(got, e)
	}
	// Each literal compiles to exactly 2048 instructions: target total 8192/10240.
	task = reviewFixture(t, regex(""), regex(""), regex(""), regex(""), regex(""))
	ss, e := Proposals(task, task.Destinations[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	var rs []ReviewedRegex
	for _, sp := range ss {
		sp.Pattern = strings.Repeat("a", 2046)
		rs = append(rs, confirmed(t, sp))
	}
	if _, e := CompileReviewedTarget(task, task.Destinations[0].ID, rs[:4]); e != nil {
		t.Fatal(e)
	}
	if m, e := CompileReviewedTarget(task, task.Destinations[0].ID, rs); m != nil || !errors.Is(e, ErrRegexBudget) {
		t.Fatal(m, e)
	}
}

func TestRegexInstructionBound(t *testing.T) {
	for _, pattern := range []string{"", "a", "(ab|cd)+", "a{0}", "a{0,10}", "a{3,}", "(a?)*", "(a?){0,}", "(a?){10}", "(?m)^x$", "(?:a|bc|def)*", "[^a]", `\b\B`, "a{0,1}", "(a|b){5,9}"} {
		p, e := compileGoRegex(GoRegexpV1, pattern, "Pattern")
		if e != nil {
			t.Fatal(e)
		}
		if p.cost.EstimatedInstructions < p.cost.ProgramInstructions {
			t.Fatalf("%s: %+v", pattern, p.cost)
		}
	}
}

func BenchmarkGoRegexPreviewBudget(b *testing.B) {
	task := reviewFixture(b, regex("old"))
	s := proposal(b, task)
	s.Pattern = "^(a?){400}a*b$"
	samples := []string{strings.Repeat("a", MaxPreviewBytes)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, e := PrepareRegexPreview(s, samples); e != nil {
			b.Fatal(e)
		}
	}
}
func BenchmarkGoRegexMatchBudget(b *testing.B) {
	for _, pattern := range []string{"^(a?){400}a*b$", "^(a?){400}[ab]*[cd]$"} {
		b.Run(pattern, func(b *testing.B) {
			m := reviewedMatcher(b, pattern)
			input := strings.Repeat("a", MaxRegexInputBytes)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, e := m.Match(input); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
