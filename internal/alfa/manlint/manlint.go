// Package manlint checks man page NAME sections against the contract
// downstream index builders rely on: `name - description`, parsable, with
// the description non-empty, on one physical source line, and short.
//
// spinclass renders a "Manpage index" into every session's system prompt
// from each page's NAME line (spinclass FDR 0030) using the same extraction
// whatis(1)/lexgrog(1) perform. A description that is really an MCP tool's
// paragraph-long help text, a NAME section that is not `name - description`
// at all, or a description wrapped over two roff lines (which lexgrog joins
// but the index does not) each degrade that index. This package is the gate.
package manlint

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// DefaultMaxDescription is the `--max` default: the longest description the
// index can carry as one row without wrapping.
const DefaultMaxDescription = 72

// Check names one rule a finding violates. The string is what `lint-man`
// prints between the page path and the detail.
type Check string

const (
	// CheckUnreadable: the page could not be read or decompressed.
	CheckUnreadable Check = "unreadable"
	// CheckUnparsable: no NAME section, or its content is not
	// `name - description` — the same verdict lexgrog(1) reports as "parse
	// failed".
	CheckUnparsable Check = "unparsable"
	// CheckEmpty: the separator is present but nothing follows it.
	CheckEmpty Check = "empty"
	// CheckWrapped: the NAME entry spans more than one physical source line.
	// lexgrog joins the lines; spinclass's index reads only the first, so it
	// truncates mid-sentence. Fails regardless of lexgrog's verdict.
	CheckWrapped Check = "wrapped"
	// CheckLong: the description exceeds the --max character budget.
	CheckLong Check = "long"
	// CheckTrailingPeriod: the description ends with a period. Warning-level:
	// a NAME description is a phrase, not a sentence, but the index still
	// renders it, so this never fails the gate on its own.
	CheckTrailingPeriod Check = "trailing-period"
)

// Severity separates the findings that fail the gate from advisory ones.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one rule violation on one page.
type Finding struct {
	Path     string   `json:"path"`
	Check    Check    `json:"check"`
	Severity Severity `json:"severity"`
	Detail   string   `json:"detail"`
}

// Entry is one `names - description` pair from a NAME section. A page
// documenting several features carries several, separated by a break or
// paragraph macro.
type Entry struct {
	Names       []string
	Description string
	// LineCount is the number of physical source lines the entry occupied.
	// Anything above one is a wrapped description.
	LineCount int
}

// LintEntries applies the per-entry rules to a parsed NAME section. The
// caller supplies the page path for the findings and the --max budget.
func LintEntries(path string, entries []Entry, maxDescription int) []Finding {
	var findings []Finding
	add := func(check Check, sev Severity, detail string) {
		findings = append(findings, Finding{Path: path, Check: check, Severity: sev, Detail: detail})
	}
	for _, e := range entries {
		// Name the entry only when the page has several; a single-entry
		// page's finding is unambiguous without it.
		prefix := ""
		if len(entries) > 1 {
			prefix = "entry " + strings.Join(e.Names, ", ") + ": "
		}
		if e.LineCount > 1 {
			add(CheckWrapped, SeverityError, fmt.Sprintf("%sNAME entry spans %d source lines", prefix, e.LineCount))
		}
		if e.Description == "" {
			add(CheckEmpty, SeverityError, prefix+"NAME line has no description after the separator")
			continue
		}
		if n := utf8.RuneCountInString(e.Description); n > maxDescription {
			add(CheckLong, SeverityError, fmt.Sprintf("%s%d > %d chars", prefix, n, maxDescription))
		}
		if strings.HasSuffix(e.Description, ".") {
			add(CheckTrailingPeriod, SeverityWarning, prefix+"description ends with a period")
		}
	}
	return findings
}

// HasErrors reports whether any finding is error-severity — the gate's
// exit-code condition.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// splitNameDescription splits an already-cleaned NAME line into its names
// and description. The separator is a space-delimited dash in any of the
// spellings lexgrog(1) accepts once roff escapes are resolved: `-`, `--`,
// an em or en dash. It must be space-delimited on the left because names
// and descriptions legitimately contain hyphens (`dodder-transform`); on the
// right it may also end the line, which is how an empty description looks.
// Names containing whitespace are dropped, as lexgrog does.
func splitNameDescription(line string) (names []string, desc string, ok bool) {
	for _, sep := range []string{" - ", " -- ", " — ", " – "} {
		if i := strings.Index(line, sep); i >= 0 {
			return parseNames(line[:i]), strings.TrimSpace(line[i+len(sep):]), true
		}
	}
	for _, tail := range []string{" -", " --", " —", " –"} {
		if strings.HasSuffix(line, tail) {
			return parseNames(strings.TrimSuffix(line, tail)), "", true
		}
	}
	return nil, "", false
}

// parseNames splits the left-hand side of a NAME line on commas, dropping
// empty fields and any name containing whitespace.
func parseNames(left string) []string {
	var names []string
	for _, n := range strings.Split(left, ",") {
		n = strings.TrimSpace(n)
		if n == "" || strings.ContainsAny(n, " \t") {
			continue
		}
		names = append(names, n)
	}
	return names
}

// entryFromLines builds an Entry from the physical lines of one NAME entry,
// already cleaned of markup. It fails when the joined text has no
// separator or no usable name before it.
func entryFromLines(lines []string) (Entry, error) {
	joined := strings.Join(lines, " ")
	names, desc, ok := splitNameDescription(joined)
	if !ok {
		return Entry{}, fmt.Errorf("NAME line %q has no ` - ` separator", joined)
	}
	if len(names) == 0 {
		return Entry{}, fmt.Errorf("NAME line %q has no name before the separator", joined)
	}
	return Entry{Names: names, Description: desc, LineCount: len(lines)}, nil
}
