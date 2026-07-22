package lint

import (
	"sort"
	"strings"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
)

// DeadReason is why a DeadOverride is dead — one of three peer failure
// modes a prefix-chain resolution can end in (see resolveOverrideFrom),
// mirroring NixpkgsMasterStatus's convention in this same package: there is
// no "live" value, since a DeadOverride is only ever constructed once one of
// these three has already been determined.
type DeadReason int

const (
	// DeadReasonInputAbsent: the ordinary case — Input is absent from
	// Target's fully-resolved declared inputs, e.g. Target declares no
	// input named Input at all.
	DeadReasonInputAbsent DeadReason = iota
	// DeadReasonViaFollow: the prefix path traverses a followed input
	// (#30). Target may legitimately declare Input in this case — the
	// override's path just never reaches it, since nix drops any override
	// declared beneath a redirected input entirely, regardless of whether
	// the redirect's destination happens to also declare an input of the
	// same name.
	DeadReasonViaFollow
	// DeadReasonViaAbsentHop: a hop before the final element — not
	// necessarily Target itself — names an input its node doesn't declare
	// at all (#32). The path can never resolve past a hop that doesn't
	// exist, so it is exactly as provably inert as DeadReasonInputAbsent.
	DeadReasonViaAbsentHop
)

// String renders the reason as the token used in diagnostics and the
// machine-readable formats.
func (r DeadReason) String() string {
	switch r {
	case DeadReasonInputAbsent:
		return "input-absent"
	case DeadReasonViaFollow:
		return "via-follow"
	case DeadReasonViaAbsentHop:
		return "via-absent-hop"
	default:
		return "unknown"
	}
}

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
	// Reason is why this override is dead. For DeadReasonViaFollow and
	// DeadReasonViaAbsentHop, the override's path never fully resolves to
	// Target at all — Target may legitimately declare Input in either case
	// — so callers must not render the ordinary "Target has no input Input"
	// message except for DeadReasonInputAbsent, or risk asserting something
	// false about the flake graph.
	Reason DeadReason
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
		target, dead, reason, ok := resolveOverride(l, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override: ov,
			Target:   joinSlash(target),
			Input:    chain[len(chain)-1],
			Direct:   true,
			Reason:   reason,
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
		target, dead, reason, ok := resolveOverrideFrom(l, startKey, chain)
		if !ok || !dead {
			continue
		}
		out = append(out, DeadOverride{
			Override: ov,
			Target:   joinSlash(target),
			Input:    chain[len(chain)-1],
			Direct:   false,
			Via:      via,
			Reason:   reason,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Override < out[j].Override })
	return out
}

// resolveOverride resolves an override declared in the linted flake.nix,
// walking from the lock root. See resolveOverrideFrom.
func resolveOverride(l *flakelock.Lock, chain []string) (target []string, dead bool, reason DeadReason, ok bool) {
	return resolveOverrideFrom(l, l.Root, chain)
}

// resolveOverrideFrom walks the override's prefix (every chain element but the
// last) from startKey via string-form node edges, then reports whether the
// final element is absent from the reached node's declared inputs (its Inputs
// keys, which include both node edges and legitimate follows arrays). target
// is the resolved prefix path. reason is meaningful only when dead is true.
// ok is false only when a hop's *starting* node is itself missing from the
// lock (l.Nodes[cur] doesn't exist) — a malformed-lock case that should not
// arise for a validly-resolved cur, kept as a defensive skip rather than a
// hard error.
//
// A prefix hop that is itself a follows-redirect (array-form InputRef, no
// Node) short-circuits to dead=true, DeadReasonViaFollow rather than
// resolving through it: once nix redirects an input via `follows`, that
// input's own subtree is never evaluated, so any override declared beneath
// it — however it reads syntactically, whatever name it targets — is inert,
// the same "has an override for a non-existent input" nix warns on for a
// directly-absent target. This is true regardless of whether the redirect's
// destination happens to also declare an input of the same name; the
// override's original path never reaches it. (#30 — collapsing an input via
// `follows --fix` previously left every override nested beneath it
// undetected as dead, in both DeadOverrides and TransitiveDeadOverrides,
// since they share this walk.)
//
// A prefix hop that names an input the current node simply doesn't declare
// at all — has=false, the sibling gap to the followed-edge case — likewise
// short-circuits to dead=true, DeadReasonViaAbsentHop, rather than the
// previous behavior of bailing with ok=false (skip, not flagged). The
// override's path can never resolve past a hop that doesn't exist, so it is
// exactly as provably inert as DeadReasonInputAbsent. (#32 — same
// skip-vs-flag bug as #30, one hop earlier: a node dropping an input
// entirely, e.g. a dependency losing a transitive input across an upstream
// bump, silently stranded every override declared beneath the old path.)
func resolveOverrideFrom(l *flakelock.Lock, startKey string, chain []string) (target []string, dead bool, reason DeadReason, ok bool) {
	cur := startKey
	prefix := chain[:len(chain)-1]
	for _, name := range prefix {
		node, exists := l.Nodes[cur]
		if !exists {
			return nil, false, 0, false
		}
		ref, has := node.Inputs[name]
		if !has {
			return prefix, true, DeadReasonViaAbsentHop, true
		}
		if ref.Node == "" {
			return prefix, true, DeadReasonViaFollow, true
		}
		cur = ref.Node
	}
	node, exists := l.Nodes[cur]
	if !exists {
		return nil, false, 0, false
	}
	_, declared := node.Inputs[chain[len(chain)-1]]
	return prefix, !declared, DeadReasonInputAbsent, true
}
