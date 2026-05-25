package lint

import (
	"os"
	"testing"

	"github.com/friedenberg/doppelgang/internal/0/flakelock"
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

// The repo's own flake.lock is a realistic fixture: it carries three
// duplicate-source node pairs (nixpkgs-master, systems, treefmt-nix) that
// follows would collapse, and no same-repo-different-rev inputs.
func TestAnalyzeRepoFlakeLock(t *testing.T) {
	b, err := os.ReadFile("../../../flake.lock")
	if err != nil {
		t.Skipf("repo flake.lock unavailable: %v", err)
	}
	l, err := flakelock.Parse(b)
	if err != nil {
		t.Fatalf("Parse repo flake.lock: %v", err)
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
