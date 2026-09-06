package nixedit

import (
	"fmt"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// OutputsShape classifies a flake's `outputs` function argument set by
// whether it accepts inputs the signature does not name — the property that
// decides whether splicing a new input into `inputs` is safe.
//
// Nix calls `outputs` with one attrset holding `self` plus every declared
// input. A formals set that enumerates names without an ellipsis is CLOSED:
// passing it an argument it does not name is the eval error
//
//	error: function 'outputs' called with unexpected argument 'nixpkgs-master'
//
// so splicing an input into such a flake breaks it unless the signature is
// widened in the same edit.
type OutputsShape int

const (
	// OutputsAbsent — no top-level `outputs` binding was found, or its value
	// is not a function this shallow grammar can model. Callers should treat
	// it as "unknown" and neither report nor repair.
	OutputsAbsent OutputsShape = iota
	// OutputsSimpleArg — `outputs = inputs: …`. A single identifier binds the
	// whole attrset, so every input is accepted. Nothing to widen.
	OutputsSimpleArg
	// OutputsEllipsis — `outputs = { self, … , ... }: …`. Formals already
	// carry `...`, so extra inputs are absorbed. Nothing to widen.
	OutputsEllipsis
	// OutputsClosed — `outputs = { self, nixpkgs }: …`. Formals enumerate
	// names with no `...`; an input not named here is an eval error.
	OutputsClosed
)

func (s OutputsShape) String() string {
	switch s {
	case OutputsSimpleArg:
		return "simple-arg"
	case OutputsEllipsis:
		return "ellipsis"
	case OutputsClosed:
		return "closed"
	default:
		return "absent"
	}
}

// AcceptsUnnamedInputs reports whether an outputs signature of this shape
// tolerates an input its formals do not name.
func (s OutputsShape) AcceptsUnnamedInputs() bool {
	return s == OutputsSimpleArg || s == OutputsEllipsis
}

// OutputsFormals classifies the top-level `outputs` binding's argument set
// and, for a formals set, returns the names it binds in source order
// (excluding `...`). ErrUnparseable is returned when the shallow grammar
// cannot parse the file, so callers fall back to skipping the check.
//
// A file with no recognisable `outputs` function yields OutputsAbsent and no
// error: the conservative outcome is "say nothing", never a false finding.
func OutputsFormals(src []byte) (shape OutputsShape, names []string, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return OutputsAbsent, nil, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	loc, err := locateOutputsFormals(matcher, src)
	if err != nil {
		return OutputsAbsent, nil, err
	}
	if loc == nil {
		return OutputsAbsent, nil, nil
	}
	if loc.simpleArg {
		return OutputsSimpleArg, nil, nil
	}
	if loc.hasEllipsis {
		return OutputsEllipsis, loc.names, nil
	}
	return OutputsClosed, loc.names, nil
}

// WidenOutputsFormals edits flake.nix so the `outputs` function accepts
// inputs its formals do not name, by appending `...` to a closed formals
// set. The rest of the file is preserved byte-for-byte.
//
// It is the repair companion to OutputsFormals and is a no-op
// (changed=false, out==src) for every shape that already accepts unnamed
// inputs — a simple-arg function, formals that already carry `...`, or a
// file with no recognisable `outputs`.
//
// The inserted text follows the surrounding layout so the result is
// nixfmt-stable: `...` goes on its own line at the formals' own indent when
// the set is written multi-line, and inline when it is written on one line.
// This matters because doppelgang's whole-tree repair runs AFTER conformist's
// formatter pass, so output that is not already formatted would be left
// unformatted in the tree.
func WidenOutputsFormals(src []byte) (out []byte, changed bool, err error) {
	matcher, err := newMatcher()
	if err != nil {
		return nil, false, fmt.Errorf("nixedit: compile grammar: %w", err)
	}
	loc, err := locateOutputsFormals(matcher, src)
	if err != nil {
		return nil, false, err
	}
	if loc == nil || loc.simpleArg || loc.hasEllipsis {
		return src, false, nil
	}
	insertAt, text := loc.ellipsisInsertion(src)
	out = make([]byte, 0, len(src)+len(text))
	out = append(out, src[:insertAt]...)
	out = append(out, text...)
	out = append(out, src[insertAt:]...)
	return out, true, nil
}

// outputsFormals is a located `outputs` argument set: the absolute span of
// its `{ … }` formals group, the names it binds, and whether it already
// carries `...`. simpleArg marks the `outputs = inputs: …` form, which has no
// formals group at all.
type outputsFormals struct {
	span        span // covers the formals group including both braces
	names       []string
	hasEllipsis bool
	simpleArg   bool
}

// locateOutputsFormals finds the top-level `outputs` binding and inspects the
// head of its value.
//
// The shallow grammar skips a binding's value opaquely, and an `outputs`
// value of the common `{ … }: let … in …` shape terminates that skip early at
// the `let`'s first inner `;` (the leftover is then absorbed by OpaqueTail).
// That truncation is harmless here: the formals group is the FIRST item of
// the value, long before any `let`, so the head of the value is always intact
// even when its tail is not.
//
// Returns nil (no error) when there is no top-level `outputs` binding or its
// value does not begin with a formals group or a simple identifier argument.
func locateOutputsFormals(matcher langlang.Matcher, src []byte) (*outputsFormals, error) {
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

	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, val, ok := keyValPath(tree, kv, src)
		if !ok || len(path) != 1 || path[0] != "outputs" {
			continue
		}
		return classifyOutputsValue(tree, val), nil
	}
	return nil, nil
}

// classifyOutputsValue inspects the leading item of the `outputs` value: a
// Group is the formals set, non-blank text is the `inputs:` simple-arg form.
// Anything else yields nil so the caller stays silent.
func classifyOutputsValue(tree langlang.Tree, val langlang.NodeID) *outputsFormals {
	for _, item := range valueItems(tree, val) {
		switch nodeName(tree, item) {
		case "Group":
			text := tree.Text(item)
			if len(text) == 0 || text[0] != '{' {
				// A list or paren head is not a formals set.
				return nil
			}
			sp := tree.Span(item)
			// A leading `{ … }` only denotes formals when it is the argument
			// side of a function, i.e. followed by `:` (optionally via an
			// `@name` binding). Without this guard a value that merely BEGINS
			// with an attrset — `outputs = { } // f;` — would be treated as a
			// signature and have `...` spliced into it, corrupting the file.
			if !followedByLambdaColon(tree, val, item) {
				return nil
			}
			sc := scanFormals(text)
			return &outputsFormals{
				span:        span{start: sp.Start.Cursor, end: sp.End.Cursor},
				names:       sc.names,
				hasEllipsis: sc.hasEllipsis,
			}
		case "Comment":
			continue
		case "OuterText":
			text := strings.TrimSpace(tree.Text(item))
			if text == "" {
				continue
			}
			// `outputs = args@{ self, nixpkgs }: …` — the name-first spelling
			// of an @-binding. Nix still enforces the formals that follow, so
			// this is NOT a catch-all argument: keep scanning for the group.
			// Reading it as simple-arg would hide a closed signature and make
			// both the check and the repair silently do nothing.
			if isIdentifierAtPrefix(text) {
				continue
			}
			// `outputs = inputs: …` — a single identifier binds everything.
			return &outputsFormals{simpleArg: true}
		default:
			return nil
		}
	}
	return nil
}

// followedByLambdaColon reports whether the item after group inside the same
// Value begins the `:` (or `@name:`) that makes the preceding `{ … }` a
// function's argument set rather than an ordinary attrset expression.
//
// Only the text immediately following the group is inspected, and only up to
// the first non-space character (after an optional `@name`), so a `:` that
// appears later in the body cannot be mistaken for the lambda's.
func followedByLambdaColon(tree langlang.Tree, val, group langlang.NodeID) bool {
	items := valueItems(tree, val)
	for i, item := range items {
		if item != group {
			continue
		}
		for _, rest := range items[i+1:] {
			switch nodeName(tree, rest) {
			case "Comment":
				continue
			case "OuterText", "InnerText":
				text := strings.TrimLeft(tree.Text(rest), " \t\r\n")
				if text == "" {
					continue
				}
				if text[0] == '@' {
					text = strings.TrimLeft(text[1:], " \t\r\n")
					j := 0
					for j < len(text) && isIdentByte(text[j]) {
						j++
					}
					text = strings.TrimLeft(text[j:], " \t\r\n")
				}
				return strings.HasPrefix(text, ":")
			default:
				return false
			}
		}
		return false
	}
	return false
}
