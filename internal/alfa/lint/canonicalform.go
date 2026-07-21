package lint

import "code.linenisgreat.com/doppelgang/internal/0/nixedit"

// CanonicalFormFinding reports that an opted-in flake's top-level `inputs`
// attribute set has one or more inputs whose bindings (url, follows,
// nested sub-attrset) are not contiguous — see nixedit.CanonicalForm and
// FDR 0007. A flake that has not opted in via the `# canonical-form`
// sentinel comment never produces this finding.
type CanonicalFormFinding struct {
	// Scattered lists, in first-appearance order, the names of inputs
	// whose bindings are not contiguous under `inputs`.
	Scattered []string
}

// ClassifyCanonicalForm evaluates flakeNix's canonical-form opt-in and
// binding contiguity, returning a finding when it has opted in and has
// scattered inputs, or nil otherwise (not opted in, or already canonical).
func ClassifyCanonicalForm(flakeNix []byte) (*CanonicalFormFinding, error) {
	report, err := nixedit.CanonicalForm(flakeNix)
	if err != nil {
		return nil, err
	}
	if !report.Enabled || len(report.Scattered) == 0 {
		return nil, nil
	}
	return &CanonicalFormFinding{Scattered: report.Scattered}, nil
}
