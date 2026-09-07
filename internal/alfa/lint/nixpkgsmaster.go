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
	// NixpkgsMasterStale: the input is pinned to a well-formed 40-hex
	// revision, but not to the caller-supplied target revision. Only
	// reachable when a target sha is supplied; without one, any valid pin is
	// conformant.
	NixpkgsMasterStale
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
	case NixpkgsMasterStale:
		return "stale"
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
	// TargetURL is the pinned url the caller asked for, set only when
	// Status is NixpkgsMasterStale — the one failure mode whose diagnostic
	// needs both the current and the wanted revision to be legible.
	TargetURL string
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
// targetSHA is the revision the fleet wants pinned. When it is non-empty, a
// well-formed pin to a *different* revision is Stale: this is what lets
// eng's update-nix cascade move an already-pinned repo forward, rather than
// accepting any 40-hex pin as conformant and no-op'ing. When targetSHA is
// empty the caller has named no target, so any well-formed pin conforms —
// the shape-only check a plain `lint` performs.
//
// The other three failure modes match the issue's original contract: input
// missing entirely, a floating (non-40-hex) github:NixOS/nixpkgs ref, and a
// non-github shape. A floating or non-github url is reported as such even
// under a target: that diagnostic is more specific than "stale", and the
// repair rewrites it to the target either way.
func ClassifyNixpkgsMaster(url string, present bool, targetSHA string) *NixpkgsMasterFinding {
	if !present {
		return &NixpkgsMasterFinding{Status: NixpkgsMasterMissing}
	}
	if nixpkgsMasterPinnedRE.MatchString(url) {
		if targetSHA == "" {
			return nil
		}
		targetURL := NixpkgsMasterURL(targetSHA)
		if url == targetURL {
			return nil
		}
		return &NixpkgsMasterFinding{Status: NixpkgsMasterStale, URL: url, TargetURL: targetURL}
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
