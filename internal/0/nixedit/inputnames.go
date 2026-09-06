package nixedit

import (
	"fmt"

	langlang "github.com/clarete/langlang/go"
)

// InputNames returns the names of the top-level flake inputs declared in
// flake.nix, in source order and without duplicates.
//
// Both shapes the rest of this package recognises are covered:
//
//   - block form: `inputs = { nixpkgs.url = …; utils.url = …; }`
//   - flat form:  `inputs.nixpkgs.url = …; inputs.utils.url = …;`
//
// These are exactly the names Nix passes to `outputs` (alongside `self`), so
// they are what an outputs signature must be able to accept.
//
// ErrUnparseable is returned when the shallow grammar cannot parse the file.
// A parseable flake.nix with no `inputs` at all yields an empty slice and no
// error.
func InputNames(src []byte) ([]string, error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
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

	var (
		names     []string
		seen      = map[string]bool{}
		blockText string
		haveBlock bool
	)
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}

	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, val, ok := keyValPath(tree, kv, src)
		if !ok || len(path) == 0 || path[0] != "inputs" {
			continue
		}
		if len(path) == 1 {
			// `inputs = { … }` — remember the body for a second pass, since
			// re-parsing it here would invalidate the tree mid-walk.
			if g, gOK := soleGroup(tree, val); gOK {
				blockText = tree.Text(g)
				haveBlock = true
			}
			continue
		}
		add(path[1])
	}

	if haveBlock {
		inner, err := blockInputNames(matcher, blockText)
		if err != nil {
			return nil, err
		}
		for _, n := range inner {
			add(n)
		}
	}
	return names, nil
}

// blockInputNames returns the first path segment of every binding inside an
// `inputs = { … }` block body, which is that block's input names.
func blockInputNames(matcher langlang.Matcher, blockText string) ([]string, error) {
	src := []byte(blockText)
	tree, _, err := matcher.Match(src)
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
	var names []string
	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, _, ok := keyValPath(tree, kv, src)
		if !ok || len(path) == 0 {
			continue
		}
		names = append(names, path[0])
	}
	return names, nil
}
