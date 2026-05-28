package render

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/friedenberg/doppelgang/internal/alfa/lint"
)

// LintSummary is the renderer input for `doppelgang lint`: the flake.lock
// follows / multi-version findings.
type LintSummary struct {
	Report lint.Report
}

// LintText writes the human-readable lint summary.
func LintText(w io.Writer, s LintSummary) error {
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

	return nil
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
	out := struct {
		Follows      []jsonFollows `json:"followsOpportunities"`
		MultiVersion []jsonMulti   `json:"multiVersionInputs"`
	}{
		Follows:      make([]jsonFollows, 0, len(s.Report.Follows)),
		MultiVersion: make([]jsonMulti, 0, len(s.Report.MultiVersion)),
	}
	for _, r := range s.Report.Follows {
		out.Follows = append(out.Follows, jsonFollows{
			Identity: r.Identity, Canonical: r.Canonical, Lines: r.Lines,
		})
	}
	for _, mv := range s.Report.MultiVersion {
		jm := jsonMulti{Source: mv.Source, Versions: make([]jsonVersion, 0, len(mv.Versions))}
		for _, v := range mv.Versions {
			jm.Versions = append(jm.Versions, jsonVersion{Rev: v.Rev, Path: v.Path})
		}
		out.MultiVersion = append(out.MultiVersion, jm)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ndjsonTest is one record in the amarbel-llc/tap test-result NDJSON
// schema (tap-ndjson(7)). `lint` maps its two checks onto top-level test
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
// and the summary's plan_count MUST equal this count. lint always runs
// exactly two checks, so it emits the plan unconditionally.
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

// LintNDJSON writes the lint summary as the amarbel-llc/tap test-result
// NDJSON schema: one JSON object per line. The two flake.lock checks are
// emitted as top-level `test` records (ok when the check found nothing),
// each finding becomes a nested subtest carrying a structured diagnostic,
// and a trailing `summary` record reports the check pass/fail counts.
// Subtests do not count toward the summary per the schema. The document
// is always valid and never bailed: lint generates records rather than
// transforming a TAP stream, so there are no parse diagnostics.
func LintNDJSON(w io.Writer, s LintSummary) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	follows := ndjsonTest{
		Type:        "test",
		N:           1,
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
		N:           2,
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

	checks := []ndjsonTest{follows, multi}

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
