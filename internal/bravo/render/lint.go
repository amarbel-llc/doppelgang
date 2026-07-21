package render

import (
	"encoding/json"
	"fmt"
	"io"

	"code.linenisgreat.com/doppelgang/internal/alfa/lint"
)

// LintSummary is the renderer input for `doppelgang lint`: the flake.lock
// follows / multi-version findings, plus the Selection of which checks to
// render. A nil/empty Selection renders all three checks (the default and
// the back-compatible behavior); see active.
type LintSummary struct {
	Report    lint.Report
	Selection lint.Selection
}

// renderDefaultSelection is the empty-Selection fallback, computed once
// (active runs several times per render, so we avoid rebuilding the map each
// call). Read-only here — never mutated.
var renderDefaultSelection = lint.DefaultSelection()

// active reports whether check c should be rendered. An empty Selection
// means the DefaultChecks subset (the same fallback the absent --checks
// default resolves to), so a caller that leaves Selection unset gets the
// three default checks — not the opt-in nixpkgs-master convention check.
func (s LintSummary) active(c lint.Check) bool {
	if len(s.Selection) == 0 {
		return renderDefaultSelection.Has(c)
	}
	return s.Selection.Has(c)
}

// LintText writes the human-readable lint summary. Only the sections for
// the checks active in s.Selection are written.
func LintText(w io.Writer, s LintSummary) error {
	if s.active(lint.CheckFollows) {
		if _, err := fmt.Fprintf(w, "── follows opportunities ──\n"); err != nil {
			return err
		}
		if len(s.Report.Follows) == 0 {
			if _, err := fmt.Fprintln(w, "(no input pins an identical source more than once)"); err != nil {
				return err
			}
		}
		for _, r := range s.Report.Follows {
			if _, err := fmt.Fprintf(w, "%s pinned %d× — collapse onto %q:\n",
				r.Identity, r.NodeCount, r.Canonical); err != nil {
				return err
			}
			for _, line := range r.Lines {
				if _, err := fmt.Fprintf(w, "    %s\n", line); err != nil {
					return err
				}
			}
		}
	}

	if s.active(lint.CheckMultiVersion) {
		if _, err := fmt.Fprintf(w, "\n── multi-version inputs ──\n"); err != nil {
			return err
		}
		if len(s.Report.MultiVersion) == 0 {
			if _, err := fmt.Fprintln(w, "(no source repository is pinned at more than one rev)"); err != nil {
				return err
			}
		}
		for _, mv := range s.Report.MultiVersion {
			parts := make([]string, 0, len(mv.Versions))
			for _, v := range mv.Versions {
				parts = append(parts, fmt.Sprintf("%s via %s", shortRev(v.Rev), v.Path))
			}
			if _, err := fmt.Fprintf(w, "%s: %d revs (%s)\n",
				mv.Source, len(mv.Versions), truncList(parts, 8)); err != nil {
				return err
			}
		}
	}

	if s.active(lint.CheckDeadOverrides) {
		if _, err := fmt.Fprintf(w, "\n── dead follows overrides ──\n"); err != nil {
			return err
		}
		if len(s.Report.DeadOverrides) == 0 {
			if _, err := fmt.Fprintln(w, "(no follows override targets a non-existent input)"); err != nil {
				return err
			}
		}
		for _, d := range s.Report.DeadOverrides {
			if _, err := fmt.Fprintf(w, "%s%s: %q has no input %q (%s)\n",
				deadVia(d), d.Override, d.Target, d.Input, deadTag(d)); err != nil {
				return err
			}
		}
	}

	if s.active(lint.CheckNixpkgsMaster) {
		if _, err := fmt.Fprintf(w, "\n── nixpkgs-master convention ──\n"); err != nil {
			return err
		}
		if s.Report.NixpkgsMaster == nil {
			if _, err := fmt.Fprintln(w, "(nixpkgs-master is pinned to github:NixOS/nixpkgs/<40-hex sha>)"); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(w, nixpkgsMasterLine(*s.Report.NixpkgsMaster)); err != nil {
			return err
		}
	}

	if s.active(lint.CheckCanonicalInputs) {
		if _, err := fmt.Fprintf(w, "\n── canonical inputs ──\n"); err != nil {
			return err
		}
		if len(s.Report.CanonicalInputs) == 0 {
			if _, err := fmt.Fprintln(w, "(all inputs point at their PAPI-canonical URLs, or papi domain not set)"); err != nil {
				return err
			}
		}
		for _, f := range s.Report.CanonicalInputs {
			if _, err := fmt.Fprintf(w, "%s: %q → %q\n", f.Input, f.CurrentURL, f.CanonicalURL); err != nil {
				return err
			}
		}
	}

	if s.active(lint.CheckCanonicalForm) {
		if _, err := fmt.Fprintf(w, "\n── canonical inputs-block form ──\n"); err != nil {
			return err
		}
		if s.Report.CanonicalForm == nil {
			if _, err := fmt.Fprintln(w, "(not opted in via `# canonical-form`, or every input's bindings are contiguous)"); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "scattered inputs (bindings not contiguous): %s\n",
				truncList(s.Report.CanonicalForm.Scattered, 8)); err != nil {
				return err
			}
		}
	}

	return nil
}

// nixpkgsMasterLine renders the precise diagnostic for a non-conformant
// nixpkgs-master input, one per failure mode.
func nixpkgsMasterLine(f lint.NixpkgsMasterFinding) string {
	switch f.Status {
	case lint.NixpkgsMasterMissing:
		return "nixpkgs-master input missing: declare `nixpkgs-master.url = \"github:NixOS/nixpkgs/<40-hex sha>\"`"
	case lint.NixpkgsMasterFloating:
		return fmt.Sprintf("nixpkgs-master floating: %q is not pinned to a 40-hex revision (want github:NixOS/nixpkgs/<40-hex sha>)", f.URL)
	case lint.NixpkgsMasterNonGithub:
		return fmt.Sprintf("nixpkgs-master non-github: %q is not a github:NixOS/nixpkgs/<40-hex sha> ref", f.URL)
	default:
		return "nixpkgs-master: non-conformant"
	}
}

// deadTag renders the direct/transitive tag for a dead override, including
// the upstream-fix hint for transitive ones.
func deadTag(d lint.DeadOverride) string {
	if d.Direct {
		return "direct"
	}
	if d.Via != "" {
		return "transitive; fix in " + d.Via
	}
	return "transitive"
}

// deadVia renders the optional "<upstream> → " prefix for a transitive dead
// override, naming the flake whose flake.nix carries the binding.
func deadVia(d lint.DeadOverride) string {
	if d.Via == "" {
		return ""
	}
	return d.Via + " → "
}

// LintJSON writes the lint summary as a single indented JSON document.
func LintJSON(w io.Writer, s LintSummary) error {
	type jsonFollows struct {
		Identity  string   `json:"identity"`
		Canonical string   `json:"canonical"`
		Lines     []string `json:"lines"`
	}
	type jsonVersion struct {
		Rev  string `json:"rev"`
		Path string `json:"path"`
	}
	type jsonMulti struct {
		Source   string        `json:"source"`
		Versions []jsonVersion `json:"versions"`
	}
	type jsonDead struct {
		Override string `json:"override"`
		Target   string `json:"target"`
		Input    string `json:"input"`
		Direct   bool   `json:"direct"`
		Via      string `json:"via,omitempty"`
	}
	type jsonNixpkgsMaster struct {
		// Conformant is true when the input is pinned to the convention.
		Conformant bool `json:"conformant"`
		// Status and URL describe the non-conformance; omitted when
		// conformant (there is nothing to report).
		Status string `json:"status,omitempty"`
		URL    string `json:"url,omitempty"`
	}
	type jsonCanonicalInput struct {
		Input        string `json:"input"`
		CurrentURL   string `json:"currentURL"`
		CanonicalURL string `json:"canonicalURL"`
	}
	type jsonCanonicalForm struct {
		// Canonical is true when every input's bindings are contiguous (or
		// the flake has not opted in — see LintText's identical framing).
		Canonical bool     `json:"canonical"`
		Scattered []string `json:"scattered,omitempty"`
	}
	// Pointer fields with omitempty so a *deselected* check is absent from
	// the document (nil pointer omitted), while a *selected* check with no
	// findings still renders (an empty `[]` for the slice checks, or a
	// `{"conformant":true}` object for nixpkgs-master). This lets a consumer
	// distinguish "not checked" from "checked, clean".
	out := struct {
		Follows         *[]jsonFollows        `json:"followsOpportunities,omitempty"`
		MultiVersion    *[]jsonMulti          `json:"multiVersionInputs,omitempty"`
		DeadOverrides   *[]jsonDead           `json:"deadOverrides,omitempty"`
		NixpkgsMaster   *jsonNixpkgsMaster    `json:"nixpkgsMaster,omitempty"`
		CanonicalInputs *[]jsonCanonicalInput `json:"canonicalInputs,omitempty"`
		CanonicalForm   *jsonCanonicalForm    `json:"canonicalForm,omitempty"`
	}{}
	if s.active(lint.CheckFollows) {
		follows := make([]jsonFollows, 0, len(s.Report.Follows))
		for _, r := range s.Report.Follows {
			follows = append(follows, jsonFollows{
				Identity: r.Identity, Canonical: r.Canonical, Lines: r.Lines,
			})
		}
		out.Follows = &follows
	}
	if s.active(lint.CheckMultiVersion) {
		multi := make([]jsonMulti, 0, len(s.Report.MultiVersion))
		for _, mv := range s.Report.MultiVersion {
			jm := jsonMulti{Source: mv.Source, Versions: make([]jsonVersion, 0, len(mv.Versions))}
			for _, v := range mv.Versions {
				jm.Versions = append(jm.Versions, jsonVersion{Rev: v.Rev, Path: v.Path})
			}
			multi = append(multi, jm)
		}
		out.MultiVersion = &multi
	}
	if s.active(lint.CheckDeadOverrides) {
		dead := make([]jsonDead, 0, len(s.Report.DeadOverrides))
		for _, d := range s.Report.DeadOverrides {
			dead = append(dead, jsonDead{
				Override: d.Override, Target: d.Target, Input: d.Input, Direct: d.Direct, Via: d.Via,
			})
		}
		out.DeadOverrides = &dead
	}
	if s.active(lint.CheckNixpkgsMaster) {
		nm := &jsonNixpkgsMaster{Conformant: s.Report.NixpkgsMaster == nil}
		if s.Report.NixpkgsMaster != nil {
			nm.Status = s.Report.NixpkgsMaster.Status.String()
			nm.URL = s.Report.NixpkgsMaster.URL
		}
		out.NixpkgsMaster = nm
	}
	if s.active(lint.CheckCanonicalInputs) {
		ci := make([]jsonCanonicalInput, 0, len(s.Report.CanonicalInputs))
		for _, f := range s.Report.CanonicalInputs {
			ci = append(ci, jsonCanonicalInput{
				Input:        f.Input,
				CurrentURL:   f.CurrentURL,
				CanonicalURL: f.CanonicalURL,
			})
		}
		out.CanonicalInputs = &ci
	}
	if s.active(lint.CheckCanonicalForm) {
		cf := &jsonCanonicalForm{Canonical: s.Report.CanonicalForm == nil}
		if s.Report.CanonicalForm != nil {
			cf.Scattered = s.Report.CanonicalForm.Scattered
		}
		out.CanonicalForm = cf
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ndjsonTest is one record in the amarbel-llc/tap test-result NDJSON
// schema (tap-ndjson(7)). `lint` maps its three checks onto top-level test
// points and each finding onto a nested subtest; Directive and Output are
// always null here because lint findings have neither concept.
type ndjsonTest struct {
	Type        string       `json:"type"`
	N           int          `json:"n"`
	Description string       `json:"description"`
	OK          bool         `json:"ok"`
	Directive   any          `json:"directive"`
	Diagnostic  any          `json:"diagnostic"`
	Output      *string      `json:"output"`
	Subtest     []ndjsonTest `json:"subtest"`
	Line        int          `json:"line"`
}

// ndjsonPlan is the leading record announcing how many top-level test
// points the document will contain, mirroring TAP's `1..N` plan line. It
// is a normative record type in the tap NDJSON schema (tap-ndjson(7)): a
// producer that knows its plan up front SHOULD emit the plan record first,
// and the summary's plan_count MUST equal this count. lint knows its plan
// up front — the number of *selected* checks (all three by default, fewer
// under --checks) — so it emits the plan unconditionally.
type ndjsonPlan struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// ndjsonSummary is the trailing summary record of the NDJSON document.
type ndjsonSummary struct {
	Type        string `json:"type"`
	Passed      int    `json:"passed"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
	Todo        int    `json:"todo"`
	Total       int    `json:"total"`
	PlanCount   int    `json:"plan_count"`
	Bailed      bool   `json:"bailed"`
	Valid       bool   `json:"valid"`
	Diagnostics []any  `json:"diagnostics"`
}

type ndjsonFollowsDiag struct {
	Identity  string   `json:"identity"`
	Canonical string   `json:"canonical"`
	NodeCount int      `json:"nodeCount"`
	Lines     []string `json:"lines"`
}

type ndjsonMultiDiag struct {
	Source   string           `json:"source"`
	Versions []ndjsonMultiVer `json:"versions"`
}

type ndjsonMultiVer struct {
	Rev  string `json:"rev"`
	Path string `json:"path"`
}

type ndjsonDeadDiag struct {
	Override string `json:"override"`
	Target   string `json:"target"`
	Input    string `json:"input"`
	Direct   bool   `json:"direct"`
	Via      string `json:"via,omitempty"`
}

type ndjsonNixpkgsMasterDiag struct {
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
}

type ndjsonCanonicalInputDiag struct {
	Input        string `json:"input"`
	CurrentURL   string `json:"currentURL"`
	CanonicalURL string `json:"canonicalURL"`
}

type ndjsonCanonicalFormDiag struct {
	Scattered []string `json:"scattered"`
}

// LintNDJSON writes the lint summary as the amarbel-llc/tap test-result
// NDJSON schema: one JSON object per line. The selected checks (a subset of
// follows opportunities, multi-version inputs, dead follows overrides; all
// three by default) are emitted as top-level `test` records (ok when the
// check found nothing), each finding becomes a nested subtest carrying a
// structured diagnostic, and a trailing `summary` record reports the check
// pass/fail counts. The plan count and summary totals reflect the number of
// selected checks, not the fixed three.
// Subtests do not count toward the summary per the schema. The document
// is always valid and never bailed: lint generates records rather than
// transforming a TAP stream, so there are no parse diagnostics.
func LintNDJSON(w io.Writer, s LintSummary) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	follows := ndjsonTest{
		Type:        "test",
		Description: "follows opportunities",
		OK:          len(s.Report.Follows) == 0,
	}
	for i, r := range s.Report.Follows {
		follows.Subtest = append(follows.Subtest, ndjsonTest{
			Type:        "test",
			N:           i + 1,
			Description: fmt.Sprintf("%s pinned %d× — collapse onto %q", r.Identity, r.NodeCount, r.Canonical),
			OK:          false,
			Diagnostic: ndjsonFollowsDiag{
				Identity:  r.Identity,
				Canonical: r.Canonical,
				NodeCount: r.NodeCount,
				Lines:     r.Lines,
			},
		})
	}

	multi := ndjsonTest{
		Type:        "test",
		Description: "multi-version inputs",
		OK:          len(s.Report.MultiVersion) == 0,
	}
	for i, mv := range s.Report.MultiVersion {
		vers := make([]ndjsonMultiVer, 0, len(mv.Versions))
		for _, v := range mv.Versions {
			vers = append(vers, ndjsonMultiVer{Rev: v.Rev, Path: v.Path})
		}
		multi.Subtest = append(multi.Subtest, ndjsonTest{
			Type:        "test",
			N:           i + 1,
			Description: fmt.Sprintf("%s pinned at %d revs", mv.Source, len(mv.Versions)),
			OK:          false,
			Diagnostic:  ndjsonMultiDiag{Source: mv.Source, Versions: vers},
		})
	}

	dead := ndjsonTest{
		Type:        "test",
		Description: "dead follows overrides",
		OK:          len(s.Report.DeadOverrides) == 0,
	}
	for i, d := range s.Report.DeadOverrides {
		dead.Subtest = append(dead.Subtest, ndjsonTest{
			Type:        "test",
			N:           i + 1,
			Description: fmt.Sprintf("%s: %q has no input %q", d.Override, d.Target, d.Input),
			OK:          false,
			Diagnostic:  ndjsonDeadDiag{Override: d.Override, Target: d.Target, Input: d.Input, Direct: d.Direct, Via: d.Via},
		})
	}

	nixpkgsMaster := ndjsonTest{
		Type:        "test",
		Description: "nixpkgs-master convention",
		OK:          s.Report.NixpkgsMaster == nil,
	}
	if f := s.Report.NixpkgsMaster; f != nil {
		nixpkgsMaster.Subtest = append(nixpkgsMaster.Subtest, ndjsonTest{
			Type:        "test",
			N:           1,
			Description: fmt.Sprintf("nixpkgs-master %s", f.Status),
			OK:          false,
			Diagnostic:  ndjsonNixpkgsMasterDiag{Status: f.Status.String(), URL: f.URL},
		})
	}

	canonicalInputs := ndjsonTest{
		Type:        "test",
		Description: "canonical inputs",
		OK:          len(s.Report.CanonicalInputs) == 0,
	}
	for i, f := range s.Report.CanonicalInputs {
		canonicalInputs.Subtest = append(canonicalInputs.Subtest, ndjsonTest{
			Type:        "test",
			N:           i + 1,
			Description: fmt.Sprintf("%s: non-canonical URL %q", f.Input, f.CurrentURL),
			OK:          false,
			Diagnostic:  ndjsonCanonicalInputDiag{Input: f.Input, CurrentURL: f.CurrentURL, CanonicalURL: f.CanonicalURL},
		})
	}

	canonicalForm := ndjsonTest{
		Type:        "test",
		Description: "canonical inputs-block form",
		OK:          s.Report.CanonicalForm == nil,
	}
	if f := s.Report.CanonicalForm; f != nil {
		canonicalForm.Subtest = append(canonicalForm.Subtest, ndjsonTest{
			Type:        "test",
			N:           1,
			Description: fmt.Sprintf("scattered inputs: %s", truncList(f.Scattered, 8)),
			OK:          false,
			Diagnostic:  ndjsonCanonicalFormDiag{Scattered: f.Scattered},
		})
	}

	// Only the selected checks become top-level test points, renumbered
	// 1..k in canonical order so the plan count and the N fields agree with
	// the selection (rather than the fixed set).
	byCheck := map[lint.Check]ndjsonTest{
		lint.CheckFollows:         follows,
		lint.CheckMultiVersion:    multi,
		lint.CheckDeadOverrides:   dead,
		lint.CheckNixpkgsMaster:   nixpkgsMaster,
		lint.CheckCanonicalInputs: canonicalInputs,
		lint.CheckCanonicalForm:   canonicalForm,
	}
	var checks []ndjsonTest
	for _, c := range lint.AllChecks {
		if !s.active(c) {
			continue
		}
		t := byCheck[c]
		t.N = len(checks) + 1
		checks = append(checks, t)
	}

	// Announce the plan up front, then the per-check test points, then the
	// trailing summary.
	if err := enc.Encode(ndjsonPlan{Type: "plan", Count: len(checks)}); err != nil {
		return err
	}
	passed := 0
	for _, c := range checks {
		if c.OK {
			passed++
		}
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return enc.Encode(ndjsonSummary{
		Type:        "summary",
		Passed:      passed,
		Failed:      len(checks) - passed,
		Total:       len(checks),
		PlanCount:   len(checks),
		Valid:       true,
		Diagnostics: []any{},
	})
}

// shortRev abbreviates a git rev for display. Kept local to render so the
// text/json output formatting stays self-contained.
func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
