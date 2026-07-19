package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
)

// transitivePipelineLock models root → tacky → bats, where bats declares only
// `igloo` (so a tacky override of bats's nixpkgs input is dead, but of bats's
// igloo input is live). igloo is a leaf (no inputs) and must not be fetched.
const transitivePipelineLock = `{
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

// tackyFlakeNix is the upstream flake.nix tacky would serve: it overrides two
// of bats's inputs, one dead (nixpkgs) and one live (igloo).
const tackyFlakeNix = `{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.igloo.follows = "igloo";
    };
  };
  outputs = { self }: { };
}
`

// TestTransitiveDeadOverridesPipeline exercises the full transitive pipeline —
// fetch (stubbed), nixedit.Overrides extraction, lint.TransitiveDeadOverrides
// resolution, upstream labeling, and leaf-skipping — without any network or
// nix. Only the dead override on a real upstream node is reported.
func TestTransitiveDeadOverridesPipeline(t *testing.T) {
	lock, err := flakelock.Parse([]byte(transitivePipelineLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var fetched []string
	fetch := func(lk *flakelock.Locked) ([]byte, bool) {
		fetched = append(fetched, lk.Repo)
		if lk.Owner == "amarbel-llc" && lk.Repo == "tacky" {
			return []byte(tackyFlakeNix), true
		}
		return nil, false // bats serves nothing; igloo is a leaf and never reaches here
	}

	got, stats := transitiveDeadOverridesWith(lock, fetch)
	if len(got) != 1 {
		t.Fatalf("want 1 transitive dead override, got %d: %+v", len(got), got)
	}
	// tacky and bats both have inputs (candidates); igloo is a leaf. Only
	// tacky's fetch succeeds.
	if stats.considered != 2 || stats.fetched != 1 {
		t.Errorf("stats = %+v, want considered=2 fetched=1", stats)
	}
	d := got[0]
	if d.Override != `inputs.bats.inputs.nixpkgs.follows` {
		t.Errorf("Override = %q, want inputs.bats.inputs.nixpkgs.follows", d.Override)
	}
	if d.Target != "bats" || d.Input != "nixpkgs" {
		t.Errorf("Target/Input = %q/%q, want bats/nixpkgs", d.Target, d.Input)
	}
	if d.Direct {
		t.Errorf("transitive override must have Direct=false: %+v", d)
	}
	if d.Via != "amarbel-llc/tacky" {
		t.Errorf("Via = %q, want amarbel-llc/tacky", d.Via)
	}

	// igloo is a leaf (no inputs) and must be skipped before fetching; only
	// tacky and bats (both have inputs) are candidates.
	for _, r := range fetched {
		if r == "igloo" {
			t.Errorf("leaf node igloo should not be fetched; fetched = %v", fetched)
		}
	}
}

// TestGithubRawFlakeNixFetch exercises the real HTTP fetch path against a
// local server (via the overridable base URL): a 200 returns the body, a
// non-200 yields ok=false, and the request path encodes owner/repo/rev.
func TestGithubRawFlakeNixFetch(t *testing.T) {
	const body = "{ inputs = { x.url = \"github:o/x\"; }; outputs = { self }: { }; }"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/amarbel-llc/tacky/ttt/flake.nix" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	prev := githubRawBaseURL
	githubRawBaseURL = srv.URL
	defer func() { githubRawBaseURL = prev }()

	got, ok := githubRawFlakeNix(context.Background(), "amarbel-llc", "tacky", "ttt")
	if !ok {
		t.Fatalf("githubRawFlakeNix: want ok for a served path")
	}
	if !strings.Contains(string(got), `x.url = "github:o/x"`) {
		t.Errorf("fetched body wrong: %q", got)
	}

	if _, ok := githubRawFlakeNix(context.Background(), "amarbel-llc", "tacky", "missing"); ok {
		t.Errorf("githubRawFlakeNix: want ok=false for a 404 path")
	}
}
