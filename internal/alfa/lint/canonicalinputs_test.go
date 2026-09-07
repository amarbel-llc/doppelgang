package lint

import (
	"strings"
	"testing"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
	"code.linenisgreat.com/doppelgang/internal/0/nixedit"
)

// Two distinct well-formed 40-hex revisions, so a test can tell a preserved
// pin apart from one silently swapped for the other.
const (
	revA = "2c8ca7354b2bba467b0602277f5d621d7d688dd4"
	revB = "9f1e2b3c4d5e6f70819a2b3c4d5e6f708192a3b4"
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
	findings, pins := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 0 {
		t.Errorf("conformant input flagged: %+v", findings)
	}
	if len(pins) != 0 {
		t.Errorf("unpinned conformant input reported as a pin: %+v", pins)
	}
}

func TestCanonicalInputsNonCanonical(t *testing.T) {
	lock := miniLock("igloo", "github", "amarbel-llc", "igloo")
	repoURLs := map[string]string{
		"igloo": "git+https://code.linenisgreat.com/igloo.git",
	}
	src := flakeNixWith("igloo", "github:amarbel-llc/igloo")
	findings, _ := CanonicalInputs(lock, src, repoURLs)
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
	findings, pins := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 0 || len(pins) != 0 {
		t.Errorf("input not in PAPI map should be skipped, got %+v / %+v", findings, pins)
	}
}

func TestCanonicalInputsEmptyPAPIMap(t *testing.T) {
	lock := miniLock("igloo", "github", "amarbel-llc", "igloo")
	src := flakeNixWith("igloo", "github:amarbel-llc/igloo")
	findings, pins := CanonicalInputs(lock, src, nil)
	if findings != nil || pins != nil {
		t.Errorf("empty PAPI map should return nil, got %+v / %+v", findings, pins)
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
	findings, _ := CanonicalInputs(lock, src, repoURLs)
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

func TestClassifyCanonicalInput(t *testing.T) {
	const (
		tarball  = "https://code.linenisgreat.com/igloo/archive/master.tar.gz"
		gitHTTPS = "git+https://code.linenisgreat.com/igloo.git"
	)
	const github = "github:linenisgreat/igloo"
	// wantTargetURL is the URL the repair would write for a finding; wantPinRev
	// is the revision of an expected pin. Exactly one is set per case, and
	// neither when the input is already exactly canonical.
	for _, tc := range []struct {
		name          string
		current       string
		canonical     string
		wantTargetURL string
		wantPinRev    string
	}{{
		name:      "tarball canonical master conforms exactly",
		current:   tarball,
		canonical: tarball,
	}, {
		// The #36 regression: a deliberate pin on the canonical host must not
		// be re-floated to master.
		name:       "tarball canonical host revision pin is conformant",
		current:    "https://code.linenisgreat.com/igloo/archive/" + revA + ".tar.gz",
		canonical:  tarball,
		wantPinRev: revA,
	}, {
		name:          "non-canonical host without a pin floats to canonical master",
		current:       "github:amarbel-llc/igloo",
		canonical:     tarball,
		wantTargetURL: tarball,
	}, {
		name:          "non-canonical host with a pin keeps the revision",
		current:       "github:amarbel-llc/igloo/" + revB,
		canonical:     tarball,
		wantTargetURL: "https://code.linenisgreat.com/igloo/archive/" + revB + ".tar.gz",
	}, {
		// A branch or tag in the ref position is a float, not a deliberate
		// revision, so it stays actionable and floats to the canonical ref.
		name:          "tarball canonical host branch ref is not a pin",
		current:       "https://code.linenisgreat.com/igloo/archive/some-branch.tar.gz",
		canonical:     tarball,
		wantTargetURL: tarball,
	}, {
		name:          "short revs are not pins",
		current:       "github:amarbel-llc/igloo/abc123",
		canonical:     tarball,
		wantTargetURL: tarball,
	}, {
		name:      "git+https canonical conforms exactly",
		current:   gitHTTPS,
		canonical: gitHTTPS,
	}, {
		name:       "git+https canonical host rev query is conformant",
		current:    gitHTTPS + "?rev=" + revA,
		canonical:  gitHTTPS,
		wantPinRev: revA,
	}, {
		name:          "git+https target carries a foreign host's pin",
		current:       "github:amarbel-llc/igloo/" + revB,
		canonical:     gitHTTPS,
		wantTargetURL: gitHTTPS + "?rev=" + revB,
	}, {
		// Every shape inputRev reads a revision out of must also be writable
		// by canonicalPinTarget, or the pin is dropped on repair.
		name:       "github canonical host rev segment is conformant",
		current:    github + "/" + revA,
		canonical:  github,
		wantPinRev: revA,
	}, {
		name:          "github canonical target carries a foreign host's pin",
		current:       "https://example.com/igloo/archive/" + revB + ".tar.gz",
		canonical:     github,
		wantTargetURL: github + "/" + revB,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			finding, pin := ClassifyCanonicalInput("igloo", tc.current, tc.canonical)
			switch {
			case tc.wantTargetURL != "":
				if pin != nil {
					t.Fatalf("want a finding, got pin %+v", *pin)
				}
				if finding == nil {
					t.Fatalf("want a finding targeting %q, got none", tc.wantTargetURL)
				}
				if finding.CanonicalURL != tc.wantTargetURL {
					t.Errorf("CanonicalURL = %q, want %q", finding.CanonicalURL, tc.wantTargetURL)
				}
				if finding.CurrentURL != tc.current {
					t.Errorf("CurrentURL = %q, want %q", finding.CurrentURL, tc.current)
				}
			case tc.wantPinRev != "":
				if finding != nil {
					t.Fatalf("pin reported as an actionable finding: %+v", *finding)
				}
				if pin == nil {
					t.Fatalf("want a pin at %s, got none", tc.wantPinRev)
				}
				if pin.Rev != tc.wantPinRev {
					t.Errorf("pin.Rev = %q, want %q", pin.Rev, tc.wantPinRev)
				}
				if pin.URL != tc.current {
					t.Errorf("pin.URL = %q, want %q", pin.URL, tc.current)
				}
			default:
				if finding != nil || pin != nil {
					t.Fatalf("exactly canonical input classified: %+v / %+v", finding, pin)
				}
			}
		})
	}
}

// TestCanonicalInputsFixPreservesPins drives the exact composition `lint --fix`
// performs — nixedit.SetInputURL over report.CanonicalInputs — and asserts the
// resulting flake.nix: the canonical-host pin is byte-identical, and the
// foreign-host pin has moved forge while keeping its revision (#36).
func TestCanonicalInputsFixPreservesPins(t *testing.T) {
	const pinnedURL = "https://code.linenisgreat.com/igloo/archive/" + revA + ".tar.gz"
	lock := &flakelock.Lock{
		Root:    "root",
		Version: 7,
		Nodes: map[string]flakelock.Node{
			"root": {
				Inputs: map[string]flakelock.InputRef{
					"igloo": {Node: "node_igloo"},
					"utils": {Node: "node_utils"},
				},
			},
			"node_igloo": {
				Locked:   &flakelock.Locked{Type: "tarball", Rev: revA},
				Original: &flakelock.Original{Type: "tarball"},
			},
			"node_utils": {
				Locked:   &flakelock.Locked{Type: "github", Owner: "amarbel-llc", Repo: "utils", Rev: revB},
				Original: &flakelock.Original{Type: "github", Owner: "amarbel-llc", Repo: "utils"},
			},
		},
	}
	repoURLs := map[string]string{
		"igloo": "https://code.linenisgreat.com/igloo/archive/master.tar.gz",
		"utils": "https://code.linenisgreat.com/utils/archive/master.tar.gz",
	}
	src := []byte(`{
  inputs = {
    igloo.url = "` + pinnedURL + `";
    utils.url = "github:amarbel-llc/utils/` + revB + `";
  };
  outputs = { self, igloo, utils }: { };
}
`)

	findings, pins := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 1 || findings[0].Input != "utils" {
		t.Fatalf("want only utils actionable, got %+v", findings)
	}
	if len(pins) != 1 || pins[0].Input != "igloo" || pins[0].Rev != revA {
		t.Fatalf("want igloo reported as a pin at %s, got %+v", revA, pins)
	}

	out := src
	for _, f := range findings {
		var err error
		out, _, err = nixedit.SetInputURL(out, f.Input, f.CanonicalURL)
		if err != nil {
			t.Fatalf("SetInputURL(%s): %v", f.Input, err)
		}
	}

	if !strings.Contains(string(out), `igloo.url = "`+pinnedURL+`";`) {
		t.Errorf("canonical-host pin was rewritten; flake.nix is now:\n%s", out)
	}
	wantUtils := `utils.url = "https://code.linenisgreat.com/utils/archive/` + revB + `.tar.gz";`
	if !strings.Contains(string(out), wantUtils) {
		t.Errorf("want %s in rewritten flake.nix, got:\n%s", wantUtils, out)
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
	findings, _ := CanonicalInputs(lock, src, repoURLs)
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(findings), findings)
	}
	if findings[0].Input != "apple" || findings[1].Input != "zebra" {
		t.Errorf("findings not sorted: %v, %v", findings[0].Input, findings[1].Input)
	}
}
