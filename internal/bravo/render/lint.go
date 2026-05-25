package render

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/friedenberg/doppelgang/internal/alfa/dupes"
	"github.com/friedenberg/doppelgang/internal/alfa/lint"
)

// LintSummary is the renderer input for `doppelgang lint`: the flake.lock
// findings plus the optional closure-level version-drift groups. Drift is
// nil when the closure pass was skipped (no installable / --no-closure).
type LintSummary struct {
	Report lint.Report
	Drift  []dupes.DriftGroup
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
			r.Identity, len(r.Lines)+1, r.Canonical); err != nil {
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

	return writeDrift(w, s.Drift)
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
		Follows      []jsonFollows    `json:"followsOpportunities"`
		MultiVersion []jsonMulti      `json:"multiVersionInputs"`
		VersionDrift []jsonDriftGroup `json:"versionDrift,omitempty"`
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
	if s.Drift != nil {
		out.VersionDrift = make([]jsonDriftGroup, 0, len(s.Drift))
		for _, dg := range s.Drift {
			jdg := jsonDriftGroup{
				Pname:      dg.Pname,
				TotalBytes: dg.TotalBytes,
				Versions:   make([]jsonDriftVersion, 0, len(dg.Versions)),
			}
			for _, v := range dg.Versions {
				jdg.Versions = append(jdg.Versions, jsonDriftVersion{
					Version:     v.Version,
					Count:       v.Count,
					Size:        v.Size,
					IsExactDupe: v.IsExactDupe,
					Parents:     v.Parents,
					Owners:      v.Owners,
				})
			}
			out.VersionDrift = append(out.VersionDrift, jdg)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// shortRev abbreviates a git rev for display. Kept local to render so the
// text/json output formatting stays self-contained.
func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
