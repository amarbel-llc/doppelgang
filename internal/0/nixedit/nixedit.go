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
	"sort"
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

	// NOTE: findInputsAttrSet may call blockChunkOffsets which re-parses a
	// sub-attrset, invalidating `tree`. All tree navigation must occur inside
	// findInputsAttrSet; do not use `tree` after this call.
	ins, ok := findInputsAttrSet(tree, src, matcher)
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

	// Location-preserving splice: when a binding targets input X and X
	// already has bindings in the block, splice after X's last binding
	// rather than at the global insert point. This keeps follows lines
	// adjacent to their input's chunk (url, existing follows, nested block)
	// instead of floating to the bottom of the inputs attrset.
	//
	// Group lines by their destination byte offset. Multiple lines sharing
	// an offset are rendered in one pass; groups are applied in ascending
	// offset order so earlier splices never shift later offsets.
	type group struct {
		lines []parsedLine
		ins   inputsAttrSet
	}
	groups := map[int]*group{}
	destOf := func(pl parsedLine) (int, inputsAttrSet) {
		if len(pl.path) >= 2 && pl.path[0] == "inputs" {
			if off, ok := ins.chunkEnd[pl.path[1]]; ok {
				// Chunk-preserving: insert right after X's last binding's
				// semicolon. Always lead with a newline; no trailing
				// indent push (the next char is the original newline).
				chunkIns := ins
				chunkIns.leadNewline = true
				chunkIns.trailNewlineIndent = ""
				return off, chunkIns
			}
		}
		return ins.insertOffset, ins
	}
	for _, pl := range toApply {
		off, destIns := destOf(pl)
		if g, ok := groups[off]; ok {
			g.lines = append(g.lines, pl)
		} else {
			groups[off] = &group{lines: []parsedLine{pl}, ins: destIns}
		}
	}

	// Sort offsets ascending so we build the output in one left-to-right pass.
	offsets := make([]int, 0, len(groups))
	for off := range groups {
		offsets = append(offsets, off)
	}
	sort.Ints(offsets)

	out = make([]byte, 0, len(src)+256)
	prev := 0
	for _, off := range offsets {
		g := groups[off]
		rendered, a := renderBindings(g.lines, g.ins)
		out = append(out, src[prev:off]...)
		out = append(out, rendered...)
		prev = off
		applied = append(applied, a...)
	}
	out = append(out, src[prev:]...)
	return out, applied, nil
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

// spliceRange replaces the byte range [start, end) in src with replacement,
// e.g. MigrateLegacySentinel's in-place comment-line rewrite. spliceAt (a
// pure insertion, no deletion) is the special case start == end.
func spliceRange(src []byte, start, end int, replacement string) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(replacement))
	out = append(out, src[:start]...)
	out = append(out, replacement...)
	out = append(out, src[end:]...)
	return out
}

// spliceAt inserts ins at byte offset off in src.
func spliceAt(src []byte, off int, ins string) []byte {
	return spliceRange(src, off, off, ins)
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
	// Used as the fallback when no chunk-specific offset is available.
	insertOffset int
	// chunkEnd maps each input name to the byte offset in src immediately
	// after the semicolon of that input's last binding — the location-
	// preserving splice point for new bindings targeting that input. Nil
	// or absent when not computed (fallback to insertOffset applies).
	chunkEnd map[string]int
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
