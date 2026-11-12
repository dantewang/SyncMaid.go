package filefilter

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

type RegexProfile string

// This profile pins behavior and budgets, not just the dependency-free engine.
// New toolchain/table semantics require a new profile and renewed review.
// Matching is unanchored unless the pattern supplies anchors, defaults to (?i)
// (which inline (?-i) can disable), and changes only INPUT backslashes to slashes.
// No dot-segment cleanup, uppercase preprocessing or Unicode normalization is
// applied. Empty pattern is deliberately constant-none, unlike regexp's empty
// expression; other empty-matching expressions retain their Go meaning.
const GoRegexpV1 RegexProfile = "go-regexp-1.27.3-ucd17-v1"
const (
	MaxRegexPatternBytes        = 8 << 10
	MaxRegexProgramInstructions = 4096
	MaxTargetRegexInstructions  = 8192
	MaxRegexInputBytes          = 128 << 10
	MaxPreviewSamples           = 16
	MaxPreviewBytes             = 32 << 10
)

var (
	ErrRegexProfile       = errors.New("unsupported regex profile or engine version")
	ErrRegexSyntax        = errors.New("invalid Go regex syntax")
	ErrRegexBudget        = errors.New("regex resource budget exceeded")
	ErrNeedsReview        = errors.New("regex requires explicit preview confirmation")
	ErrStaleReview        = errors.New("regex confirmation no longer matches its source or proposal")
	ErrProfilePersistence = errors.New("regex profile persistence is not designed or connected")
)

// RegexSpec is an editable proposal, never execution permission. Only Proposals
// supplies its opaque source binding; changing Pattern/Profile invalidates an
// earlier confirmation. It deliberately has no persisted representation yet.
type RegexSpec struct {
	Profile RegexProfile
	Pattern string
	source  regexSource
}

func (s RegexSpec) Origin() RegexOrigin        { return s.source.info() }
func (RegexSpec) MarshalJSON() ([]byte, error) { return nil, ErrProfilePersistence }

type RegexCost struct{ PatternBytes, EstimatedInstructions, ProgramInstructions int }
type RegexSample struct {
	Input, NormalizedInput string
	Matched                bool
}
type RegexPreview struct {
	spec     RegexSpec
	program  *goRegexProgram
	samples  []RegexSample
	warnings []string
}

func (p *RegexPreview) Spec() RegexSpec        { return p.spec }
func (p *RegexPreview) Samples() []RegexSample { return append([]RegexSample(nil), p.samples...) }
func (p *RegexPreview) Warnings() []string     { return append([]string(nil), p.warnings...) }
func (p *RegexPreview) Cost() RegexCost        { return p.program.cost }

// ReviewedRegex is an in-memory confirmation receipt, NOT proof of a person's
// identity or deletion permission. A future UI/coordinator must call Confirm
// only on explicit user acceptance. JSON cannot restore its private capability.
type ReviewedRegex struct {
	Spec    RegexSpec
	digest  [32]byte
	program *goRegexProgram
}

func (ReviewedRegex) MarshalJSON() ([]byte, error) { return nil, ErrProfilePersistence }
func (p *RegexPreview) Confirm() (ReviewedRegex, error) {
	if p == nil || p.program == nil || !p.spec.source.valid() {
		return ReviewedRegex{}, ErrNeedsReview
	}
	return ReviewedRegex{Spec: p.spec, digest: specDigest(p.spec), program: p.program}, nil
}

func specDigest(s RegexSpec) [32]byte {
	b, _ := json.Marshal(struct {
		Origin  RegexOrigin
		Profile RegexProfile
		Pattern string
	}{s.Origin(), s.Profile, s.Pattern})
	return sha256.Sum256(b)
}
func checkRegexVersion(profile RegexProfile, engine, table string) error {
	if profile != GoRegexpV1 || engine != "go1.27.3" || table != "17.0.0" {
		return fmt.Errorf("%w: profile=%q Go=%q Unicode=%q", ErrRegexProfile, profile, engine, table)
	}
	return nil
}

// PrepareRegexPreview evaluates ONLY the proposed Go behavior on finite samples.
// It never computes old Rust results or claims equivalence, and does not return
// an executable task matcher. All sample limits are checked before any results.
func PrepareRegexPreview(spec RegexSpec, samples []string) (*RegexPreview, error) {
	field := spec.source.field
	if !spec.source.valid() {
		return nil, &Error{Field: "Source", Err: ErrNeedsReview}
	}
	if len(samples) < 1 || len(samples) > MaxPreviewSamples {
		return nil, &Error{Field: field + ".Samples", Err: ErrRegexBudget}
	}
	total := 0
	for i, s := range samples {
		if err := checkRegexInput(s, fmt.Sprintf("%s.Samples[%d]", field, i)); err != nil {
			return nil, err
		}
		total += len(s)
		if total > MaxPreviewBytes {
			return nil, &Error{Field: field + ".Samples", Err: fmt.Errorf("%w: total sample bytes > %d", ErrRegexBudget, MaxPreviewBytes)}
		}
	}
	program, err := compileGoRegex(spec.Profile, spec.Pattern, field+".NewPattern")
	if err != nil {
		return nil, err
	}
	p := &RegexPreview{spec: spec, program: program, warnings: []string{
		"Only this new Go profile is evaluated; no measured Rust results or equivalence proof are provided.",
		"Go Perl classes and word boundaries are ASCII. Case folding and Unicode properties use the pinned Go tables.",
		"Matching defaults to ignore case, folds backslashes to slashes, and performs no Unicode normalization.",
		"Rust flags, character-set operations and accepted/rejected syntax can differ even when the pattern compiles.",
	}}
	if spec.Pattern == "" {
		p.warnings = append(p.warnings, "Empty pattern explicitly selects nothing.")
	} else if program.re.MatchString("") {
		p.warnings = append(p.warnings, "The new pattern matches empty input; this does not prove it selects every path.")
	}
	for _, s := range samples {
		normal := strings.ReplaceAll(s, `\`, "/")
		p.samples = append(p.samples, RegexSample{Input: s, NormalizedInput: normal, Matched: program.re.MatchString(normal)})
	}
	return p, nil
}

type goRegexProgram struct {
	re   *regexp.Regexp
	cost RegexCost
}

func checkRegexInput(s, field string) error {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return &Error{Field: field, Err: ErrInvalidInput}
	}
	if len(s) > MaxRegexInputBytes {
		return &Error{Field: field, Err: fmt.Errorf("%w: input bytes > %d", ErrRegexBudget, MaxRegexInputBytes)}
	}
	return nil
}
func compileGoRegex(profile RegexProfile, pattern, field string) (*goRegexProgram, error) {
	fail := func(err error) (*goRegexProgram, error) { return nil, &Error{Field: field, Pattern: pattern, Err: err} }
	if err := checkRegexVersion(profile, runtime.Version(), unicode.Version); err != nil {
		return fail(err)
	}
	if len(pattern) > MaxRegexPatternBytes {
		return fail(fmt.Errorf("%w: pattern bytes > %d", ErrRegexBudget, MaxRegexPatternBytes))
	}
	if !utf8.ValidString(pattern) || strings.ContainsRune(pattern, 0) {
		return fail(ErrInvalidRule)
	}
	effective := pattern
	if pattern == "" {
		effective = `\b\B`
	} // Explicit constant-none policy, never a nil program or empty-engine all-match.
	effective = "(?i)" + effective
	ast, err := syntax.Parse(effective, syntax.Perl)
	if err != nil {
		return fail(errors.Join(ErrRegexSyntax, err))
	}
	estimated := instructionBound(ast, MaxRegexProgramInstructions-2) + 2 // Fail+Match instructions.
	if estimated > MaxRegexProgramInstructions {
		return fail(fmt.Errorf("%w: AST instruction bound > %d", ErrRegexBudget, MaxRegexProgramInstructions))
	}
	prog, err := syntax.Compile(ast.Simplify())
	if err != nil {
		return fail(errors.Join(ErrRegexSyntax, err))
	}
	if len(prog.Inst) > MaxRegexProgramInstructions {
		return fail(fmt.Errorf("%w: compiled instructions > %d", ErrRegexBudget, MaxRegexProgramInstructions))
	}
	re, err := regexp.Compile(effective)
	if err != nil {
		return fail(errors.Join(ErrRegexSyntax, err))
	}
	return &goRegexProgram{re: re, cost: RegexCost{PatternBytes: len(pattern), EstimatedInstructions: estimated, ProgramInstructions: len(prog.Inst)}}, nil
}

// A saturating conservative cost of the STANDARD LIBRARY AST, before Simplify
// can expand counted repetitions. This is not a second regex parser. Rejection
// can be conservative; the real program size is checked as well after simplify.
func instructionBound(r *syntax.Regexp, limit int) int {
	add := func(a, b int) int {
		if a > limit || b > limit || a > limit-b {
			return limit + 1
		}
		return a + b
	}
	mul := func(a, b int) int {
		if a > limit || b > limit || a != 0 && b > limit/a {
			return limit + 1
		}
		return a * b
	}
	child := func() int {
		if len(r.Sub) != 1 {
			return limit + 1
		}
		return instructionBound(r.Sub[0], limit)
	}
	switch r.Op {
	case syntax.OpNoMatch, syntax.OpEmptyMatch, syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 1
	case syntax.OpLiteral:
		return min(max(1, len(r.Rune)), limit+1)
	case syntax.OpCapture:
		return add(child(), 2)
	case syntax.OpStar:
		// A nullable operand needs both plus and quest in syntax/compiler.star.
		return add(child(), 2)
	case syntax.OpPlus, syntax.OpQuest:
		return add(child(), 1)
	case syntax.OpRepeat:
		if r.Max == 0 {
			return 1
		}
		c := child()
		if r.Max < 0 {
			return add(mul(c, max(1, r.Min)), 2)
		}
		return add(mul(c, r.Max), r.Max-r.Min)
	case syntax.OpConcat, syntax.OpAlternate:
		n := 0
		for _, s := range r.Sub {
			n = add(n, instructionBound(s, limit))
			if n > limit {
				return n
			}
		}
		if r.Op == syntax.OpAlternate {
			n = add(n, max(0, len(r.Sub)-1))
		}
		return max(1, n)
	default:
		return limit + 1 // New AST constructs are not silently admitted to a frozen profile.
	}
}
