package nixedit

import (
	"fmt"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// CanonicalFormDirective is the directive name a flake places after the
// `# doppelgang:` prefix (see directivePrefix) to opt into canonical-form
// enforcement, e.g. `# doppelgang: canonical`, on its own line immediately
// above its `inputs` binding (the `inputs = { … }` block, or the first
// `inputs.…` binding in flat form). Absent the directive (in either this
// structured form or the legacy canonicalFormLegacySentinel spelling),
// CanonicalForm reports Enabled=false and CanonicalFormFixTargets returns
// nothing to change — mirroring FDR-0004's `# keep sorted` opt-in precedent
// so third-party flakes are never re-shaped.
const CanonicalFormDirective = "canonical"

// canonicalFormLegacySentinel is the opt-in spelling this package used
// before the structured `# doppelgang: <directive>` convention. Still
// recognized for Enabled so already-opted-in flakes keep working; --fix
// rewrites it to the structured form via MigrateLegacySentinel.
const canonicalFormLegacySentinel = "# canonical-form"

// CanonicalFormReport is the result of scanning a flake.nix's top-level
// `inputs` attribute set for canonical-form violations.
type CanonicalFormReport struct {
	// Enabled reports whether the flake opted in, via either the
	// structured `# doppelgang: canonical` directive or the legacy
	// `# canonical-form` sentinel.
	Enabled bool
	// LegacySentinel reports whether the opt-in used the legacy
	// `# canonical-form` spelling rather than the structured directive.
	// Populated only when Enabled is true.
	LegacySentinel bool
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
	scan, err := analyzeCanonicalForm(src, true)
	if err != nil {
		return CanonicalFormReport{}, err
	}
	if !scan.enabled {
		return CanonicalFormReport{}, nil
	}
	return CanonicalFormReport{Enabled: scan.enabled, LegacySentinel: scan.legacy, Scattered: scatteredNames(scan.order)}, nil
}

// MigrateLegacySentinel rewrites a legacy `# canonical-form` opt-in comment
// to the structured `# doppelgang: canonical` directive, in place,
// preserving the line's indentation. Returns src unchanged (changed=false)
// when no legacy sentinel is present — including when the flake has not
// opted in at all, or already uses the structured directive.
func MigrateLegacySentinel(src []byte) (out []byte, changed bool, err error) {
	// wantOrder=false: this rewrite only needs bindingStart/legacy, not the
	// binding-order walk (which, in block form, costs a second grammar
	// parse of the inputs group) — skipping it avoids paying for work the
	// caller discards.
	scan, err := analyzeCanonicalForm(src, false)
	if err != nil {
		return nil, false, err
	}
	if !scan.legacy {
		return src, false, nil
	}
	lineStartOff := lineStart(src, scan.bindingStart)
	prevLineEnd := lineStartOff - 1 // the '\n' terminating the sentinel's line
	prevLineStart := lineStart(src, prevLineEnd)
	// lineIndent measures whitespace between a line's start and the given
	// offset, so it needs an offset past the indent — prevLineEnd (the
	// line's last byte) rather than prevLineStart (which IS the indent's
	// start, yielding an empty measurement).
	indent := lineIndent(src, prevLineEnd)
	out = spliceRange(src, prevLineStart, prevLineEnd, indent+"# "+directivePrefix+" "+CanonicalFormDirective)
	return out, true, nil
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
// nothing to change when the flake has not opted in (neither opt-in spelling
// present) or has no scattered inputs.
func CanonicalFormFixTargets(src []byte) (deleteTargets, reapplyLines []string, err error) {
	scan, err := analyzeCanonicalForm(src, true)
	if err != nil {
		return nil, nil, err
	}
	if !scan.enabled {
		return nil, nil, nil
	}
	scattered := map[string]bool{}
	for _, name := range scatteredNames(scan.order) {
		scattered[name] = true
	}
	for _, b := range scan.order {
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

// canonicalFormScan is what analyzeCanonicalForm's callers need, named
// rather than positional so each caller's `scan.foo` reads self-documented
// instead of a run of blank-discarded positionals.
type canonicalFormScan struct {
	// enabled and legacy are canonicalFormOptIn's result for the governing
	// `inputs` binding's preceding line.
	enabled, legacy bool
	// bindingStart is the byte offset of that `inputs` binding — the
	// anchor MigrateLegacySentinel walks backward from to find the opt-in
	// comment's line.
	bindingStart int
	// order is the in-file-order sequence of bindings directly under
	// `inputs`, uniformly across block and flat form. Left nil when the
	// caller passed wantOrder=false to analyzeCanonicalForm.
	order []inputBinding
}

// analyzeCanonicalForm parses src and reports its canonical-form opt-in
// status. When wantOrder is true it additionally walks the in-file-order
// sequence of bindings directly under `inputs` — in block form this costs a
// second grammar parse of the group's text (blockBindingOrder), so callers
// that only need the opt-in status (MigrateLegacySentinel) pass false to
// skip work they'd otherwise discard.
func analyzeCanonicalForm(src []byte, wantOrder bool) (canonicalFormScan, error) {
	matcher, err := newMatcher()
	if err != nil {
		return canonicalFormScan{}, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
	if err != nil {
		return canonicalFormScan{}, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	root, ok := tree.Root()
	if !ok {
		return canonicalFormScan{}, ErrUnparseable
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return canonicalFormScan{}, ErrUnparseable
	}

	var (
		scan       canonicalFormScan
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
			scan.bindingStart = tree.Span(child).Start.Cursor
			scan.enabled, scan.legacy = canonicalFormOptIn(src, scan.bindingStart)
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
		if !wantOrder {
			continue
		}
		// Flat form: `inputs.<x>… = …`. Strip the leading "inputs" segment
		// so the path is relative like block form's, and share its
		// per-binding conversion.
		scan.order = append(scan.order, bindingFromRelPath(path[1:], val, tree))
	}
	if haveBlock && wantOrder {
		// groupBlock's text is read from the still-valid outer tree before
		// blockBindingOrder re-parses it (invalidating node IDs) — same
		// ordering hazard documented in blockChunkOffsets.
		scan.order = blockBindingOrder(matcher, []byte(tree.Text(blockGroup)))
	}
	return scan, nil
}

// canonicalFormOptIn reports whether the line immediately above byte offset
// off opts a flake into canonical-form enforcement — either the structured
// `# doppelgang: canonical` directive or the legacy `# canonical-form`
// sentinel (legacy=true in that case, so callers know MigrateLegacySentinel
// has something to rewrite).
func canonicalFormOptIn(src []byte, off int) (enabled, legacy bool) {
	line, ok := precedingLine(src, off)
	if !ok {
		return false, false
	}
	if line == canonicalFormLegacySentinel {
		return true, true
	}
	if name, dOK := parseDirective(line); dOK && name == CanonicalFormDirective {
		return true, false
	}
	return false, false
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
