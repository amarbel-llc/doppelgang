package nixedit

import (
	"fmt"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// InputURL returns the url string bound to the top-level flake input
// `input` (e.g. "nixpkgs-master") in flake.nix, and whether such a `.url`
// binding is present. It recognizes the same three shapes as Overrides:
//
//   - flat top-level: `inputs.<input>.url = "…"`;
//   - flat-in-block: `<input>.url = "…"` inside `inputs = { … }`;
//   - nested sub-attrset value: `<input> = { url = "…"; }`.
//
// A binding whose value is not a plain double-quoted string (an
// interpolation, a `let … in`, etc.) is treated as not-present — the fleet's
// inputs are all plain string urls, and reporting "missing" is the safe
// conservative outcome. ErrUnparseable is returned when the shallow grammar
// cannot parse the file, so callers fall back to skipping the check.
func InputURL(src []byte, input string) (url string, present bool, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return "", false, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	loc, err := locateInputURL(matcher, src, 0, nil, input)
	if err != nil {
		return "", false, err
	}
	if loc == nil {
		return "", false, nil
	}
	return loc.value, true, nil
}

// SetInputURL edits flake.nix so the top-level input `input` is pinned to
// `url`, preserving the rest of the file byte-for-byte. It is the repair
// companion to InputURL and covers both cases:
//
//   - PRESENT-but-different: the input already declares a `.url`, so only
//     that url's string literal is rewritten in place. A no-op
//     (changed=false, out==src) when it already equals url.
//   - MISSING: the input declares no `.url`, so a `<input>.url = "<url>";`
//     binding is spliced into the top-level `inputs` attrset with the same
//     discipline Apply uses for follows lines (block or flat form,
//     untouched bytes verbatim).
//
// ErrUnparseable is returned when the file cannot be parsed or (in the
// missing case) has no editable `inputs` region to splice into, so callers
// fall back to print-only.
func SetInputURL(src []byte, input, url string) (out []byte, changed bool, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, false, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	loc, err := locateInputURL(matcher, src, 0, nil, input)
	if err != nil {
		return nil, false, err
	}
	if loc != nil {
		if loc.value == url {
			return src, false, nil
		}
		out = make([]byte, 0, len(src)+len(url))
		out = append(out, src[:loc.span.start]...)
		out = append(out, '"')
		out = append(out, url...)
		out = append(out, '"')
		out = append(out, src[loc.span.end:]...)
		return out, true, nil
	}
	// Missing: splice a new url binding via the Apply machinery, which
	// handles the block/flat form split and idempotency.
	line := "inputs." + input + `.url = "` + url + `"`
	out, applied, err := Apply(src, []string{line})
	if err != nil {
		return nil, false, err
	}
	return out, len(applied) > 0, nil
}

// urlLoc is a located `.url` string literal: the absolute byte span of the
// quoted literal in the original file and its unquoted value.
type urlLoc struct {
	span  span
	value string
}

// locateInputURL walks the bindings of attrsetSrc (a whole flake.nix or a
// single `{ … }` group body at absolute offset base) looking for the binding
// whose full attr-path is `inputs.<input>.url`, returning its string
// literal's absolute span and unquoted value. It descends bare-attrset input
// values by re-parsing them (like collectFollows / locateBindings), reading
// the whole current tree before any re-parse because langlang's matcher
// invalidates a prior tree on the next Match. Returns nil (no error) when no
// such binding is found.
func locateInputURL(matcher langlang.Matcher, attrsetSrc []byte, base int, prefix []string, input string) (*urlLoc, error) {
	tree, _, err := matcher.Match(attrsetSrc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	root, ok := tree.Root()
	if !ok {
		return nil, ErrUnparseable
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return nil, ErrUnparseable
	}

	want := strings.Join([]string{"inputs", input, "url"}, ".")

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
		if strings.Join(full, ".") == want {
			sp, value, ok := stringValueSpan(tree, val)
			if !ok {
				continue
			}
			return &urlLoc{
				span:  span{start: base + sp.start, end: base + sp.end},
				value: value,
			}, nil
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
		loc, _ := locateInputURL(matcher, []byte(sg.text), sg.base, sg.prefix, input)
		if loc != nil {
			return loc, nil
		}
	}
	return nil, nil
}

// stringValueSpan returns the span and unquoted text of a Value node that is
// exactly one double-quoted string (e.g. a url), false otherwise. The span
// covers the quoted literal including its surrounding quotes. A value that is
// a group, interpolation, or other compound expression yields false so the
// caller treats it as not a plain string url.
func stringValueSpan(tree langlang.Tree, val langlang.NodeID) (span, string, bool) {
	for _, c := range valueItems(tree, val) {
		switch nodeName(tree, c) {
		case "String":
			sp := tree.Span(c)
			return span{start: sp.Start.Cursor, end: sp.End.Cursor}, unquote(tree.Text(c)), true
		case "OuterText":
			if strings.TrimSpace(tree.Text(c)) != "" {
				return span{}, "", false
			}
		default:
			return span{}, "", false
		}
	}
	return span{}, "", false
}
