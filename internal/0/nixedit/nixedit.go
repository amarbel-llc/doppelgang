// Package nixedit performs targeted surgery on a flake.nix: it splices
// `inputs.X...follows = "Y"` bindings into the top-level `inputs`
// attribute set, preserving the rest of the file byte-for-byte.
//
// It parses flake.nix with a shallow Nix PEG (nix.peg, embedded) via
// amarbel-llc/langlang's runtime matcher. The grammar models only the
// structure needed to locate the `inputs` attrset and its bindings;
// every binding value is skipped opaquely. Any file the grammar cannot
// parse yields ErrUnparseable, and callers fall back to print-only
// rather than risk corrupting the file.
package nixedit

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed nix.peg
var nixGrammar []byte

// ErrUnparseable is returned when the shallow Nix grammar cannot parse
// the input, or cannot find a top-level `inputs` attribute set to edit.
// Callers should treat this as "apply nothing" and fall back to
// report-only behavior.
var ErrUnparseable = errors.New("nixedit: flake.nix not parseable by the shallow Nix grammar")

// Apply splices the given follows lines into flake.nix's top-level
// `inputs` attribute set and returns the rewritten source.
//
// Each line is expected in the form produced by the lint package:
//
//	inputs.<a>.inputs.<b>...follows = "<canonical/path>"
//
// A trailing "   # ..." comment (lint appends one for multi-parent
// nodes) is stripped before applying. Lines whose attr-path is already
// bound somewhere under the existing `inputs` attrset are skipped, so
// re-running Apply on an already-collapsed flake is a no-op (the
// returned applied slice is empty and out == src).
//
// The returned applied slice lists the lines actually written, in input
// order. err is ErrUnparseable when the file could not be parsed or has
// no editable `inputs` attrset.
func Apply(src []byte, lines []string) (out []byte, applied []string, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}

	ins, ok := findInputsAttrSet(tree, src)
	if !ok {
		return nil, nil, ErrUnparseable
	}

	// Normalize requested lines and drop ones already satisfied.
	var toApply []parsedLine
	for _, raw := range lines {
		pl, ok := parseFollowsLine(raw)
		if !ok {
			// Not a recognizable follows line; skip defensively rather
			// than splice something malformed.
			continue
		}
		if ins.has(pl.path) {
			continue
		}
		toApply = append(toApply, pl)
	}
	if len(toApply) == 0 {
		return src, nil, nil
	}

	rendered, appliedLines := renderBindings(toApply, ins)
	out = spliceAt(src, ins.insertOffset, rendered)
	return out, appliedLines, nil
}

// parsedLine is a follows edit decomposed into its LHS attr-path and the
// full canonical text used to re-render the binding.
type parsedLine struct {
	// path is the LHS attr names, e.g. ["inputs","utils","inputs","systems","follows"].
	path []string
	// text is the cleaned `inputs.X...follows = "Y"` (comment stripped).
	text string
}

// parseFollowsLine cleans a lint follows line and extracts its LHS
// attr-path. It strips any trailing `#` comment (lint appends a
// multi-parent note) and surrounding whitespace. Returns false if the
// line does not look like `inputs.… = "…"`.
func parseFollowsLine(raw string) (parsedLine, bool) {
	s := strings.TrimSpace(raw)
	// Strip a trailing comment. The follows RHS is a quoted string with
	// no '#', so the first '#' after the closing quote starts a comment.
	if i := strings.Index(s, "#"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	eq := strings.Index(s, "=")
	if eq < 0 {
		return parsedLine{}, false
	}
	lhs := strings.TrimSpace(s[:eq])
	if !strings.HasPrefix(lhs, "inputs.") {
		return parsedLine{}, false
	}
	return parsedLine{path: strings.Split(lhs, "."), text: s}, true
}

// renderBindings formats the lines to splice into the located inputs
// region and returns the spliced text plus the cleaned text of each line
// applied (for the caller's report).
//
// In block mode each binding's leading `inputs.` segment is stripped
// (the enclosing `inputs = { … }` already supplies it). In flat mode the
// full `inputs.…` form is kept. When leadNewline is set (the splice point
// is mid-line) each binding is written as a leading newline + indent +
// text + ";"; otherwise each binding is written as indent + text + ";\n",
// which suits inserting at the start of a block's closing-brace line. A
// non-empty trailNewlineIndent appends a final "\n" + that indent (used
// to move a single-line block's closing brace onto its own line).
func renderBindings(lines []parsedLine, ins inputsAttrSet) (string, []string) {
	var b strings.Builder
	applied := make([]string, 0, len(lines))
	for _, pl := range lines {
		text := pl.text
		if ins.blockMode {
			text = strings.TrimPrefix(text, "inputs.")
		}
		if ins.leadNewline {
			b.WriteString("\n")
			b.WriteString(ins.indent)
			b.WriteString(text)
			b.WriteString(";")
		} else {
			b.WriteString(ins.indent)
			b.WriteString(text)
			b.WriteString(";\n")
		}
		applied = append(applied, pl.text)
	}
	if ins.leadNewline && ins.trailNewlineIndent != "" {
		b.WriteString("\n")
		b.WriteString(ins.trailNewlineIndent)
	}
	return b.String(), applied
}

// spliceAt inserts ins at byte offset off in src.
func spliceAt(src []byte, off int, ins string) []byte {
	out := make([]byte, 0, len(src)+len(ins))
	out = append(out, src[:off]...)
	out = append(out, ins...)
	out = append(out, src[off:]...)
	return out
}

// inputsAttrSet describes the located, editable `inputs` region and how
// to splice new follows bindings into it.
type inputsAttrSet struct {
	// existing is the set of attr-paths already bound under inputs,
	// joined by ".", each in the full `inputs.`-prefixed form (e.g.
	// "inputs.utils.inputs.systems.follows"). Used for idempotency.
	existing map[string]bool
	// insertOffset is the byte offset at which new bindings are spliced:
	// just before the `inputs = { … }` block's closing brace (block
	// mode), or just after the last flat inputs.* binding (flat mode).
	insertOffset int
	// indent is the leading whitespace to mirror for spliced lines.
	indent string
	// blockMode is true when splicing inside an `inputs = { … }` block,
	// in which case the leading `inputs.` segment is stripped from each
	// rendered binding (the block already supplies it).
	blockMode bool
	// leadNewline is true when the splice point is mid-line (flat mode
	// after a binding's `;`, or a single-line block right at its closing
	// brace), so each new binding is written as "\n" + indent + text + ";"
	// rather than indent + text + ";\n".
	leadNewline bool
	// trailNewlineIndent, when non-empty, is written as "\n" + this value
	// after the last leadNewline binding — used to push a single-line
	// block's closing brace onto its own line at the brace's indent.
	trailNewlineIndent string
}

func (i inputsAttrSet) has(path []string) bool {
	return i.existing[strings.Join(path, ".")]
}
