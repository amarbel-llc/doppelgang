package nixedit

import (
	"fmt"
	"sort"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// Overrides returns the `follows` override bindings declared in flake.nix's
// top-level `inputs`, each as a full attr-path string ending in `.follows`,
// e.g. "inputs.bats.inputs.nixpkgs.follows". It is the read-only companion to
// the dead-override analysis in internal/alfa/lint, which decides which of
// these point at an input the dependency no longer declares.
//
// Three flake.nix shapes are handled uniformly:
//
//   - flat top-level bindings: `inputs.<dep>.inputs.<x>.follows = …`;
//   - a block `inputs = { … }` whose bindings carry an implicit `inputs`
//     prefix, e.g. `igloo.inputs.nixpkgs-master.follows = …`;
//   - a nested sub-attrset input value, e.g. `bats = { inputs.nixpkgs.follows
//     = …; }`, which yields `inputs.bats.inputs.nixpkgs.follows`.
//
// The shallow grammar parses each attribute set's binding *keys* structurally
// but its binding *values* opaquely, so a sub-attrset value is descended by
// re-parsing its `{ … }` text. A file the grammar cannot parse yields
// ErrUnparseable; a nested value that fails to re-parse is skipped (its
// subtree is simply not searched) rather than failing the whole extraction.
func Overrides(src []byte) ([]string, error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	var out []string
	if err := collectFollows(matcher, src, nil, &out); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// collectFollows parses attrsetSrc (a whole flake.nix or a single `{ … }`
// group body) and walks its top-level bindings, appending every follows
// binding's full attr-path (prefix + key path) and descending into bare
// attrset values. prefix is the attr-path of the enclosing attrset (nil at
// the file root, ["inputs"] inside an `inputs = { … }` block, and so on).
//
// langlang's matcher is single-tree: each Match invalidates the node IDs of
// the previously matched tree. So this fully reads the current tree first —
// emitting follows and gathering the (text, prefix) of each sub-attrset value
// — and only then re-parses those sub-attrsets. A nested re-parse failure is
// non-fatal: that subtree is skipped rather than abandoning the whole file.
func collectFollows(matcher langlang.Matcher, attrsetSrc []byte, prefix []string, out *[]string) error {
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
		if path[len(path)-1] == "follows" {
			*out = append(*out, strings.Join(full, "."))
			continue
		}
		if group, ok := soleGroup(tree, val); ok {
			subs = append(subs, subAttrSet{text: tree.Text(group), prefix: full})
		}
	}

	// The current tree is fully read; re-parsing sub-attrsets now is safe.
	for _, s := range subs {
		_ = collectFollows(matcher, []byte(s.text), s.prefix, out)
	}
	return nil
}
