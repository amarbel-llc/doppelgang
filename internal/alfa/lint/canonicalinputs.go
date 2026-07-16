package lint

import (
	"sort"

	"github.com/friedenberg/doppelgang/internal/0/flakelock"
	"github.com/friedenberg/doppelgang/internal/0/nixedit"
)

// CanonicalInputFinding reports that a top-level flake input's URL does not
// match the PAPI-published canonical nix URL for that repository. The canonical
// URL is either the verbatim flake_url from the PAPI entry (papi#56 tarball
// form, when present) or the git+https form derived from the web URL
// (git+<url>.git) when flake_url is absent. A conformant input — or one not
// published by the PAPI domain — produces no finding.
type CanonicalInputFinding struct {
	// Input is the top-level input name in flake.nix (e.g. "igloo").
	Input string
	// CurrentURL is the current .url binding from flake.nix, e.g.
	// "github:amarbel-llc/igloo". Empty when the binding is absent or not a
	// plain quoted string (both are skipped; this field is informational only).
	CurrentURL string
	// CanonicalURL is the PAPI-authoritative canonical nix flake URL for this
	// repo. It is either the verbatim flake_url from the PAPI entry (tarball
	// form, e.g. "https://code.linenisgreat.com/igloo/archive/master.tar.gz")
	// or the git+https form derived from the web URL
	// (e.g. "git+https://code.linenisgreat.com/igloo.git"). This is the value
	// the repair writes.
	CanonicalURL string
}

// CanonicalInputs checks each top-level root input in the lock against the
// PAPI repo-URL map and returns findings for inputs whose flake.nix URL does
// not match the canonical form. repoURLs maps repo name to canonical nix URL
// (built by the caller from `papi repos <domain>` JSON output). src is the
// content of flake.nix. When repoURLs is empty or src is nil, the function
// returns nil (offline / unconfigured degrade).
//
// Only inputs that resolve to an actual lock node (not a follows-resolved
// alias) are considered. Inputs not present in repoURLs are silently skipped.
// Inputs whose current URL binding is absent or not a plain quoted string are
// skipped (safe conservative outcome, matching nixedit's behaviour for
// unparseable values).
func CanonicalInputs(l *flakelock.Lock, src []byte, repoURLs map[string]string) []CanonicalInputFinding {
	if len(repoURLs) == 0 || len(src) == 0 {
		return nil
	}
	root, ok := l.Nodes[l.Root]
	if !ok {
		return nil
	}
	var findings []CanonicalInputFinding
	for inputName, ref := range root.Inputs {
		if ref.Node == "" {
			continue // follows-resolved alias; not a direct input URL
		}
		canonicalURL, ok := repoURLs[inputName]
		if !ok {
			continue // not published by the PAPI domain; skip
		}
		currentURL, present, err := nixedit.InputURL(src, inputName)
		if err != nil || !present {
			continue // unparseable or absent URL binding; skip
		}
		if currentURL == canonicalURL {
			continue // already canonical
		}
		findings = append(findings, CanonicalInputFinding{
			Input:        inputName,
			CurrentURL:   currentURL,
			CanonicalURL: canonicalURL,
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Input < findings[j].Input
	})
	return findings
}

// NixURL returns the canonical nix flake URL for a PAPI entry using the
// two-tier resolution introduced by papi#56: flakeURL verbatim when non-empty
// (tarball form), otherwise the git+https form derived from webURL. This is
// the authoritative resolution so all callers agree on the precedence.
func NixURL(webURL, flakeURL string) string {
	if flakeURL != "" {
		return flakeURL
	}
	return CanonicalNixURL(webURL)
}

// CanonicalNixURL converts a PAPI repo web URL (e.g.
// "https://code.linenisgreat.com/igloo") to the git+https nix flake input URL
// ("git+https://code.linenisgreat.com/igloo.git"). Use NixURL when a
// flake_url field may also be present.
func CanonicalNixURL(papiWebURL string) string {
	return "git+" + papiWebURL + ".git"
}
