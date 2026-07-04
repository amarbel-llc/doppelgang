// Package lint analyzes a parsed flake.lock for reducible duplication:
// inputs that pin an identical source more than once (collapsible via
// `follows`) and source repositories pinned at more than one revision.
package lint

import (
	"sort"
	"strings"

	"github.com/friedenberg/doppelgang/internal/0/flakelock"
)

// Report is the result of analyzing a flake.lock.
type Report struct {
	// Follows lists groups of nodes pinning a byte-identical source that
	// could be collapsed to one node via `follows`.
	Follows []FollowsRec
	// MultiVersion lists source repositories pinned at more than one rev.
	MultiVersion []MultiVersionInput
	// DeadOverrides lists `follows` overrides whose target input the
	// dependency does not declare. Unlike Follows / MultiVersion (computed
	// from the lock alone by Analyze), these require the override bindings
	// parsed from a flake.nix; see DeadOverrides. Analyze leaves this nil.
	DeadOverrides []DeadOverride
	// NixpkgsMaster is non-nil when the flake's top-level `nixpkgs-master`
	// input does not conform to the SHA-pinned convention
	// (github:NixOS/nixpkgs/<40-hex>). It is computed from flake.nix alone
	// (not the lock); a conformant flake — and Analyze, which never runs
	// this check — leaves it nil. See CheckNixpkgsMaster.
	NixpkgsMaster *NixpkgsMasterFinding
}

// FollowsRec recommends collapsing a set of nodes that pin an identical
// source down to the single canonical node.
type FollowsRec struct {
	// Identity describes the shared source, e.g. "NixOS/nixpkgs @ d233902".
	Identity string
	// Canonical is the follows target: the attr-path of the node the
	// others should follow, rendered slash-joined (e.g. "nixpkgs/systems").
	Canonical string
	// Lines are the concrete flake.nix edits to add, one per *un-shadowed*
	// redundant node, e.g. `inputs.nixpkgs.inputs.systems.follows = "..."`.
	Lines []string
	// NodeCount is the total number of nodes sharing this identity in
	// the lockfile (canonical + every redundant member, including those
	// whose follows line was shadow-pruned and so does not appear in
	// Lines). The renderer uses this for the "pinned N×" header so the
	// count reflects the true duplication rather than the post-prune
	// emit count.
	NodeCount int
}

// MultiVersionInput flags a source repository pinned at more than one rev
// across the dependency graph.
type MultiVersionInput struct {
	Source   string // "owner/repo"
	Versions []InputVersion
}

// InputVersion is one rev of a multi-versioned source, with a sample
// attr-path that reaches it.
type InputVersion struct {
	Rev  string
	Path string // slash-joined attr-path, e.g. "nixpkgs/nixpkgs-master"
}

// pinned is a reachable node that carries a Locked pin.
type pinned struct {
	key  string
	path []string
	lk   *flakelock.Locked
}

// Analyze runs both flake.lock analyses and returns their findings.
func Analyze(l *flakelock.Lock) Report {
	paths := attrPaths(l)
	indeg := inDegrees(l)

	nodes := make([]pinned, 0, len(paths))
	for key, path := range paths {
		if key == l.Root {
			continue
		}
		if n := l.Nodes[key]; n.Locked != nil {
			nodes = append(nodes, pinned{key: key, path: path, lk: n.Locked})
		}
	}

	return Report{
		Follows:      followsRecs(nodes, indeg),
		MultiVersion: multiVersion(nodes),
	}
}

// followsRecs groups reachable, pinned nodes by exact source identity and
// emits a recommendation per group with more than one member. Output is
// shadow-pruned: a redundant-node line is dropped when a strict prefix of
// its path is itself a redundant-node line in the same output, because
// applying the outer follows collapses the path it would override. A
// group whose every line is shadowed is omitted entirely. This is a
// structural pass; it does not verify that the resulting lockfile is
// duplicate-free (the canonical's own sub-inputs may not align), so a
// re-run after applying the suggestions can still surface residual
// duplicates as new findings.
func followsRecs(nodes []pinned, indeg map[string]int) []FollowsRec {
	byIdentity := map[string][]pinned{}
	for _, p := range nodes {
		id := identity(p.lk)
		if id == "" {
			continue
		}
		byIdentity[id] = append(byIdentity[id], p)
	}

	type sortedGroup struct {
		canonical pinned
		members   []pinned
	}
	groups := make([]sortedGroup, 0, len(byIdentity))
	redundantPaths := map[string]bool{}
	for _, group := range byIdentity {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return less(group[i].path, group[j].path) })
		groups = append(groups, sortedGroup{canonical: group[0], members: group[1:]})
		for _, m := range group[1:] {
			redundantPaths[joinSlash(m.path)] = true
		}
	}

	isShadowed := func(p []string) bool {
		for i := 1; i < len(p); i++ {
			if redundantPaths[joinSlash(p[:i])] {
				return true
			}
		}
		return false
	}

	recs := make([]FollowsRec, 0, len(groups))
	for _, g := range groups {
		rec := FollowsRec{
			Identity:  describe(g.canonical.lk),
			Canonical: joinSlash(g.canonical.path),
			NodeCount: 1 + len(g.members),
		}
		for _, m := range g.members {
			if isShadowed(m.path) {
				continue
			}
			line := followsLine(m.path, g.canonical.path)
			if indeg[m.key] > 1 {
				line += "   # node has multiple parents; repeat for each"
			}
			rec.Lines = append(rec.Lines, line)
		}
		if len(rec.Lines) == 0 {
			continue
		}
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Canonical != recs[j].Canonical {
			return recs[i].Canonical < recs[j].Canonical
		}
		return recs[i].Identity < recs[j].Identity
	})
	return recs
}

// multiVersion groups pinned nodes by owner/repo and flags any source
// pinned at more than one distinct rev.
func multiVersion(nodes []pinned) []MultiVersionInput {
	bySource := map[string]map[string][]string{} // source -> rev -> shortest path
	for _, p := range nodes {
		src := source(p.lk)
		if src == "" || p.lk.Rev == "" {
			continue
		}
		revs := bySource[src]
		if revs == nil {
			revs = map[string][]string{}
			bySource[src] = revs
		}
		if cur, ok := revs[p.lk.Rev]; !ok || less(p.path, cur) {
			revs[p.lk.Rev] = p.path
		}
	}

	out := make([]MultiVersionInput, 0)
	for src, revs := range bySource {
		if len(revs) < 2 {
			continue
		}
		mv := MultiVersionInput{Source: src}
		for rev, path := range revs {
			mv.Versions = append(mv.Versions, InputVersion{Rev: rev, Path: joinSlash(path)})
		}
		sort.Slice(mv.Versions, func(i, j int) bool { return mv.Versions[i].Rev < mv.Versions[j].Rev })
		out = append(out, mv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// attrPaths returns, for each node reachable from root via string-form
// inputs, the shortest attribute path from root (root maps to the empty
// path). Ties between equal-length paths are broken lexically so output
// is deterministic. Array-form (already-resolved follows) inputs are not
// traversed because they do not introduce nodes.
func attrPaths(l *flakelock.Lock) map[string][]string {
	paths := map[string][]string{l.Root: {}}
	level := []string{l.Root}
	for len(level) > 0 {
		sort.Slice(level, func(i, j int) bool {
			return joinSlash(paths[level[i]]) < joinSlash(paths[level[j]])
		})
		var next []string
		for _, cur := range level {
			node := l.Nodes[cur]
			base := paths[cur]
			for _, attr := range sortedNodeInputs(node) {
				child := node.Inputs[attr].Node
				if _, seen := paths[child]; seen {
					continue
				}
				p := make([]string, len(base)+1)
				copy(p, base)
				p[len(base)] = attr
				paths[child] = p
				next = append(next, child)
			}
		}
		level = next
	}
	return paths
}

// inDegrees counts incoming string-form (node) edges per node key.
func inDegrees(l *flakelock.Lock) map[string]int {
	indeg := map[string]int{}
	for _, n := range l.Nodes {
		for _, ref := range n.Inputs {
			if ref.Node != "" {
				indeg[ref.Node]++
			}
		}
	}
	return indeg
}

// sortedNodeInputs returns the node's string-form input attr names, sorted.
func sortedNodeInputs(n flakelock.Node) []string {
	attrs := make([]string, 0, len(n.Inputs))
	for attr, ref := range n.Inputs {
		if ref.Node != "" {
			attrs = append(attrs, attr)
		}
	}
	sort.Strings(attrs)
	return attrs
}

// identity is the byte-identity key for a locked pin: narHash when present
// (content hash), else the resolved rev, else the url. Two nodes sharing an
// identity are the same source and safe to dedupe via follows.
func identity(lk *flakelock.Locked) string {
	switch {
	case lk.NarHash != "":
		return "narHash:" + lk.NarHash
	case lk.Rev != "":
		return strings.Join([]string{"rev", lk.Type, lk.Owner, lk.Repo, lk.Rev}, ":")
	case lk.URL != "":
		return "url:" + lk.URL
	default:
		return ""
	}
}

// source is the location key (owner/repo) used for multi-version grouping.
// url-only sources are excluded: their urls embed the pinned rev, so equal
// urls are already an identity match and unequal urls are not comparable
// without fragile parsing.
func source(lk *flakelock.Locked) string {
	if lk.Owner != "" && lk.Repo != "" {
		return lk.Owner + "/" + lk.Repo
	}
	return ""
}

// describe renders a human-readable label for a locked pin.
func describe(lk *flakelock.Locked) string {
	if lk.Owner != "" && lk.Repo != "" {
		s := lk.Owner + "/" + lk.Repo
		if lk.Rev != "" {
			s += " @ " + shortRev(lk.Rev)
		}
		return s
	}
	if lk.URL != "" {
		return lk.URL
	}
	if lk.NarHash != "" {
		return lk.NarHash
	}
	return "(unknown source)"
}

// followsLine renders the flake.nix edit to make the input at dupPath
// follow the input at canonPath:
//
//	dupPath=["nixpkgs","systems"], canonPath=["nixpkgs"]  ->
//	`inputs.nixpkgs.inputs.systems.follows = "nixpkgs"`
func followsLine(dupPath, canonPath []string) string {
	return "inputs." + strings.Join(dupPath, ".inputs.") +
		".follows = \"" + joinSlash(canonPath) + "\""
}

func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

func joinSlash(p []string) string { return strings.Join(p, "/") }

// less orders attr-paths by length, then lexically on the slash-join.
func less(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return joinSlash(a) < joinSlash(b)
}
