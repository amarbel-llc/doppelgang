package lint

import (
	"sort"
	"strings"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
)

// DeadOverride is a `follows` override that points at an input the targeted
// dependency does not declare — the condition Nix warns on as "has an
// override for a non-existent input". It overrides nothing and can be pruned.
type DeadOverride struct {
	// Override is the full override binding's LHS attr-path, e.g.
	// "inputs.bats.inputs.nixpkgs.follows".
	Override string
	// Target is the slash-joined dependency-input chain the overridden
	// input was supposed to attach to, e.g. "bats" (or "tommy/bats" for a
	// deeper override). It is the node Nix names in its warning.
	Target string
	// Input is the overridden input name that does not exist on Target,
	// e.g. "nixpkgs".
	Input string
	// Direct is true when the override binding lives in the linted
	// flake.nix (so `--fix` can prune it here). Transitive overrides
	// recovered from an upstream flake.nix set this false.
	Direct bool
	// Via names the upstream flake whose flake.nix carries a transitive
	// override (the place it must be fixed). It is empty for direct
	// overrides; the best-effort online transitive path populates it.
	Via string
	// ViaFollow is true when this override is dead because its prefix path
	// traverses a followed input, rather than because Input is absent from
	// Target's declared inputs (see resolveOverrideFrom). Target may
	// legitimately declare Input in this case — the override's path just
	// never reaches it — so callers rendering "Target has no input Input"
	// for the ordinary case must render a different message here, or risk
	// asserting something false about the flake graph.
	ViaFollow bool
}

// DeadOverrides resolves each override (a full-form `inputs.<dep>…inputs.<x>.follows`
// attr-path string, as produced from a flake.nix) against the lock and returns
// those that are dead: either the overridden input <x> is absent from the
// declared inputs of the node the override's prefix chain resolves to from
// root, or the chain traverses a followed input along the way (see
// resolveOverrideFrom).
//
// It is deliberately conservative — any override it cannot confidently resolve
// (malformed shape, a dependency that is not a node input of root, an
// intermediate hop naming an input its node doesn't declare at all) is
// skipped rather than flagged, so it never reports a false positive. Results
// are tagged Direct because the overrides come from the linted flake.nix; the
// transitive (upstream) path tags its findings separately. Output is sorted
// by Override for determinism.
func DeadOverrides(l *flakelock.Lock, overrides []string) []DeadOverride {
	out := make([]DeadOverride, 0)
	for _, ov := range overrides {
		chain, ok := overrideChain(ov)
		if !ok {
			continue
		}
		target, dead, viaFollow, ok := resolveOverride(l, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override:  ov,
			Target:    joinSlash(target),
			Input:     chain[len(chain)-1],
			Direct:    true,
			ViaFollow: viaFollow,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Override < out[j].Override })
	return out
}

// overrideChain decomposes a follows override LHS into the dependency-input
// chain it targets. The canonical form alternates the literal "inputs" with
// input names and ends in "follows":
//
//	inputs.bats.inputs.nixpkgs.follows  ->  ["bats", "nixpkgs"]
//
// The last element is the overridden input; the preceding elements are the
// dependency path from root to the node it attaches to. A chain shorter than
// two is not a dependency override (e.g. `inputs.x.follows` is the linted
// flake's own input following another) and yields ok=false.
func overrideChain(lhs string) ([]string, bool) {
	segs := strings.Split(lhs, ".")
	if len(segs) < 5 || segs[len(segs)-1] != "follows" {
		return nil, false
	}
	body := segs[:len(segs)-1] // drop trailing "follows"
	var chain []string
	for i, s := range body {
		if i%2 == 0 {
			if s != "inputs" {
				return nil, false
			}
			continue
		}
		chain = append(chain, s)
	}
	// A well-formed body has even length (inputs/name pairs), so the last
	// element parsed must be a name, not a dangling "inputs".
	if len(body)%2 != 0 || len(chain) < 2 {
		return nil, false
	}
	return chain, true
}

// TransitiveDeadOverrides is the upstream analogue of DeadOverrides: it
// resolves overrides recovered from an upstream flake's flake.nix (the flake
// at node startKey) against that node's subtree in the lock, flagging those
// whose target input the dependency does not declare. Findings are tagged
// Direct=false and Via=via (the upstream flake, where the fix must land).
// Like DeadOverrides it is conservative — anything it cannot resolve from
// startKey is skipped. Output is sorted by Override.
func TransitiveDeadOverrides(l *flakelock.Lock, startKey string, overrides []string, via string) []DeadOverride {
	out := make([]DeadOverride, 0)
	for _, ov := range overrides {
		chain, ok := overrideChain(ov)
		if !ok {
			continue
		}
		target, dead, viaFollow, ok := resolveOverrideFrom(l, startKey, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override:  ov,
			Target:    joinSlash(target),
			Input:     chain[len(chain)-1],
			Direct:    false,
			Via:       via,
			ViaFollow: viaFollow,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Override < out[j].Override })
	return out
}

// resolveOverride resolves an override declared in the linted flake.nix,
// walking from the lock root. See resolveOverrideFrom.
func resolveOverride(l *flakelock.Lock, chain []string) (target []string, dead, viaFollow, ok bool) {
	return resolveOverrideFrom(l, l.Root, chain)
}

// resolveOverrideFrom walks the override's prefix (every chain element but the
// last) from startKey via string-form node edges, then reports whether the
// final element is absent from the reached node's declared inputs (its Inputs
// keys, which include both node edges and legitimate follows arrays). target
// is the resolved prefix path. ok is false when the prefix cannot be fully
// resolved at all (an intermediate hop names an input the current node
// doesn't declare) — caller skips those rather than risk a false positive.
//
// A prefix hop that is itself a follows-redirect (array-form InputRef, no
// Node) short-circuits to dead=true (viaFollow=true) rather than resolving
// through it: once nix redirects an input via `follows`, that input's own
// subtree is never evaluated, so any override declared beneath it — however
// it reads syntactically, whatever name it targets — is inert, the same "has
// an override for a non-existent input" nix warns on for a directly-absent
// target. This is true regardless of whether the redirect's destination
// happens to also declare an input of the same name; the override's original
// path never reaches it. (#30 — collapsing an input via `follows --fix`
// previously left every override nested beneath it undetected as dead, in
// both DeadOverrides and TransitiveDeadOverrides, since they share this
// walk.) Because viaFollow overrides may legitimately have their Input
// declared on Target, callers must not render them with the ordinary "Target
// has no input Input" message.
func resolveOverrideFrom(l *flakelock.Lock, startKey string, chain []string) (target []string, dead, viaFollow, ok bool) {
	cur := startKey
	prefix := chain[:len(chain)-1]
	for _, name := range prefix {
		node, exists := l.Nodes[cur]
		if !exists {
			return nil, false, false, false
		}
		ref, has := node.Inputs[name]
		if !has {
			return nil, false, false, false
		}
		if ref.Node == "" {
			return prefix, true, true, true
		}
		cur = ref.Node
	}
	node, exists := l.Nodes[cur]
	if !exists {
		return nil, false, false, false
	}
	_, declared := node.Inputs[chain[len(chain)-1]]
	return prefix, !declared, false, true
}
