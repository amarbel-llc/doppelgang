package lint

import (
	"os"
	"testing"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
)

// synthLock: shared and shared_2 are the same source (narHash sha-s),
// reachable as a/shared and b/shared respectively. a wins the canonical
// tie (lexical), so b's copy should be told to follow it.
const synthLock = `{
  "nodes": {
    "root": { "inputs": { "a": "a", "b": "b" } },
    "a": {
      "inputs": { "shared": "shared" },
      "locked": { "type": "github", "owner": "o", "repo": "a", "rev": "aaa", "narHash": "sha-a" }
    },
    "b": {
      "inputs": { "shared": "shared_2" },
      "locked": { "type": "github", "owner": "o", "repo": "b", "rev": "bbb", "narHash": "sha-b" }
    },
    "shared": { "locked": { "type": "github", "owner": "x", "repo": "s", "rev": "sss", "narHash": "sha-s" } },
    "shared_2": { "locked": { "type": "github", "owner": "x", "repo": "s", "rev": "sss", "narHash": "sha-s" } }
  },
  "root": "root",
  "version": 7
}`

func TestFollowsRecForIdenticalSource(t *testing.T) {
	l, err := flakelock.Parse([]byte(synthLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := Analyze(l)
	if len(r.Follows) != 1 {
		t.Fatalf("want 1 follows rec, got %d: %+v", len(r.Follows), r.Follows)
	}
	rec := r.Follows[0]
	if rec.Canonical != "a/shared" {
		t.Errorf("Canonical = %q, want a/shared", rec.Canonical)
	}
	if len(rec.Lines) != 1 || rec.Lines[0] != `inputs.b.inputs.shared.follows = "a/shared"` {
		t.Errorf("Lines = %v, want [inputs.b.inputs.shared.follows = \"a/shared\"]", rec.Lines)
	}
}

// shadowingLock: two duplicate-source identity groups whose redundant
// paths are nested. Group sha-X has members at [a,x] and [b]; group
// sha-Y has members at [a,x,y] and [c]. Lint should emit
// `inputs.a.inputs.x.follows = "b"` for sha-X and would emit
// `inputs.a.inputs.x.inputs.y.follows = "c"` for sha-Y — but the
// latter's path has a/x as a strict prefix, which is itself a redundant
// path in the same output. Path-prefix shadow-pruning drops it.
const shadowingLock = `{
  "nodes": {
    "root": { "inputs": { "a": "a", "b": "b", "c": "c" } },
    "a": {
      "inputs": { "x": "aX" },
      "locked": { "type": "github", "owner": "o", "repo": "a", "rev": "aaa", "narHash": "sha-a" }
    },
    "aX": {
      "inputs": { "y": "aXy" },
      "locked": { "type": "github", "owner": "o", "repo": "x", "rev": "xxx", "narHash": "sha-X" }
    },
    "aXy": {
      "locked": { "type": "github", "owner": "o", "repo": "y", "rev": "yyy", "narHash": "sha-Y" }
    },
    "b": {
      "locked": { "type": "github", "owner": "o", "repo": "x", "rev": "xxx", "narHash": "sha-X" }
    },
    "c": {
      "locked": { "type": "github", "owner": "o", "repo": "y", "rev": "yyy", "narHash": "sha-Y" }
    }
  },
  "root": "root",
  "version": 7
}`

func TestFollowsRecPrunesPathPrefixShadow(t *testing.T) {
	l, err := flakelock.Parse([]byte(shadowingLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := Analyze(l)
	if len(r.Follows) != 1 {
		t.Fatalf("want 1 follows rec after shadow-prune, got %d: %+v", len(r.Follows), r.Follows)
	}
	rec := r.Follows[0]
	if len(rec.Lines) != 1 {
		t.Fatalf("want 1 line, got %d: %+v", len(rec.Lines), rec.Lines)
	}
	if rec.Lines[0] != `inputs.a.inputs.x.follows = "b"` {
		t.Errorf("Lines[0] = %q, want inputs.a.inputs.x.follows = \"b\"", rec.Lines[0])
	}
}

// deadOverrideLock: root pins a real dependency `dep` whose own declared
// inputs are `keep` (a legitimate follows, array form) and `real` (a node
// edge). An override targeting `dep`'s `gone` input is dead (dep declares
// no `gone`); overrides targeting `keep` or `real` are live. An override on
// `absent` (not even a node input of root) cannot be resolved and must be
// skipped, not flagged.
const deadOverrideLock = `{
  "nodes": {
    "root": { "inputs": { "dep": "dep", "real": "real" } },
    "dep": {
      "inputs": { "keep": ["keep"], "real": "real" },
      "locked": { "type": "github", "owner": "o", "repo": "dep", "rev": "ddd", "narHash": "sha-d" }
    },
    "real": { "locked": { "type": "github", "owner": "o", "repo": "real", "rev": "rrr", "narHash": "sha-r" } }
  },
  "root": "root",
  "version": 7
}`

func TestDeadOverridesFlagsOnlyDeadOnes(t *testing.T) {
	l, err := flakelock.Parse([]byte(deadOverrideLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := DeadOverrides(l, []string{
		`inputs.dep.inputs.gone.follows`, // dead: dep has no `gone`
		`inputs.dep.inputs.keep.follows`, // live: dep declares `keep` (follows)
		`inputs.dep.inputs.real.follows`, // live: dep declares `real` (node)
		`inputs.absent.inputs.x.follows`, // skip: `absent` is not a node input of root
		`inputs.dep.follows`,             // skip: top-level follows, not a dependency override
	})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 dead override, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.Override != `inputs.dep.inputs.gone.follows` {
		t.Errorf("Override = %q, want inputs.dep.inputs.gone.follows", d.Override)
	}
	if d.Target != "dep" {
		t.Errorf("Target = %q, want dep", d.Target)
	}
	if d.Input != "gone" {
		t.Errorf("Input = %q, want gone", d.Input)
	}
	if !d.Direct {
		t.Errorf("Direct = false, want true (overrides came from the linted flake.nix)")
	}
}

// transitiveLock models root → tacky → bats, where bats declares only
// `igloo`. tacky's own flake.nix overrides bats's nixpkgs/utils/igloo inputs;
// the first two are dead (bats has no nixpkgs/utils), igloo is live.
const transitiveLock = `{
  "nodes": {
    "root": { "inputs": { "tacky": "tacky" } },
    "tacky": {
      "inputs": { "bats": "bats" },
      "locked": { "type": "github", "owner": "amarbel-llc", "repo": "tacky", "rev": "ttt", "narHash": "sha-t" }
    },
    "bats": {
      "inputs": { "igloo": "igloo" },
      "locked": { "type": "github", "owner": "amarbel-llc", "repo": "bats", "rev": "bbb", "narHash": "sha-b" }
    },
    "igloo": { "locked": { "type": "github", "owner": "amarbel-llc", "repo": "igloo", "rev": "iii", "narHash": "sha-i" } }
  },
  "root": "root",
  "version": 7
}`

func TestTransitiveDeadOverrides(t *testing.T) {
	l, err := flakelock.Parse([]byte(transitiveLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := TransitiveDeadOverrides(l, "tacky", []string{
		`inputs.bats.inputs.nixpkgs.follows`, // dead: bats has no nixpkgs
		`inputs.bats.inputs.utils.follows`,   // dead: bats has no utils
		`inputs.bats.inputs.igloo.follows`,   // live: bats declares igloo
	}, "amarbel-llc/tacky")
	if len(got) != 2 {
		t.Fatalf("want 2 transitive dead overrides, got %d: %+v", len(got), got)
	}
	for _, d := range got {
		if d.Direct {
			t.Errorf("transitive override must have Direct=false: %+v", d)
		}
		if d.Via != "amarbel-llc/tacky" {
			t.Errorf("Via = %q, want amarbel-llc/tacky", d.Via)
		}
		if d.Target != "bats" {
			t.Errorf("Target = %q, want bats", d.Target)
		}
	}
	if got[0].Input != "nixpkgs" || got[1].Input != "utils" {
		t.Errorf("inputs = %q,%q want nixpkgs,utils (sorted by Override)", got[0].Input, got[1].Input)
	}
}

// collapsedFollowsLock reproduces #30's eng repro: `follows --fix` collapsed
// just-us's `bats` input onto chrest's copy (just-us.inputs.bats is now
// array-form, following ["chrest","bats"]), leaving behind overrides that
// were declared against the OLD real "bats" node just-us used to have —
// nested overrides Nix ignores entirely once an input is redirected via
// follows, regardless of whether the redirect's destination happens to also
// declare an input of the same name. chrest's actual bats node declares only
// "conformist" and "igloo" — notably NOT "nixpkgs" — so
// just-us.bats.nixpkgs.bun2nix is doubly dead (traverses the follows *and*
// its own prefix hop is absent), while just-us.bats.conformist is dead
// solely because it traverses the follows (chrest's bats does declare
// "conformist").
const collapsedFollowsLock = `{
  "nodes": {
    "root": { "inputs": { "just-us": "just-us", "chrest": "chrest" } },
    "just-us": {
      "inputs": { "bats": ["chrest", "bats"] },
      "locked": { "type": "github", "owner": "o", "repo": "just-us", "rev": "jjj", "narHash": "sha-j" }
    },
    "chrest": {
      "inputs": { "bats": "bats" },
      "locked": { "type": "github", "owner": "o", "repo": "chrest", "rev": "ccc", "narHash": "sha-c" }
    },
    "bats": {
      "inputs": { "conformist": "conformist", "igloo": "igloo" },
      "locked": { "type": "github", "owner": "o", "repo": "bats", "rev": "bbb", "narHash": "sha-b" }
    },
    "conformist": { "locked": { "type": "github", "owner": "o", "repo": "conformist", "rev": "cfc", "narHash": "sha-cf" } },
    "igloo": { "locked": { "type": "github", "owner": "o", "repo": "igloo", "rev": "iii", "narHash": "sha-i" } }
  },
  "root": "root",
  "version": 7
}`

// TestDeadOverridesFlagsOverridesBeneathFollowedInput is the offline
// (DeadOverrides, root-resolved) regression for #30: overrides nested
// beneath just-us's now-followed `bats` input are flagged dead, the
// collapse binding itself is untouched, and a legitimate override reached
// through a real (non-followed) node edge is still correctly left alone.
func TestDeadOverridesFlagsOverridesBeneathFollowedInput(t *testing.T) {
	l, err := flakelock.Parse([]byte(collapsedFollowsLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := DeadOverrides(l, []string{
		`inputs.just-us.inputs.bats.inputs.nixpkgs.inputs.bun2nix.follows`, // dead: traverses just-us's followed bats, AND chrest's bats has no nixpkgs
		`inputs.just-us.inputs.bats.inputs.conformist.follows`,             // dead: traverses just-us's followed bats (chrest's bats declaring "conformist" is irrelevant — the redirect is never reached through this path)
		`inputs.just-us.inputs.bats.follows`,                               // live: this IS the collapse binding, not a nested override
		`inputs.chrest.inputs.bats.inputs.igloo.follows`,                   // live: chrest.bats is a real node edge (not followed), and its bats declares igloo
		`inputs.chrest.inputs.bats.inputs.gone.follows`,                    // dead: real node edge, but chrest's bats has no "gone" (the pre-existing absent-input case, unaffected by this fix)
	})
	want := map[string]bool{
		`inputs.just-us.inputs.bats.inputs.nixpkgs.inputs.bun2nix.follows`: true,
		`inputs.just-us.inputs.bats.inputs.conformist.follows`:             true,
		`inputs.chrest.inputs.bats.inputs.gone.follows`:                    true,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d dead overrides, got %d: %+v", len(want), len(got), got)
	}
	for _, d := range got {
		if !want[d.Override] {
			t.Errorf("unexpected dead override: %s", d.Override)
		}
		if !d.Direct {
			t.Errorf("Direct = false for %s, want true", d.Override)
		}
	}
}

// TestTransitiveDeadOverridesFlagsOverridesBeneathFollowedInput is the
// --online analogue of the above: TransitiveDeadOverrides shares
// resolveOverrideFrom, so an upstream flake's own overrides beneath a
// followed input must be caught the same way, starting resolution from
// just-us itself rather than root.
func TestTransitiveDeadOverridesFlagsOverridesBeneathFollowedInput(t *testing.T) {
	l, err := flakelock.Parse([]byte(collapsedFollowsLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := TransitiveDeadOverrides(l, "just-us", []string{
		`inputs.bats.inputs.nixpkgs.inputs.bun2nix.follows`, // dead: traverses just-us's followed bats
		`inputs.bats.inputs.conformist.follows`,             // dead: same
	}, "o/just-us")
	if len(got) != 2 {
		t.Fatalf("want 2 transitive dead overrides, got %d: %+v", len(got), got)
	}
	// Target is always the override's full prefix chain (every intermediate
	// hop, not just the one where the followed edge was found) — the same
	// contract the pre-existing "absent input" dead-override path already
	// has, so a 3-element chain's Target is 2 segments deep even though
	// resolution stopped at the first ("bats").
	wantTarget := map[string]string{
		`inputs.bats.inputs.nixpkgs.inputs.bun2nix.follows`: "bats/nixpkgs",
		`inputs.bats.inputs.conformist.follows`:             "bats",
	}
	for _, d := range got {
		if d.Direct {
			t.Errorf("transitive override must have Direct=false: %+v", d)
		}
		if d.Via != "o/just-us" {
			t.Errorf("Via = %q, want o/just-us", d.Via)
		}
		if want, ok := wantTarget[d.Override]; ok && d.Target != want {
			t.Errorf("Target for %s = %q, want %q", d.Override, d.Target, want)
		}
	}
}

// multiVersionLock: two distinct revs of NixOS/nixpkgs reachable from root.
const multiVersionLock = `{
  "nodes": {
    "root": { "inputs": { "stable": "stable", "tool": "tool" } },
    "stable": { "locked": { "type": "github", "owner": "NixOS", "repo": "nixpkgs", "rev": "1111111", "narHash": "sha-1" } },
    "tool": {
      "inputs": { "nixpkgs": "nixpkgs" },
      "locked": { "type": "github", "owner": "t", "repo": "tool", "rev": "ttt", "narHash": "sha-t" }
    },
    "nixpkgs": { "locked": { "type": "github", "owner": "NixOS", "repo": "nixpkgs", "rev": "2222222", "narHash": "sha-2" } }
  },
  "root": "root",
  "version": 7
}`

func TestMultiVersionInput(t *testing.T) {
	l, err := flakelock.Parse([]byte(multiVersionLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := Analyze(l)
	if len(r.MultiVersion) != 1 {
		t.Fatalf("want 1 multi-version input, got %d: %+v", len(r.MultiVersion), r.MultiVersion)
	}
	mv := r.MultiVersion[0]
	if mv.Source != "NixOS/nixpkgs" {
		t.Errorf("Source = %q, want NixOS/nixpkgs", mv.Source)
	}
	if len(mv.Versions) != 2 {
		t.Errorf("want 2 versions, got %d: %+v", len(mv.Versions), mv.Versions)
	}
	// Different revs of the same repo are highlighted, never auto-collapsed.
	if len(r.Follows) != 0 {
		t.Errorf("want no follows recs for distinct revs, got %+v", r.Follows)
	}
}

// testdata/madder_with_duplicates.flake.lock is a frozen snapshot of
// amarbel-llc/madder@master's flake.lock (34 nodes, 9 duplicate-source
// identity groups, deeply nested input paths via tap+tommy). It is the
// realistic fixture for path-prefix shadow-pruning: an unpruned emitter
// would surface 19 lines across the 9 groups, while the shadow-pruned
// emitter produces exactly the 9 un-shadowed lines below — verified by
// applying them to madder's flake.nix and re-locking (34 → 15 nodes,
// no residual lint findings).
func TestAnalyzeMadderFlakeLock(t *testing.T) {
	b, err := os.ReadFile("testdata/madder_with_duplicates.flake.lock")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	l, err := flakelock.Parse(b)
	if err != nil {
		t.Fatalf("Parse fixture: %v", err)
	}
	r := Analyze(l)

	want := map[string]bool{
		`inputs.nixpkgs.inputs.nixpkgs-master.follows = "nixpkgs-master"`:     false,
		`inputs.utils.inputs.systems.follows = "nixpkgs/systems"`:             false,
		`inputs.tommy.inputs.bats.follows = "bats"`:                           false,
		`inputs.tommy.inputs.tap.follows = "tap"`:                             false,
		`inputs.tap.inputs.bats.follows = "bats"`:                             false,
		`inputs.tap.inputs.treefmt-nix.follows = "nixpkgs/treefmt-nix"`:       false,
		`inputs.tap.inputs.crane.follows = "purse-first/crane"`:               false,
		`inputs.tap.inputs.gomod2nix.follows = "purse-first/gomod2nix"`:       false,
		`inputs.tap.inputs.rust-overlay.follows = "purse-first/rust-overlay"`: false,
	}
	got := 0
	for _, rec := range r.Follows {
		for _, line := range rec.Lines {
			got++
			if _, ok := want[line]; ok {
				want[line] = true
			}
		}
	}
	if got != 9 {
		t.Errorf("got %d follows lines, want 9 (the un-shadowed minimum): %+v", got, r.Follows)
	}
	for line, seen := range want {
		if !seen {
			t.Errorf("missing expected follows line: %s", line)
		}
	}
}

// testdata/repo_with_duplicates.flake.lock is a frozen snapshot of this
// repo's flake.lock before the follows in flake.nix collapsed three
// duplicate-source node pairs (nixpkgs-master, systems, treefmt-nix).
// Exercises canonical-selection with multi-member groups and confirms
// NixOS/nixpkgs vs amarbel-llc/nixpkgs do not collide as multi-version
// inputs (distinct repos).
func TestAnalyzeRepoFlakeLock(t *testing.T) {
	b, err := os.ReadFile("testdata/repo_with_duplicates.flake.lock")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	l, err := flakelock.Parse(b)
	if err != nil {
		t.Fatalf("Parse fixture: %v", err)
	}
	r := Analyze(l)

	want := map[string]bool{
		`inputs.nixpkgs.inputs.nixpkgs-master.follows = "nixpkgs-master"`: false,
		`inputs.utils.inputs.systems.follows = "nixpkgs/systems"`:         false,
		`inputs.nixpkgs.inputs.treefmt-nix.follows = "treefmt-nix"`:       false,
	}
	got := 0
	for _, rec := range r.Follows {
		for _, line := range rec.Lines {
			got++
			if _, ok := want[line]; ok {
				want[line] = true
			}
		}
	}
	if got != 3 {
		t.Errorf("got %d follows lines, want 3: %+v", got, r.Follows)
	}
	for line, seen := range want {
		if !seen {
			t.Errorf("missing expected follows line: %s", line)
		}
	}
	if len(r.MultiVersion) != 0 {
		t.Errorf("want no multi-version inputs (NixOS/nixpkgs and amarbel-llc/nixpkgs are distinct repos), got %+v", r.MultiVersion)
	}
}
