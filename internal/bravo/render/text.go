// Package render emits dupes.Group output as either human-readable text
// or structured JSON.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/friedenberg/doppelgang/internal/alfa/dupes"
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
	return nil
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
