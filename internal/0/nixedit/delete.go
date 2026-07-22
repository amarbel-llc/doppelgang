package nixedit

import (
	"fmt"
	"sort"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// DeleteBindings removes the bindings at the given full attr-paths (e.g.
// "inputs.bats.inputs.nixpkgs.follows") from flake.nix and returns the
// rewritten source plus the attr-paths actually found and removed. It is the
// inverse of Apply: where Apply splices follows bindings in, DeleteBindings
// excises them — used by `lint --fix` to prune dead overrides.
//
// Targets are matched in the same full-form vocabulary Overrides emits, so a
// binding is located regardless of whether it is written flat at the top
// level, flat inside an `inputs = { … }` block, or nested in a sub-attrset
// input value. Each removed span covers the binding through its terminating
// `;`; when the binding occupies its own line, the line's leading indentation
// and trailing newline go too, leaving no blank-line scar.
//
// A target that is not found is silently ignored (so the call is idempotent
// and a no-op when nothing matches). ErrUnparseable is returned when the
// shallow grammar cannot parse the file, so callers fall back to print-only.
func DeleteBindings(src []byte, targets []string) (out []byte, removed []string, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	want := make(map[string]bool, len(targets))
	for _, t := range targets {
		want[t] = true
	}
	spans := map[string]span{}
	if err := locateBindings(matcher, src, 0, nil, want, spans); err != nil {
		return nil, nil, err
	}
	if len(spans) == 0 {
		return src, nil, nil
	}

	list := make([]span, 0, len(spans))
	removed = make([]string, 0, len(spans))
	for t, s := range spans {
		list = append(list, s)
		removed = append(removed, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].start < list[j].start })

	out = make([]byte, 0, len(src))
	prev := 0
	for _, s := range list {
		if s.start < prev {
			continue // defensive: overlapping spans (distinct bindings never overlap)
		}
		out = append(out, src[prev:s.start]...)
		prev = s.end
	}
	out = append(out, src[prev:]...)
	sort.Strings(removed)
	return out, removed, nil
}

// span is a half-open byte range [start, end) in the original source.
type span struct{ start, end int }

// locateBindings walks the bindings of attrsetSrc (a whole flake.nix or a
// single `{ … }` group body at absolute offset base in the original file),
// recording the absolute deletion span of every binding whose full attr-path
// is in want, and descending into bare-attrset values. Like collectFollows it
// reads the whole tree before re-parsing any sub-attrset, because langlang's
// matcher invalidates a prior tree on the next Match.
func locateBindings(matcher langlang.Matcher, attrsetSrc []byte, base int, prefix []string, want map[string]bool, spans map[string]span) error {
	tree, _, err := matcher.Match(attrsetSrc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	root, ok := tree.Root()
	if !ok {
		return ErrUnparseable
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return ErrUnparseable
	}

	type subAttrSet struct {
		text   string
		base   int
		prefix []string
	}
	var subs []subAttrSet

	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, val, ok := keyValPath(tree, kv, attrsetSrc)
		if !ok || len(path) == 0 {
			continue
		}
		full := append(append([]string{}, prefix...), path...)
		key := strings.Join(full, ".")
		if path[len(path)-1] == "follows" && want[key] {
			kv := tree.Span(kv)
			s := deleteSpan(attrsetSrc, kv.Start.Cursor, kv.End.Cursor)
			spans[key] = span{start: base + s.start, end: base + s.end}
			continue
		}
		if group, ok := soleGroup(tree, val); ok {
			subs = append(subs, subAttrSet{
				text:   tree.Text(group),
				base:   base + tree.Span(group).Start.Cursor,
				prefix: full,
			})
		}
	}

	for _, sg := range subs {
		_ = locateBindings(matcher, []byte(sg.text), sg.base, sg.prefix, want, spans)
	}
	return nil
}

// deleteSpan computes the byte range to excise for a binding whose KeyVal
// spans [kvStart, kvEnd) in src. It extends the end past the terminating `;`
// (which can sit just beyond the KeyVal span). When the binding occupies its
// own line — only whitespace before it, only the line ending after the `;` —
// the line's leading indent and its trailing newline are included so the line
// vanishes without leaving a blank scar. Otherwise only the binding text and
// its `;` are removed, preserving surrounding layout.
//
// When such an own-line binding also sits in its own blank-line-separated
// paragraph (a blank line both before and after it — how a scattered follows
// line typically lands, hand-added or left behind by a collapse), deleting
// only the binding's line would merge those two flanking blank lines into a
// run of 2+, which nixfmt collapses back to one on its next run — breaking
// the nixfmt-stability of --fix's output (#27). So the following blank-line
// run is swallowed too, leaving just the one blank line already before it.
func deleteSpan(src []byte, kvStart, kvEnd int) span {
	end := afterSemicolon(src, kvEnd)
	ownLine := onlyBlankBefore(src, kvStart)

	i := end
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if ownLine && i >= len(src) {
		return span{start: lineStart(src, kvStart), end: len(src)}
	}
	if ownLine && (src[i] == '\n' || src[i] == '\r') {
		j := i
		if j < len(src) && src[j] == '\r' {
			j++
		}
		if j < len(src) && src[j] == '\n' {
			j++
		}
		start := lineStart(src, kvStart)
		if precedingLineBlank(src, start) {
			j = skipBlankLines(src, j)
		}
		return span{start: start, end: j}
	}
	return span{start: kvStart, end: end}
}

// precedingLineBlank reports whether the line immediately before byte
// offset off — which must itself be a line start — is blank (whitespace-only,
// possibly empty). Returns false when off is the file's first line (no
// preceding line to check).
func precedingLineBlank(src []byte, off int) bool {
	if off == 0 || src[off-1] != '\n' {
		return false
	}
	prevStart := lineStart(src, off-1)
	for i := prevStart; i < off-1; i++ {
		if src[i] != ' ' && src[i] != '\t' && src[i] != '\r' {
			return false
		}
	}
	return true
}

// skipBlankLines advances off past every immediately-following blank line
// (whitespace-only content through its terminating '\n'), stopping at the
// first non-blank line or end of file. Returns off unchanged if the line
// starting there is not blank.
func skipBlankLines(src []byte, off int) int {
	for {
		i := off
		for i < len(src) && src[i] != '\n' {
			if src[i] != ' ' && src[i] != '\t' && src[i] != '\r' {
				return off
			}
			i++
		}
		if i >= len(src) {
			return off
		}
		off = i + 1
	}
}
