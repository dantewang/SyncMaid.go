package filefilter

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"runtime"
	"unicode"

	"syncmaid/internal/taskconfig"
)

const maxReviewDocumentBytes = 1 << 20
const maxReviewDestinations = 128

type regexSource struct {
	task, destination taskconfig.ID
	field, legacy     string
	document          [32]byte
}

func (s regexSource) valid() bool { return s.task != "" && s.destination != "" && s.field != "" }

type RegexOrigin struct {
	TaskID, DestinationID                taskconfig.ID
	Field, LegacyPattern, DocumentSHA256 string
}

func (s regexSource) info() RegexOrigin {
	return RegexOrigin{s.task, s.destination, s.field, s.legacy, fmt.Sprintf("%x", s.document)}
}

type reviewTask struct {
	task   taskconfig.Task
	digest [32]byte
}

// Snapshot the task so later caller edits cannot change either the binding or
// what gets compiled. Callers must not concurrently mutate input during copying.
// This is shape/binding validation, not taskconfig's execution/FS validation.
func snapshotReviewTask(task taskconfig.Task) (reviewTask, error) {
	if len(task.Destinations) > maxReviewDestinations {
		return reviewTask{}, &Error{Field: "Destinations", Err: ErrRegexBudget}
	}
	count := 0
	for _, d := range task.Destinations {
		if err := walkReviewRules(d.Filters, "Filters", 0, &count, nil); err != nil {
			return reviewTask{}, err
		}
	}
	b, err := taskconfig.Encode([]taskconfig.Task{task})
	if err != nil {
		return reviewTask{}, err
	}
	if len(b) > maxReviewDocumentBytes {
		return reviewTask{}, &Error{Field: "Task", Err: ErrRegexBudget}
	}
	ts, err := taskconfig.Decode(b)
	if err != nil {
		return reviewTask{}, err
	}
	canonical, err := taskconfig.Encode(ts)
	if err != nil {
		return reviewTask{}, err
	}
	if !bytes.Equal(b, canonical) {
		return reviewTask{}, &Error{Field: "Task", Err: fmt.Errorf("%w: use decoded or canonical model values", ErrInvalidRule)}
	}
	seen := map[taskconfig.ID]bool{}
	for _, d := range ts[0].Destinations {
		if seen[d.ID] {
			return reviewTask{}, &Error{Field: "Destinations.Id", Err: ErrInvalidRule}
		}
		seen[d.ID] = true
	}
	return reviewTask{task: ts[0], digest: sha256.Sum256(b)}, nil
}

// Proposals discovers old regex nodes but never confirms or compiles them for
// execution. Pattern initially copies the legacy spelling for a user's review;
// it may require editing before the new profile will even compile.
func Proposals(task taskconfig.Task, destination taskconfig.ID) ([]RegexSpec, error) {
	t, err := snapshotReviewTask(task)
	if err != nil {
		return nil, err
	}
	return t.proposals(destination)
}
func (t reviewTask) destination(id taskconfig.ID) (int, error) {
	for i, d := range t.task.Destinations {
		if d.ID == id {
			return i, nil
		}
	}
	return 0, &Error{Field: "Destination.Id", Err: ErrInvalidRule}
}
func (t reviewTask) proposals(id taskconfig.ID) ([]RegexSpec, error) {
	i, err := t.destination(id)
	if err != nil {
		return nil, err
	}
	var out []RegexSpec
	count := 0
	err = walkReviewRules(t.task.Destinations[i].Filters, fmt.Sprintf("Destinations[%d].Filters", i), 0, &count, func(field string, r taskconfig.Rule) {
		if r.Kind == "regex" && r.Pattern != nil {
			source := regexSource{task: t.task.ID, destination: id, field: field, legacy: *r.Pattern, document: t.digest}
			out = append(out, RegexSpec{Profile: GoRegexpV1, Pattern: *r.Pattern, source: source})
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func walkReviewRules(rules []taskconfig.Rule, base string, depth int, count *int, visit func(string, taskconfig.Rule)) error {
	for i, r := range rules {
		if err := walkReviewNode(r, fmt.Sprintf("%s[%d]", base, i), depth, count, visit); err != nil {
			return err
		}
	}
	return nil
}
func walkReviewNode(r taskconfig.Rule, field string, depth int, count *int, visit func(string, taskconfig.Rule)) error {
	*count = *count + 1
	if depth > 128 || *count > 4096 {
		return &Error{Field: field, Err: ErrRegexBudget}
	}
	if visit != nil {
		visit(field, r)
	}
	// Walk invalid payloads too: direct Go cycles must fail before JSON encoding.
	if r.Rules != nil {
		if err := walkReviewRules(*r.Rules, field+".Rules", depth+1, count, visit); err != nil {
			return err
		}
	}
	if r.Rule != nil {
		return walkReviewNode(*r.Rule, field+".Rule", depth+1, count, visit)
	}
	return nil
}

// CompileReviewedTarget prepares the whole destination or returns an error,
// never a successful zero-file result for a blocked Sync target. Full task/FS
// validation remains separate. All reviews are scoped to this exact task copy.
// Moving tasks must use CompileReviewedMove; a per-target call cannot bypass
// their all-destinations review gate.
func CompileReviewedTarget(task taskconfig.Task, destination taskconfig.ID, reviews []ReviewedRegex) (*Matcher, error) {
	t, err := snapshotReviewTask(task)
	if err != nil {
		return nil, err
	}
	if t.task.Kind != taskconfig.Sync {
		return nil, &Error{Field: "Kind", Err: fmt.Errorf("%w: use CompileReviewedMove for Move", ErrInvalidRule)}
	}
	return t.compileTarget(destination, reviews)
}
func (t reviewTask) compileTarget(id taskconfig.ID, reviews []ReviewedRegex) (*Matcher, error) {
	i, err := t.destination(id)
	if err != nil {
		return nil, err
	}
	d := t.task.Destinations[i]
	if d.Strategy == taskconfig.Mirror && (len(d.Filters) != 1 || d.Filters[0].Kind != "all") {
		return nil, &Error{Field: fmt.Sprintf("Destinations[%d].Filters", i), Err: ErrInvalidRule}
	}
	specs, err := t.proposals(id)
	if err != nil {
		return nil, err
	}
	sources := map[string]regexSource{}
	for _, s := range specs {
		sources[s.source.field] = s.source
	}
	approved := map[string]*goRegexProgram{}
	for _, r := range reviews {
		field := r.Spec.source.field
		if expected, ok := sources[field]; !ok || r.Spec.source != expected {
			return nil, &Error{Field: field, Err: ErrStaleReview}
		}
		if _, duplicate := approved[field]; duplicate {
			return nil, &Error{Field: field, Err: ErrStaleReview}
		}
		if r.program == nil {
			return nil, &Error{Field: field, Err: ErrNeedsReview}
		}
		if r.digest != specDigest(r.Spec) {
			return nil, &Error{Field: field, Err: ErrStaleReview}
		}
		if err := checkRegexVersion(r.Spec.Profile, runtime.Version(), unicode.Version); err != nil {
			return nil, &Error{Field: field, Err: err}
		}
		approved[field] = r.program
	}
	return compileWithRegex(d.Filters, fmt.Sprintf("Destinations[%d].Filters", i), func(field, legacy string) (preparedRegex, error) {
		if p, ok := approved[field]; ok {
			return preparedRegex{kind: regexGo, program: p}, nil
		}
		if legacy == "" {
			return preparedRegex{kind: regexNone}, nil
		}
		return preparedRegex{}, &Error{Field: field, Pattern: legacy, Err: ErrNeedsReview}
	})
}

// CompileReviewedMove is only a preparation contract, NOT routing/execution.
// Any unconfirmed/invalid destination returns NO matcher list, so a future
// caller cannot skip it and send its files into a later catch-all destination.
func CompileReviewedMove(task taskconfig.Task, reviews map[taskconfig.ID][]ReviewedRegex) ([]*Matcher, error) {
	t, err := snapshotReviewTask(task)
	if err != nil {
		return nil, err
	}
	if t.task.Kind != taskconfig.Moving {
		return nil, &Error{Field: "Kind", Err: ErrInvalidRule}
	}
	for id := range reviews {
		if _, err := t.destination(id); err != nil {
			return nil, &Error{Field: "Reviews", Err: ErrStaleReview}
		}
	}
	out := make([]*Matcher, 0, len(t.task.Destinations))
	for _, d := range t.task.Destinations {
		if d.Strategy != taskconfig.Move {
			return nil, &Error{Field: "Strategy", Err: ErrInvalidRule}
		}
		m, err := t.compileTarget(d.ID, reviews[d.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
