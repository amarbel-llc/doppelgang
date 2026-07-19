// Package render emits dupes.Group output as either human-readable text
// or structured JSON.
package render

import (
	"fmt"
	"io"
	"strings"

	"code.linenisgreat.com/doppelgang/internal/alfa/dupes"
)

// Summary is the renderer input: scope label, totals, the duplicate
// groups to emit, and an optional owners map. When Owners is non-nil it
// is used in place of each Copy.Parents for the per-copy attribution
// line.
type Summary struct {
	Scope      string
	TotalPaths int
	TotalBytes int64
	Groups     []dupes.Group
	Owners     map[string][]string
	// Drift, when non-nil, is rendered as an additional section beneath
	// the exact-duplicate groups. nil means the version-drift pass was
	// not requested (or was opted out of); empty slice means it ran but
	// found nothing.
	Drift []dupes.DriftGroup
}

// Text writes the human-readable summary used by the CLI.
func Text(w io.Writer, s Summary) error {
	if _, err := fmt.Fprintf(w, "%s closure: %d paths, %s\n\n",
		s.Scope, s.TotalPaths, human(s.TotalBytes)); err != nil {
		return err
	}
	for _, gr := range s.Groups {
		if _, err := fmt.Fprintf(w, "── %s   ×%d   per-copy=%s   wasted=%s\n",
			gr.Name, len(gr.Copies), human(gr.Copies[0].Size), human(gr.Wasted)); err != nil {
			return err
		}
		for i, c := range gr.Copies {
			if _, err := fmt.Fprintf(w, "    [#%d] %s\n", i+1, attribLine(c, s.Owners)); err != nil {
				return err
			}
		}
	}
	return writeDrift(w, s.Drift)
}

// writeDrift emits the version-drift section. nil drift skips the section
// entirely; an empty (but non-nil) slice prints a one-line "no drift"
// confirmation so the reader knows the pass ran. When any drift version
// carries Parents or Owners, the per-pname output expands into multiline
// form (header + indented per-version lines) so the attribution is
// readable; otherwise the compact one-line form is used.
func writeDrift(w io.Writer, drift []dupes.DriftGroup) error {
	if drift == nil {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n── Version drift ──\n"); err != nil {
		return err
	}
	if len(drift) == 0 {
		_, err := fmt.Fprintln(w, "(no pname has more than one version in this closure)")
		return err
	}
	for _, dg := range drift {
		if driftHasAttribution(dg) {
			if err := writeDriftMultiline(w, dg); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s\t%d versions: %s\n",
			dg.Pname, len(dg.Versions), formatDriftVersions(dg.Versions)); err != nil {
			return err
		}
	}
	return nil
}

func driftHasAttribution(dg dupes.DriftGroup) bool {
	for _, v := range dg.Versions {
		if len(v.Parents) > 0 || len(v.Owners) > 0 {
			return true
		}
	}
	return false
}

// writeDriftMultiline renders one drift pname across multiple lines:
// header line with pname and version count, followed by an indented
// line per version with the version tag and either owners (preferred
// when present) or parents.
func writeDriftMultiline(w io.Writer, dg dupes.DriftGroup) error {
	if _, err := fmt.Fprintf(w, "%s\t%d versions:\n", dg.Pname, len(dg.Versions)); err != nil {
		return err
	}
	for _, v := range dg.Versions {
		tag := v.Version
		if v.IsExactDupe {
			tag = fmt.Sprintf("%s (×%d exact dupe)", v.Version, v.Count)
		}
		if len(v.Owners) > 0 {
			if _, err := fmt.Fprintf(w, "    %s  owners: %s\n",
				tag, truncList(v.Owners, 6)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "    %s  via %s\n",
			tag, truncList(v.Parents, 6)); err != nil {
			return err
		}
	}
	return nil
}

// formatDriftVersions renders a comma-separated version list with each
// also-an-exact-dupe version tagged inline, e.g.
// "21.1.7, 21.1.8 (×2 exact dupe)".
func formatDriftVersions(vs []dupes.DriftVersion) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		if v.IsExactDupe {
			parts = append(parts, fmt.Sprintf("%s (×%d exact dupe)", v.Version, v.Count))
		} else {
			parts = append(parts, v.Version)
		}
	}
	return strings.Join(parts, ", ")
}

func attribLine(c dupes.Copy, owners map[string][]string) string {
	if owners != nil {
		o := owners[c.Path]
		if len(o) == 0 {
			return "(unowned: not reachable from any top-level)"
		}
		return "owners: " + truncList(o, 6)
	}
	if len(c.Parents) == 0 {
		return "(closure root)"
	}
	return "via " + truncList(c.Parents, 6)
}

// truncList joins ss with comma-spaces, truncating to max items with a
// "(N more)" tail if exceeded.
func truncList(ss []string, max int) string {
	if len(ss) <= max {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:max], ", ") + fmt.Sprintf(", ... (%d more)", len(ss)-max)
}

// human formats bytes with binary suffix at one decimal place. K omits
// the decimal because per-copy sizes below 1 MB are usually whole.
func human(b int64) string {
	const (
		K = int64(1024)
		M = K * 1024
		G = M * 1024
	)
	switch {
	case b >= G:
		return fmt.Sprintf("%.1fG", float64(b)/float64(G))
	case b >= M:
		return fmt.Sprintf("%.1fM", float64(b)/float64(M))
	case b >= K:
		return fmt.Sprintf("%dK", b/K)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
