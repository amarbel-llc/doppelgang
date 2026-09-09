package manlint

import (
	"errors"
	"regexp"
	"strings"
)

// scdocNameHeadingRe matches the scdoc(5) `# NAME` section heading.
var scdocNameHeadingRe = regexp.MustCompile(`^#[ \t]+NAME[ \t]*$`)

// ParseScdocNameSection extracts the NAME entries from scdoc(5) source:
// the lines between `# NAME` and the next heading, one entry per paragraph
// (blank-line separated), each `name - description`. The same rules as the
// rendered page apply, so a wrapped source paragraph is caught before scdoc
// renders it into a wrapped roff line.
func ParseScdocNameSection(src string) ([]Entry, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if scdocNameHeadingRe.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errors.New("no NAME section")
	}
	var (
		entries []Entry
		current []string
		errs    []string
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		e, err := entryFromLines(current)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			entries = append(entries, e)
		}
		current = nil
	}
	for _, raw := range lines[start+1:] {
		if strings.HasPrefix(raw, "#") {
			break
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			flush()
			continue
		}
		current = append(current, cleanScdoc(line))
	}
	flush()
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	if len(entries) == 0 {
		return nil, errors.New("NAME section has no content")
	}
	return entries, nil
}

// cleanScdoc strips scdoc(5) inline formatting from a line: `*bold*`
// markers, `_underline_` markers at word boundaries (an underscore between
// two word characters is literal, as in scdoc), and backslash escapes,
// which yield the escaped character.
func cleanScdoc(line string) string {
	var out strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && i+1 < len(runes):
			i++
			out.WriteRune(runes[i])
		case r == '*':
		case r == '_':
			prevWord := i > 0 && isWordRune(runes[i-1])
			nextWord := i+1 < len(runes) && isWordRune(runes[i+1])
			if prevWord && nextWord {
				out.WriteRune(r)
			}
		default:
			out.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func isWordRune(r rune) bool {
	return r == '-' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
}
