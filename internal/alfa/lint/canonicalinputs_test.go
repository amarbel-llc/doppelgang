package lint

import (
	"testing"

	"github.com/friedenberg/doppelgang/internal/0/flakelock"
)

// miniLock builds a minimal lock with a single root input for testing.
func miniLock(inputName, lockType, owner, repo string) *flakelock.Lock {
	return &flakelock.Lock{
		Root:    "root",
		Version: 7,
		Nodes: map[string]flakelock.Node{
			"root": {
				Inputs: map[string]flakelock.InputRef{
					inputName: {Node: "node_" + inputName},
				},
			},
			"node_" + inputName: {
				Locked: &flakelock.Locked{
					Type:  lockType,
					Owner: owner,
					Repo:  repo,
					Rev:   "abc123",
				},
				Original: &flakelock.Original{
					Type:  lockType,
					Owner: owner,
					Repo:  repo,
				},
			},
		},
	}
}

// flakeNixWith builds a minimal flake.nix src with the given input URL.
func flakeNixWith(inputName, url string) []byte {
	return []byte(`{
  inputs = {
    ` + inputName + `.url = "` + url + `";
  };
  outputs = { self, ` + inputName + ` }: { };
}
`)
}

func TestCanonicalInputsConformant(t *testing.T) {
	lock := miniLock("igloo", "git", "linenisgreat", "igloo")
	repoURLs := map[string]string{
		"igloo": "git+https://code.linenisgreat.com/igloo.git",
	}
	src := flakeNixWith("igloo", "git+https://code.linenisgreat.com/igloo.git")
	findings := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 0 {
		t.Errorf("conformant input flagged: %+v", findings)
	}
}

func TestCanonicalInputsNonCanonical(t *testing.T) {
	lock := miniLock("igloo", "github", "amarbel-llc", "igloo")
	repoURLs := map[string]string{
		"igloo": "git+https://code.linenisgreat.com/igloo.git",
	}
	src := flakeNixWith("igloo", "github:amarbel-llc/igloo")
	findings := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Input != "igloo" {
		t.Errorf("finding.Input = %q, want %q", f.Input, "igloo")
	}
	if f.CurrentURL != "github:amarbel-llc/igloo" {
		t.Errorf("finding.CurrentURL = %q, want %q", f.CurrentURL, "github:amarbel-llc/igloo")
	}
	if f.CanonicalURL != "git+https://code.linenisgreat.com/igloo.git" {
		t.Errorf("finding.CanonicalURL = %q, want %q", f.CanonicalURL, "git+https://code.linenisgreat.com/igloo.git")
	}
}

func TestCanonicalInputsSkipsNotInPAPI(t *testing.T) {
	// nixpkgs is not in the PAPI map (it's a NixOS repo, not the operator's).
	lock := miniLock("nixpkgs", "github", "NixOS", "nixpkgs")
	repoURLs := map[string]string{
		"igloo": "git+https://code.linenisgreat.com/igloo.git",
	}
	src := flakeNixWith("nixpkgs", "github:NixOS/nixpkgs/abc123")
	findings := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 0 {
		t.Errorf("input not in PAPI map should be skipped, got %+v", findings)
	}
}

func TestCanonicalInputsEmptyPAPIMap(t *testing.T) {
	lock := miniLock("igloo", "github", "amarbel-llc", "igloo")
	src := flakeNixWith("igloo", "github:amarbel-llc/igloo")
	findings := CanonicalInputs(lock, src, nil)
	if findings != nil {
		t.Errorf("empty PAPI map should return nil, got %+v", findings)
	}
}

func TestCanonicalInputsSkipsFollowsInputs(t *testing.T) {
	// A follows-resolved input (ref.Node == "") should be skipped.
	lock := &flakelock.Lock{
		Root:    "root",
		Version: 7,
		Nodes: map[string]flakelock.Node{
			"root": {
				Inputs: map[string]flakelock.InputRef{
					"igloo": {Node: "node_igloo"},
					// follows-resolved: points to an array, not a node key
					"igloo/utils": {Follows: []string{"utils"}},
				},
			},
			"node_igloo": {
				Locked:   &flakelock.Locked{Type: "git", Rev: "abc"},
				Original: &flakelock.Original{Type: "git"},
			},
		},
	}
	repoURLs := map[string]string{
		"igloo": "git+https://code.linenisgreat.com/igloo.git",
		"utils": "git+https://code.linenisgreat.com/utils.git",
	}
	src := []byte(`{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self, igloo }: { };
}
`)
	findings := CanonicalInputs(lock, src, repoURLs)
	// Only igloo should be found; igloo/utils is a follows and skipped.
	if len(findings) != 1 || findings[0].Input != "igloo" {
		t.Errorf("want only igloo finding, got %+v", findings)
	}
}

func TestCanonicalNixURL(t *testing.T) {
	got := CanonicalNixURL("https://code.linenisgreat.com/igloo")
	want := "git+https://code.linenisgreat.com/igloo.git"
	if got != want {
		t.Errorf("CanonicalNixURL = %q, want %q", got, want)
	}
}

func TestNixURLFlakeURLPresent(t *testing.T) {
	got := NixURL("https://code.linenisgreat.com/igloo", "https://code.linenisgreat.com/igloo/archive/master.tar.gz")
	want := "https://code.linenisgreat.com/igloo/archive/master.tar.gz"
	if got != want {
		t.Errorf("NixURL (flake_url present) = %q, want %q", got, want)
	}
}

func TestNixURLFlakeURLAbsent(t *testing.T) {
	got := NixURL("https://code.linenisgreat.com/igloo", "")
	want := "git+https://code.linenisgreat.com/igloo.git"
	if got != want {
		t.Errorf("NixURL (flake_url absent) = %q, want %q", got, want)
	}
}

func TestCanonicalInputsSorted(t *testing.T) {
	// Multiple findings should be returned sorted by input name.
	lock := &flakelock.Lock{
		Root:    "root",
		Version: 7,
		Nodes: map[string]flakelock.Node{
			"root": {
				Inputs: map[string]flakelock.InputRef{
					"zebra": {Node: "node_zebra"},
					"apple": {Node: "node_apple"},
				},
			},
			"node_zebra": {
				Locked:   &flakelock.Locked{Type: "github", Owner: "o", Repo: "zebra", Rev: "abc"},
				Original: &flakelock.Original{Type: "github", Owner: "o", Repo: "zebra"},
			},
			"node_apple": {
				Locked:   &flakelock.Locked{Type: "github", Owner: "o", Repo: "apple", Rev: "abc"},
				Original: &flakelock.Original{Type: "github", Owner: "o", Repo: "apple"},
			},
		},
	}
	repoURLs := map[string]string{
		"zebra": "git+https://example.com/zebra.git",
		"apple": "git+https://example.com/apple.git",
	}
	src := []byte(`{
  inputs = {
    zebra.url = "github:o/zebra";
    apple.url = "github:o/apple";
  };
  outputs = { self, zebra, apple }: { };
}
`)
	findings := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(findings), findings)
	}
	if findings[0].Input != "apple" || findings[1].Input != "zebra" {
		t.Errorf("findings not sorted: %v, %v", findings[0].Input, findings[1].Input)
	}
}
