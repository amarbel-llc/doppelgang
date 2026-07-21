package lint

import "code.linenisgreat.com/doppelgang/internal/0/nixedit"

// CanonicalFormFinding reports that an opted-in flake's top-level `inputs`
// attribute set has one or more inputs whose bindings (url, follows,
// nested sub-attrset) are not contiguous, and/or that it opts in via the
// deprecated `# canonical-form` sentinel rather than the structured
// `# doppelgang: canonical` directive — see nixedit.CanonicalForm and
// FDR 0007. A flake that has not opted in via either spelling never
// produces this finding.
type CanonicalFormFinding struct {
	// Scattered lists, in first-appearance order, the names of inputs
	// whose bindings are not contiguous under `inputs`.
	Scattered []string
	// LegacySentinel reports that the flake opts in via the deprecated
	// `# canonical-form` spelling; `--fix` rewrites it to the structured
	// `# doppelgang: canonical` directive.
	LegacySentinel bool
}

// ClassifyCanonicalForm evaluates flakeNix's canonical-form opt-in and
// binding contiguity, returning a finding when it has opted in and either
// has scattered inputs or uses the deprecated sentinel spelling, or nil
// otherwise (not opted in, or already canonical with the structured
// directive).
func ClassifyCanonicalForm(flakeNix []byte) (*CanonicalFormFinding, error) {
	report, err := nixedit.CanonicalForm(flakeNix)
	if err != nil {
		return nil, err
	}
	if !report.Enabled || (len(report.Scattered) == 0 && !report.LegacySentinel) {
		return nil, nil
	}
	return &CanonicalFormFinding{Scattered: report.Scattered, LegacySentinel: report.LegacySentinel}, nil
}
