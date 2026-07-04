package lint

import (
	"regexp"
	"strings"
)

// NixpkgsMasterStatus is why a flake's top-level `nixpkgs-master` input
// fails the SHA-pinned convention. There is no "conformant" value: a
// conformant flake yields a nil *NixpkgsMasterFinding rather than a status.
type NixpkgsMasterStatus int

const (
	// NixpkgsMasterMissing: the flake declares no `nixpkgs-master.url`
	// input at all.
	NixpkgsMasterMissing NixpkgsMasterStatus = iota
	// NixpkgsMasterFloating: the input is a github:NixOS/nixpkgs ref but is
	// not pinned to a full 40-hex revision (no rev, or a branch/tag name,
	// or a short rev).
	NixpkgsMasterFloating
	// NixpkgsMasterNonGithub: the input's url is not a github:NixOS/nixpkgs
	// ref at all (e.g. a path:, git+https:, or a different owner/repo).
	NixpkgsMasterNonGithub
)

// String renders the status as the token used in diagnostics and the
// machine-readable formats.
func (s NixpkgsMasterStatus) String() string {
	switch s {
	case NixpkgsMasterMissing:
		return "missing"
	case NixpkgsMasterFloating:
		return "floating"
	case NixpkgsMasterNonGithub:
		return "non-github"
	default:
		return "unknown"
	}
}

// NixpkgsMasterFinding reports that the flake's top-level `nixpkgs-master`
// input does not conform to the convention
//
//	nixpkgs-master.url = "github:NixOS/nixpkgs/<40-hex sha>";
//
// which eng's update-nix cascade requires of every member repo. A
// conformant flake produces no finding (a nil *NixpkgsMasterFinding).
type NixpkgsMasterFinding struct {
	// Status is why the input is non-conformant.
	Status NixpkgsMasterStatus
	// URL is the current url of the input, for the diagnostic. It is empty
	// when Status is NixpkgsMasterMissing (there is no url to report).
	URL string
}

const nixpkgsMasterGithubPrefix = "github:NixOS/nixpkgs"

var (
	// nixpkgsMasterPinnedRE is the exact conforming shape: the fleet
	// convention pins nixpkgs-master to a full 40-char lowercase-hex
	// revision of NixOS/nixpkgs.
	nixpkgsMasterPinnedRE = regexp.MustCompile(`^github:NixOS/nixpkgs/[0-9a-f]{40}$`)
	// nixpkgsSHARE validates a bare 40-char lowercase-hex git revision, the
	// shape the --nixpkgs-master-sha repair parameter must take.
	nixpkgsSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ClassifyNixpkgsMaster classifies the url of a flake's top-level
// `nixpkgs-master` input against the convention and returns a finding when
// it does not conform, or nil when it does. present reports whether the
// input declares a `.url` at all (false ⇒ missing); url is that url when
// present.
//
// The three failure modes match the issue's contract: input missing
// entirely, a floating (non-40-hex) github:NixOS/nixpkgs ref, and a
// non-github shape.
func ClassifyNixpkgsMaster(url string, present bool) *NixpkgsMasterFinding {
	if !present {
		return &NixpkgsMasterFinding{Status: NixpkgsMasterMissing}
	}
	if nixpkgsMasterPinnedRE.MatchString(url) {
		return nil
	}
	// A github:NixOS/nixpkgs ref that is not the full 40-hex pin is floating:
	// the bare ref, a /ref (branch/tag/short rev), or a ?query form. Match the
	// prefix only at a ref boundary so a different repo whose name merely
	// starts with "nixpkgs" (e.g. github:NixOS/nixpkgs-unstable) is classed
	// non-github, not floating.
	if url == nixpkgsMasterGithubPrefix ||
		strings.HasPrefix(url, nixpkgsMasterGithubPrefix+"/") ||
		strings.HasPrefix(url, nixpkgsMasterGithubPrefix+"?") {
		return &NixpkgsMasterFinding{Status: NixpkgsMasterFloating, URL: url}
	}
	return &NixpkgsMasterFinding{Status: NixpkgsMasterNonGithub, URL: url}
}

// ValidNixpkgsSHA reports whether s is a 40-char lowercase-hex git revision,
// the required shape of the repair's target sha.
func ValidNixpkgsSHA(s string) bool { return nixpkgsSHARE.MatchString(s) }

// NixpkgsMasterURL builds the conventional pinned url for a revision, e.g.
// "github:NixOS/nixpkgs/<sha>". It is the value the repair writes.
func NixpkgsMasterURL(sha string) string {
	return nixpkgsMasterGithubPrefix + "/" + sha
}
