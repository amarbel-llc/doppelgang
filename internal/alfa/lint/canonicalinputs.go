package lint

import (
	"regexp"
	"sort"
	"strings"

	"code.linenisgreat.com/doppelgang/internal/0/flakelock"
	"code.linenisgreat.com/doppelgang/internal/0/nixedit"
)

// CanonicalInputFinding reports that a top-level flake input's URL does not
// resolve to the PAPI-published canonical nix URL for that repository. The
// canonical URL is either the verbatim flake_url from the PAPI entry (papi#56
// tarball form, when present) or the git+https form derived from the web URL
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
	// repo — the value the repair writes. When CurrentURL pins a revision,
	// this carries that revision into the canonical shape, so migrating forges
	// preserves the pin instead of floating the input to the canonical ref.
	CanonicalURL string
}

// CanonicalInputPin reports a top-level input that already resolves to its
// PAPI-canonical host and repo path but pins a 40-hex revision in place of the
// canonical floating ref. The check governs which forge an input is fetched
// from, not which revision, so a pin is conformant (#36).
//
// Pins are carried separately from CanonicalInputFinding rather than tagged
// with a status inside it, so that no consumer can fail the check — or rewrite
// the URL — on a deliberate pin. This keeps the package's convention that a
// finding always means "actionable"; see nixpkgsmaster.go.
type CanonicalInputPin struct {
	// Input is the top-level input name in flake.nix (e.g. "igloo").
	Input string
	// URL is the pinned canonical-host URL, exactly as flake.nix binds it.
	URL string
	// Rev is the 40-hex revision URL pins.
	Rev string
}

// tarballArchiveRE splits a forge tarball flake URL
// "https://host/path/archive/<ref>.tar.gz" into its "…/archive/" prefix and
// its <ref>.
var tarballArchiveRE = regexp.MustCompile(`^(.*/archive/)([^/]+)\.tar\.gz$`)

// inputRev returns the 40-hex revision an input URL pins, or "" when it pins
// none. It recognises the three shapes the fleet's flakes actually write: a
// `rev=` query parameter, the forge tarball archive path
// (…/archive/<rev>.tar.gz), and the github: shorthand's third path segment
// (github:owner/repo/<rev>).
//
// Only a full 40-hex revision counts. A branch or tag name in any of those
// positions is a float, not a deliberate revision, and stays actionable.
func inputRev(rawURL string) string {
	base, query, _ := strings.Cut(rawURL, "?")
	for _, kv := range strings.Split(query, "&") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "rev" && sha40RE.MatchString(v) {
			return v
		}
	}
	if m := tarballArchiveRE.FindStringSubmatch(base); m != nil && sha40RE.MatchString(m[2]) {
		return m[2]
	}
	if strings.HasPrefix(base, "github:") {
		if parts := strings.Split(base, "/"); len(parts) == 3 && sha40RE.MatchString(parts[2]) {
			return parts[2]
		}
	}
	return ""
}

// canonicalPinTarget expresses a canonical URL pinned to rev, and reports
// whether the canonical URL has a shape a pin can be written into: the tarball
// form takes the revision in place of its archive ref, the git+https form
// takes it as a `?rev=` parameter, and the github: shorthand takes it as a
// third path segment.
//
// Every shape inputRev can read a revision out of must be writable here too.
// Otherwise an input pinned in a shape this function does not cover would be
// classed non-canonical and repaired to the floating canonical URL, dropping
// the revision — the #36 bug, in a different shape.
func canonicalPinTarget(canonicalURL, rev string) (string, bool) {
	if m := tarballArchiveRE.FindStringSubmatch(canonicalURL); m != nil {
		return m[1] + rev + ".tar.gz", true
	}
	if strings.Contains(canonicalURL, "?") {
		return "", false // already parameterised; do not guess how to add a rev
	}
	if strings.HasPrefix(canonicalURL, "git+") {
		return canonicalURL + "?rev=" + rev, true
	}
	// A bare github:owner/repo takes the rev as its third segment. A shorthand
	// that already carries a ref is left alone: appending would be malformed.
	if strings.HasPrefix(canonicalURL, "github:") && strings.Count(canonicalURL, "/") == 1 {
		return canonicalURL + "/" + rev, true
	}
	return "", false
}

// ClassifyCanonicalInput compares one input's current URL against its
// PAPI-canonical URL and returns at most one of: a finding, when the URL does
// not resolve to the canonical forge; or a pin, when it does but names a
// revision rather than the canonical floating ref. Both are nil when the URL
// is already exactly canonical.
//
// A pin conforms — see CanonicalInputPin. A finding whose URL pins a revision
// carries that revision into its CanonicalURL, so the repair migrates the
// forge without silently floating the input.
func ClassifyCanonicalInput(input, currentURL, canonicalURL string) (*CanonicalInputFinding, *CanonicalInputPin) {
	if currentURL == canonicalURL {
		return nil, nil
	}
	finding := &CanonicalInputFinding{
		Input:        input,
		CurrentURL:   currentURL,
		CanonicalURL: canonicalURL,
	}
	rev := inputRev(currentURL)
	if rev == "" {
		return finding, nil
	}
	pinned, ok := canonicalPinTarget(canonicalURL, rev)
	if !ok {
		return finding, nil
	}
	if currentURL == pinned {
		return nil, &CanonicalInputPin{Input: input, URL: currentURL, Rev: rev}
	}
	finding.CanonicalURL = pinned
	return finding, nil
}

// CanonicalInputs checks each top-level root input in the lock against the
// PAPI repo-URL map, returning the inputs whose flake.nix URL does not resolve
// to the canonical forge and, separately, those that resolve to it but pin a
// revision. repoURLs maps repo name to canonical nix URL (built by the caller
// from `papi repos <domain>` JSON output). src is the content of flake.nix.
// When repoURLs is empty or src is nil, both results are nil (offline /
// unconfigured degrade). Each result is sorted by input name.
//
// Only inputs that resolve to an actual lock node (not a follows-resolved
// alias) are considered. Inputs not present in repoURLs are silently skipped.
// Inputs whose current URL binding is absent or not a plain quoted string are
// skipped (safe conservative outcome, matching nixedit's behaviour for
// unparseable values).
func CanonicalInputs(l *flakelock.Lock, src []byte, repoURLs map[string]string) ([]CanonicalInputFinding, []CanonicalInputPin) {
	if len(repoURLs) == 0 || len(src) == 0 {
		return nil, nil
	}
	root, ok := l.Nodes[l.Root]
	if !ok {
		return nil, nil
	}
	var findings []CanonicalInputFinding
	var pins []CanonicalInputPin
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
		finding, pin := ClassifyCanonicalInput(inputName, currentURL, canonicalURL)
		if finding != nil {
			findings = append(findings, *finding)
		}
		if pin != nil {
			pins = append(pins, *pin)
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Input < findings[j].Input
	})
	sort.Slice(pins, func(i, j int) bool {
		return pins[i].Input < pins[j].Input
	})
	return findings, pins
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
