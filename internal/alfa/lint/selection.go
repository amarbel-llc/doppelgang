package lint

import (
	"fmt"
	"sort"
	"strings"
)

// Check identifies one of lint's three analyses. The string value is the
// name accepted by `doppelgang lint --checks`.
type Check string

const (
	CheckFollows       Check = "follows"
	CheckMultiVersion  Check = "multi-version"
	CheckDeadOverrides Check = "dead-overrides"
)

// AllChecks is the canonical order of every check. Renderers iterate this
// and filter by the active Selection so output order is stable regardless
// of which subset is selected.
var AllChecks = []Check{CheckFollows, CheckMultiVersion, CheckDeadOverrides}

// Selection is the set of enabled checks. The zero value (nil) enables
// none; callers building one from the CLI should use ParseSelection (which
// treats an absent --checks as all) or AllSelection.
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

// AllSelection returns a Selection with every check enabled — the default
// when `--checks` is absent.
func AllSelection() Selection {
	s := make(Selection, len(AllChecks))
	for _, c := range AllChecks {
		s[c] = true
	}
	return s
}

// ParseSelection parses a comma-separated `--checks` value into a
// Selection. An empty string selects every check (the default, so behavior
// is unchanged when the flag is absent). "all" is an alias for every check
// and may appear alongside other names. Whitespace around names is
// tolerated and empty fields (e.g. a trailing comma) are ignored. An
// unrecognized name yields an error naming the valid checks.
func ParseSelection(raw string) (Selection, error) {
	if strings.TrimSpace(raw) == "" {
		return AllSelection(), nil
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
