package nixedit

import (
	"fmt"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// CanonicalFormSentinel is the opt-in marker: a flake enables canonical-form
// enforcement by placing this comment on its own line immediately above its
// `inputs` binding (the `inputs = { … }` block, or the first `inputs.…`
// binding in flat form). Absent the sentinel, CanonicalForm reports
// Enabled=false and CanonicalFormFixTargets returns nothing to change —
// mirroring FDR-0004's `# keep sorted` opt-in precedent so third-party
// flakes are never re-shaped.
const CanonicalFormSentinel = "# canonical-form"

// CanonicalFormReport is the result of scanning a flake.nix's top-level
// `inputs` attribute set for canonical-form violations.
type CanonicalFormReport struct {
	// Enabled reports whether the flake opted in via CanonicalFormSentinel.
	Enabled bool
	// Scattered lists, in first-appearance order, the names of inputs
	// whose bindings are not contiguous under `inputs` — i.e. some other
	// input's binding appears between two bindings belonging to the same
	// input. Populated only when Enabled is true.
	Scattered []string
}

// CanonicalForm scans src for canonical-form opt-in and contiguity
// violations. It does not itself gate on Enabled — callers decide whether
// to act on a disabled report (present for completeness/debugging); lint's
// check and --fix both skip flakes where Enabled is false.
func CanonicalForm(src []byte) (CanonicalFormReport, error) {
	enabled, order, err := analyzeCanonicalForm(src)
	if err != nil {
		return CanonicalFormReport{}, err
	}
	if !enabled {
		return CanonicalFormReport{}, nil
	}
	return CanonicalFormReport{Enabled: enabled, Scattered: scatteredNames(order)}, nil
}

// CanonicalFormFixTargets computes what --fix needs to relocate a scattered
// flake's follows bindings into contiguous chunks: the full attr-paths to
// remove via DeleteBindings, and the corresponding lint-format lines
// (`inputs.X…follows = "Y"`) to re-add afterward via Apply — which, per its
// location-preserving splice, lands each one adjacent to its target input's
// remaining bindings.
//
// Only follows/override bindings move; a scattered input's `url` binding or
// nested sub-attrset is left where it is. Chunk-internal placement (moving a
// follows inside an existing nested `X = { … }` block) and alphabetical
// chunk ordering are not attempted by this pass — see FDR 0007. Returns
// nothing to change when the flake has not opted in (CanonicalFormSentinel
// absent) or has no scattered inputs.
func CanonicalFormFixTargets(src []byte) (deleteTargets, reapplyLines []string, err error) {
	enabled, order, err := analyzeCanonicalForm(src)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, nil
	}
	scattered := map[string]bool{}
	for _, name := range scatteredNames(order) {
		scattered[name] = true
	}
	for _, b := range order {
		if b.followsPath == "" || !scattered[b.input] {
			continue
		}
		deleteTargets = append(deleteTargets, b.followsPath)
		reapplyLines = append(reapplyLines, b.followsPath+" = "+strings.TrimSpace(b.followsRHS))
	}
	return deleteTargets, reapplyLines, nil
}

// inputBinding is one binding directly under `inputs` (or, in block mode,
// directly under the `inputs = { … }` group): the input it belongs to, and —
// when the binding is itself a follows/override — the full attr-path and RHS
// text needed to delete and re-splice it.
type inputBinding struct {
	input       string
	followsPath string // "" unless this binding is itself a follows binding
	followsRHS  string // set only alongside followsPath
}

// analyzeCanonicalForm parses src and returns whether it opts into
// canonical-form enforcement plus the in-file-order sequence of bindings
// directly under `inputs`, uniformly across block and flat form.
func analyzeCanonicalForm(src []byte) (enabled bool, order []inputBinding, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return false, nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
	if err != nil {
		return false, nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	root, ok := tree.Root()
	if !ok {
		return false, nil, ErrUnparseable
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return false, nil, ErrUnparseable
	}

	var (
		haveEnable bool
		blockGroup langlang.NodeID
		haveBlock  bool
	)
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
		if !haveEnable {
			enabled = precedingLineIsSentinel(src, tree.Span(child).Start.Cursor)
			haveEnable = true
		}
		if len(path) == 1 {
			// Block form: `inputs = { … }`.
			if g, gOK := soleGroup(tree, val); gOK {
				blockGroup = g
				haveBlock = true
			}
			continue
		}
		// Flat form: `inputs.<x>… = …`. Strip the leading "inputs" segment
		// so the path is relative like block form's, and share its
		// per-binding conversion.
		order = append(order, bindingFromRelPath(path[1:], val, tree))
	}
	if haveBlock {
		// groupBlock's text is read from the still-valid outer tree before
		// blockBindingOrder re-parses it (invalidating node IDs) — same
		// ordering hazard documented in blockChunkOffsets.
		order = blockBindingOrder(matcher, []byte(tree.Text(blockGroup)))
	}
	return enabled, order, nil
}

// blockBindingOrder re-parses an `inputs = { … }` block's content and
// returns the in-file-order sequence of its direct bindings. Block-form
// paths are already relative to `inputs` (the group supplies that prefix),
// matching the relative form bindingFromRelPath expects.
func blockBindingOrder(matcher langlang.Matcher, groupText []byte) []inputBinding {
	tree, seq, ok := reparseGroupSequence(matcher, groupText)
	if !ok {
		return nil
	}
	var order []inputBinding
	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, val, ok := keyValPath(tree, kv, groupText)
		if !ok || len(path) == 0 {
			continue
		}
		order = append(order, bindingFromRelPath(path, val, tree))
	}
	return order
}

// bindingFromRelPath converts a binding whose attr-path is already relative
// to `inputs` (flat form's path with its leading "inputs" segment
// stripped, or a block-form path as parsed) into an inputBinding, resolving
// the full "inputs."-prefixed follows attr-path and RHS text when the
// binding is itself a follows/override. Shared by analyzeCanonicalForm's
// flat-form loop and blockBindingOrder so the two forms' bindings convert
// identically.
func bindingFromRelPath(relPath []string, val langlang.NodeID, tree langlang.Tree) inputBinding {
	b := inputBinding{input: relPath[0]}
	if len(relPath) > 1 && relPath[len(relPath)-1] == "follows" {
		b.followsPath = "inputs." + strings.Join(relPath, ".")
		b.followsRHS = tree.Text(val)
	}
	return b
}

// scatteredNames returns, in first-appearance order, the input names whose
// bindings in order are not contiguous — some other input's binding
// appears between two of theirs.
func scatteredNames(order []inputBinding) []string {
	firstSeen := map[string]int{}
	lastSeen := map[string]int{}
	for i, b := range order {
		if _, ok := firstSeen[b.input]; !ok {
			firstSeen[b.input] = i
		}
		lastSeen[b.input] = i
	}
	var scattered []string
	seen := map[string]bool{}
	for _, b := range order {
		if seen[b.input] {
			continue
		}
		seen[b.input] = true
		for i := firstSeen[b.input]; i <= lastSeen[b.input]; i++ {
			if order[i].input != b.input {
				scattered = append(scattered, b.input)
				break
			}
		}
	}
	return scattered
}

// precedingLineIsSentinel reports whether the line immediately before the
// line containing byte offset off is exactly CanonicalFormSentinel (leading/
// trailing whitespace tolerated).
func precedingLineIsSentinel(src []byte, off int) bool {
	curLineStart := lineStart(src, off)
	if curLineStart == 0 {
		return false
	}
	prevLineEnd := curLineStart - 1 // the '\n' terminating the previous line
	prevLineStart := lineStart(src, prevLineEnd)
	return strings.TrimSpace(string(src[prevLineStart:prevLineEnd])) == CanonicalFormSentinel
}
