package lint

import "testing"

const conformantSHA = "567a49d1913ce81ac6e9582e3553dd90a955875f"

func TestCheckNixpkgsMasterConformant(t *testing.T) {
	// The exact convention shape yields no finding.
	if f := ClassifyNixpkgsMaster("github:NixOS/nixpkgs/"+conformantSHA, true); f != nil {
		t.Errorf("conformant url flagged: %+v", f)
	}
}

func TestCheckNixpkgsMasterMissing(t *testing.T) {
	f := ClassifyNixpkgsMaster("", false)
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
			f := ClassifyNixpkgsMaster(tc.url, true)
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
		f := ClassifyNixpkgsMaster(url, true)
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
		"567a49d",                                  // too short
		"567A49D1913CE81AC6E9582E3553DD90A955875F", // uppercase
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
