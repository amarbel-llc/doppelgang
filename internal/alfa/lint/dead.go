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
	// ViaAbsentHop is true when this override is dead because a hop in its
	// prefix path — not necessarily the final one — names an input its node
	// doesn't declare at all, rather than because the FINAL element (Input)
	// is absent from Target's declared inputs (see resolveOverrideFrom).
	// Like ViaFollow, this means the path never fully resolves to Target, so
	// callers must not render the ordinary "Target has no input Input"
	// message for it.
	ViaAbsentHop bool
}

// DeadOverrides resolves each override (a full-form `inputs.<dep>…inputs.<x>.follows`
// attr-path string, as produced from a flake.nix) against the lock and returns
// those that are dead: the overridden input <x> is absent from the declared
// inputs of the node the override's prefix chain resolves to from root, the
// chain traverses a followed input along the way, or a hop in the chain names
// an input that doesn't exist at all (see resolveOverrideFrom).
//
// It is deliberately conservative — any override it cannot confidently
// resolve (malformed shape, a hop's starting node itself missing from the
// lock — see resolveOverrideFrom) is skipped rather than flagged, so it
// never reports a false positive. Results are tagged Direct because the
// overrides come from the linted flake.nix; the transitive (upstream) path
// tags its findings separately. Output is sorted by Override for
// determinism.
func DeadOverrides(l *flakelock.Lock, overrides []string) []DeadOverride {
	out := make([]DeadOverride, 0)
	for _, ov := range overrides {
		chain, ok := overrideChain(ov)
		if !ok {
			continue
		}
		target, dead, viaFollow, viaAbsentHop, ok := resolveOverride(l, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override:     ov,
			Target:       joinSlash(target),
			Input:        chain[len(chain)-1],
			Direct:       true,
			ViaFollow:    viaFollow,
			ViaAbsentHop: viaAbsentHop,
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
		target, dead, viaFollow, viaAbsentHop, ok := resolveOverrideFrom(l, startKey, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override:     ov,
			Target:       joinSlash(target),
			Input:        chain[len(chain)-1],
			Direct:       false,
			Via:          via,
			ViaFollow:    viaFollow,
			ViaAbsentHop: viaAbsentHop,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Override < out[j].Override })
	return out
}

// resolveOverride resolves an override declared in the linted flake.nix,
// walking from the lock root. See resolveOverrideFrom.
func resolveOverride(l *flakelock.Lock, chain []string) (target []string, dead, viaFollow, viaAbsentHop, ok bool) {
	return resolveOverrideFrom(l, l.Root, chain)
}

// resolveOverrideFrom walks the override's prefix (every chain element but the
// last) from startKey via string-form node edges, then reports whether the
// final element is absent from the reached node's declared inputs (its Inputs
// keys, which include both node edges and legitimate follows arrays). target
// is the resolved prefix path. ok is false only when a hop's *starting* node
// is itself missing from the lock (l.Nodes[cur] doesn't exist) — a
// malformed-lock case that should not arise for a validly-resolved cur, kept
// as a defensive skip rather than a hard error.
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
// walk.)
//
// A prefix hop that names an input the current node simply doesn't declare
// at all — has=false, the sibling gap to the followed-edge case — likewise
// short-circuits to dead=true (viaAbsentHop=true), rather than the previous
// behavior of bailing with ok=false (skip, not flagged). The override's path
// can never resolve past a hop that doesn't exist, so it is exactly as
// provably inert as a directly-absent final Input. (#32 — same skip-vs-flag
// bug as #30, one hop earlier: a node dropping an input entirely, e.g. a
// dependency losing a transitive input across an upstream bump, silently
// stranded every override declared beneath the old path.)
//
// Because viaFollow and viaAbsentHop overrides both mean the path never
// fully reaches Target, callers must not render either with the ordinary
// "Target has no input Input" message — Target may legitimately declare
// Input in the viaFollow case, and in the viaAbsentHop case Target itself
// may not even be where the break occurred.
func resolveOverrideFrom(l *flakelock.Lock, startKey string, chain []string) (target []string, dead, viaFollow, viaAbsentHop, ok bool) {
	cur := startKey
	prefix := chain[:len(chain)-1]
	for _, name := range prefix {
		node, exists := l.Nodes[cur]
		if !exists {
			return nil, false, false, false, false
		}
		ref, has := node.Inputs[name]
		if !has {
			return prefix, true, false, true, true
		}
		if ref.Node == "" {
			return prefix, true, true, false, true
		}
		cur = ref.Node
	}
	node, exists := l.Nodes[cur]
	if !exists {
		return nil, false, false, false, false
	}
	_, declared := node.Inputs[chain[len(chain)-1]]
	return prefix, !declared, false, false, true
}
