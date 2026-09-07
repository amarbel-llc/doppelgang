package lint

import "testing"

const conformantSHA = "567a49d1913ce81ac6e9582e3553dd90a955875f"

// otherSHA is a well-formed revision distinct from conformantSHA, standing
// in for the fleet's next nixpkgs pin.
const otherSHA = "f13ff45a67c1f4c1a5e2b4f8e0d3c9a7b6543210"

func TestCheckNixpkgsMasterConformant(t *testing.T) {
	// The exact convention shape yields no finding.
	if f := ClassifyNixpkgsMaster("github:NixOS/nixpkgs/"+conformantSHA, true, ""); f != nil {
		t.Errorf("conformant url flagged: %+v", f)
	}
	// A pin that already equals the target is likewise conformant — the
	// cascade must not rewrite a repo that is already up to date.
	if f := ClassifyNixpkgsMaster("github:NixOS/nixpkgs/"+conformantSHA, true, conformantSHA); f != nil {
		t.Errorf("pin matching the target flagged: %+v", f)
	}
}

func TestCheckNixpkgsMasterStale(t *testing.T) {
	// A well-formed pin to a revision other than the target is stale: this
	// is what lets eng's update-nix cascade advance an already-pinned repo
	// instead of no-op'ing on it.
	url := "github:NixOS/nixpkgs/" + conformantSHA
	f := ClassifyNixpkgsMaster(url, true, otherSHA)
	if f == nil || f.Status != NixpkgsMasterStale {
		t.Fatalf("want Stale finding, got %+v", f)
	}
	if f.URL != url {
		t.Errorf("finding url = %q, want %q", f.URL, url)
	}
	if want := "github:NixOS/nixpkgs/" + otherSHA; f.TargetURL != want {
		t.Errorf("finding targetURL = %q, want %q", f.TargetURL, want)
	}
	if got := f.Status.String(); got != "stale" {
		t.Errorf("Status.String() = %q, want %q", got, "stale")
	}
}

func TestCheckNixpkgsMasterNoTargetAcceptsAnyPin(t *testing.T) {
	// Backward compatibility: without a target sha the check is shape-only,
	// so a pin to any revision conforms. A plain `lint` must not start
	// failing repos merely for lagging the fleet revision.
	for _, sha := range []string{conformantSHA, otherSHA} {
		if f := ClassifyNixpkgsMaster("github:NixOS/nixpkgs/"+sha, true, ""); f != nil {
			t.Errorf("sha %s flagged with no target: %+v", sha, f)
		}
	}
}

func TestCheckNixpkgsMasterMissing(t *testing.T) {
	f := ClassifyNixpkgsMaster("", false, "")
	if f == nil || f.Status != NixpkgsMasterMissing {
		t.Fatalf("want Missing finding, got %+v", f)
	}
	if f.URL != "" {
		t.Errorf("missing finding should carry no url, got %q", f.URL)
	}
}

func TestCheckNixpkgsMasterFloating(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"no-rev", "github:NixOS/nixpkgs"},
		{"branch-name", "github:NixOS/nixpkgs/nixpkgs-unstable"},
		{"master-branch", "github:NixOS/nixpkgs/master"},
		{"short-rev", "github:NixOS/nixpkgs/567a49d"},
		{"uppercase-hex", "github:NixOS/nixpkgs/567A49D1913CE81AC6E9582E3553DD90A955875F"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A target sha is supplied to prove floating still wins over
			// stale: it is the more specific diagnostic, and the repair
			// rewrites the url to the target either way.
			f := ClassifyNixpkgsMaster(tc.url, true, otherSHA)
			if f == nil || f.Status != NixpkgsMasterFloating {
				t.Fatalf("url %q: want Floating finding, got %+v", tc.url, f)
			}
			if f.URL != tc.url {
				t.Errorf("finding url = %q, want %q", f.URL, tc.url)
			}
		})
	}
}

func TestCheckNixpkgsMasterNonGithub(t *testing.T) {
	cases := []string{
		"path:/nix/store/whatever",
		"git+https://github.com/NixOS/nixpkgs?rev=" + conformantSHA,
		"github:someone-else/nixpkgs/" + conformantSHA,
		// A different repo whose name merely starts with "nixpkgs" must be
		// non-github, not misclassified as a floating NixOS/nixpkgs ref.
		"github:NixOS/nixpkgs-unstable/" + conformantSHA,
	}
	for _, url := range cases {
		// As with floating, non-github outranks stale under a target.
		f := ClassifyNixpkgsMaster(url, true, otherSHA)
		if f == nil || f.Status != NixpkgsMasterNonGithub {
			t.Errorf("url %q: want NonGithub finding, got %+v", url, f)
		}
	}
}

func TestValidNixpkgsSHA(t *testing.T) {
	good := conformantSHA
	if !ValidNixpkgsSHA(good) {
		t.Errorf("ValidNixpkgsSHA(%q) = false, want true", good)
	}
	for _, bad := range []string{
		"",
		"567a49d", // too short
		"567A49D1913CE81AC6E9582E3553DD90A955875F",  // uppercase
		"567a49d1913ce81ac6e9582e3553dd90a955875f0", // 41 chars
		"github:NixOS/nixpkgs/" + conformantSHA,     // full url, not a bare sha
	} {
		if ValidNixpkgsSHA(bad) {
			t.Errorf("ValidNixpkgsSHA(%q) = true, want false", bad)
		}
	}
}

func TestNixpkgsMasterURL(t *testing.T) {
	got := NixpkgsMasterURL(conformantSHA)
	want := "github:NixOS/nixpkgs/" + conformantSHA
	if got != want {
		t.Errorf("NixpkgsMasterURL = %q, want %q", got, want)
	}
}
