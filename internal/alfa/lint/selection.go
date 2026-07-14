package lint

import (
	"fmt"
	"sort"
	"strings"
)

// Check identifies one of lint's analyses. The string value is the
// name accepted by `doppelgang lint --checks`.
type Check string

const (
	CheckFollows         Check = "follows"
	CheckMultiVersion    Check = "multi-version"
	CheckDeadOverrides   Check = "dead-overrides"
	CheckNixpkgsMaster   Check = "nixpkgs-master"
	CheckCanonicalInputs Check = "canonical-inputs"
)

// AllChecks is the canonical order of every check. Renderers iterate this
// and filter by the active Selection so output order is stable regardless
// of which subset is selected.
var AllChecks = []Check{CheckFollows, CheckMultiVersion, CheckDeadOverrides, CheckNixpkgsMaster, CheckCanonicalInputs}

// DefaultChecks is the subset selected when `--checks` is absent — the three
// flake.lock/flake.nix analyses that need no external parameter. The
// nixpkgs-master and canonical-inputs checks are deliberately excluded from
// the default: they encode fleet policy rather than universal
// reducible-duplication findings, and canonical-inputs additionally requires a
// network call (papi) and a --papi-domain parameter. Both are opt-in via
// `--checks nixpkgs-master` / `--checks canonical-inputs` (or the `all`
// alias). Keeping the default at three also preserves the pre-existing
// exit-code, output, and NDJSON plan-count behavior for every existing
// consumer.
var DefaultChecks = []Check{CheckFollows, CheckMultiVersion, CheckDeadOverrides}

// Selection is the set of enabled checks. The zero value (nil) enables
// none; callers building one from the CLI should use ParseSelection (which
// treats an absent --checks as the default subset) or AllSelection.
type Selection map[Check]bool

// Has reports whether c is enabled in the selection.
func (s Selection) Has(c Check) bool { return s[c] }

// Count returns the number of enabled checks.
func (s Selection) Count() int {
	n := 0
	for _, c := range AllChecks {
		if s[c] {
			n++
		}
	}
	return n
}

// AllSelection returns a Selection with every check enabled — the target of
// the `all` alias.
func AllSelection() Selection { return selectionFromChecks(AllChecks) }

// DefaultSelection returns the Selection used when `--checks` is absent: the
// DefaultChecks subset (see its doc for why nixpkgs-master is opt-in).
func DefaultSelection() Selection { return selectionFromChecks(DefaultChecks) }

// selectionFromChecks builds a Selection enabling exactly the given checks.
func selectionFromChecks(checks []Check) Selection {
	s := make(Selection, len(checks))
	for _, c := range checks {
		s[c] = true
	}
	return s
}

// ParseSelection parses a comma-separated `--checks` value into a
// Selection. An empty string selects the DefaultChecks subset (so behavior
// is unchanged when the flag is absent). "all" is an alias for every check —
// including the opt-in nixpkgs-master convention check — and may appear
// alongside other names. Whitespace around names is tolerated and empty
// fields (e.g. a trailing comma) are ignored. An unrecognized name yields an
// error naming the valid checks.
func ParseSelection(raw string) (Selection, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultSelection(), nil
	}
	sel := make(Selection, len(AllChecks))
	for _, field := range strings.Split(raw, ",") {
		name := strings.TrimSpace(field)
		if name == "" {
			continue
		}
		if name == "all" {
			for _, c := range AllChecks {
				sel[c] = true
			}
			continue
		}
		c := Check(name)
		if !knownCheck(c) {
			return nil, fmt.Errorf("unknown check %q; valid checks are %s (or 'all')", name, validNames())
		}
		sel[c] = true
	}
	if sel.Count() == 0 {
		return nil, fmt.Errorf("no checks selected; valid checks are %s (or 'all')", validNames())
	}
	return sel, nil
}

// knownCheck reports whether c is one of the canonical checks.
func knownCheck(c Check) bool {
	for _, k := range AllChecks {
		if k == c {
			return true
		}
	}
	return false
}

// validNames renders the canonical check names for an error message, e.g.
// "follows, multi-version, dead-overrides".
func validNames() string {
	names := make([]string, 0, len(AllChecks))
	for _, c := range AllChecks {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
