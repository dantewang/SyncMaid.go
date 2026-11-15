package filefilter

import (
	"fmt"
	"syncmaid/internal/taskconfig"
)

// RegexConfirmation is the versioned persisted decision, not a serialized
// program or a new decision inferred from compiling a legacy expression.
type RegexConfirmation struct {
	DecisionSHA256 string
	Version        int
	Origin         RegexOrigin
	Profile        RegexProfile
	Pattern        string
}

func (r ReviewedRegex) Confirmation() (RegexConfirmation, error) {
	if r.program == nil || r.digest != specDigest(r.Spec) {
		return RegexConfirmation{}, ErrStaleReview
	}
	return RegexConfirmation{Version: 1, Origin: r.Spec.Origin(), Profile: r.Spec.Profile, Pattern: r.Spec.Pattern, DecisionSHA256: fmt.Sprintf("%x", r.digest)}, nil
}

// RestoreConfirmation must only consume a saved decision from the Go document.
// It rebinds the whole task and recompiles; it never confirms an arbitrary spec.
func RestoreConfirmation(task taskconfig.Task, saved RegexConfirmation) (ReviewedRegex, error) {
	if saved.Version != 1 {
		return ReviewedRegex{}, ErrStaleReview
	}
	specs, err := Proposals(task, saved.Origin.DestinationID)
	if err != nil {
		return ReviewedRegex{}, err
	}
	for _, spec := range specs {
		if spec.Origin() != saved.Origin {
			continue
		}
		spec.Pattern, spec.Profile = saved.Pattern, saved.Profile
		if saved.DecisionSHA256 != fmt.Sprintf("%x", specDigest(spec)) {
			return ReviewedRegex{}, ErrStaleReview
		}
		program, err := compileGoRegex(spec.Profile, spec.Pattern, spec.source.field)
		if err != nil {
			return ReviewedRegex{}, err
		}
		return ReviewedRegex{Spec: spec, digest: specDigest(spec), program: program}, nil
	}
	return ReviewedRegex{}, ErrStaleReview
}
