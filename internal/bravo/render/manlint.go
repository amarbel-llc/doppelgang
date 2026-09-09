package render

import (
	"encoding/json"
	"fmt"
	"io"

	"code.linenisgreat.com/doppelgang/internal/alfa/manlint"
)

// ManLintSummary is the renderer input for `doppelgang lint-man`.
type ManLintSummary struct {
	Max      int
	Pages    int
	Findings []manlint.Finding
}

// ManLintText writes one `<path>: <check>: <detail>` line per finding, so
// the output greps and diffs like a compiler's. Warnings are suffixed so a
// reader can tell at a glance which lines fail the gate.
func ManLintText(w io.Writer, s ManLintSummary) error {
	for _, f := range s.Findings {
		suffix := ""
		if f.Severity == manlint.SeverityWarning {
			suffix = " (warning)"
		}
		if _, err := fmt.Fprintf(w, "%s: %s: %s%s\n", f.Path, f.Check, f.Detail, suffix); err != nil {
			return err
		}
	}
	return nil
}

// ManLintJSON writes the summary as one indented JSON document: the budget,
// the number of pages checked, and every finding with its severity.
func ManLintJSON(w io.Writer, s ManLintSummary) error {
	findings := s.Findings
	if findings == nil {
		findings = []manlint.Finding{}
	}
	out := struct {
		Max      int               `json:"max"`
		Pages    int               `json:"pages"`
		Findings []manlint.Finding `json:"findings"`
	}{Max: s.Max, Pages: s.Pages, Findings: findings}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
