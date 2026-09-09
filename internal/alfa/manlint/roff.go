package manlint

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// nameHeadingRe matches a NAME section heading in either roff dialect:
	// man(7) `.SH NAME` / `.SH "NAME"` and mdoc(7) `.Sh NAME`. The same
	// expression spinclass's index extractor uses.
	nameHeadingRe = regexp.MustCompile(`(?i)^\.S[Hh][ \t]+"?NAME"?[ \t]*$`)
	// sectionStartRe matches any request that ends a NAME section: the next
	// section or subsection heading in either dialect.
	sectionStartRe = regexp.MustCompile(`^\.S[HhSs]([ \t]|$)`)
	// fontEscapeRe matches inline font escapes: `\fB`, `\f(CW`, `\f[CR]`.
	fontEscapeRe = regexp.MustCompile(`\\f(\([A-Za-z0-9]{2}|\[[^\]]*\]|[A-Za-z0-9])`)
	// fontMacros are the man(7) requests whose arguments are NAME content
	// (`.B foo`, `.BR foo "\- bar"`), as opposed to breaks and paragraphs,
	// which separate entries.
	fontMacros = map[string]bool{
		"B": true, "I": true, "R": true, "BR": true, "RB": true, "BI": true,
		"IB": true, "IR": true, "RI": true, "SB": true, "SM": true,
	}
)

// roffCleaner resolves the escapes that survive into a NAME line, longest
// spellings first so `\*(lq` is not read as `\*` + `(lq`.
var roffCleaner = strings.NewReplacer(
	`\*(lq`, `"`,
	`\*(rq`, `"`,
	`\(em`, "—",
	`\(en`, "–",
	`\[em]`, "—",
	`\[en]`, "–",
	`\(oq`, "‘",
	`\(cq`, "’",
	`\(lq`, "“",
	`\(rq`, "”",
	`\(dq`, `"`,
	`\(aq`, `'`,
	`\(hy`, "-",
	`\-`, "-",
	`\ `, " ",
	`\~`, " ",
	`\&`, "",
	`\|`, "",
	`\^`, "",
	`\/`, "",
	`\,`, "",
	`\e`, `\`,
)

// cleanRoff strips font escapes, resolves the common character escapes,
// and collapses whitespace — the text a reader sees, which is what the
// length budget is measured against and what the separator is found in.
func cleanRoff(s string) string {
	s = fontEscapeRe.ReplaceAllString(s, "")
	s = roffCleaner.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// ParseRoffNameSection extracts the NAME entries from rendered roff source
// in either the man(7) or mdoc(7) dialect. It returns an error when the
// page has no NAME section or its content cannot be read as
// `name - description` — the cases lexgrog(1) reports as a parse failure.
func ParseRoffNameSection(src string) ([]Entry, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if nameHeadingRe.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errors.New("no NAME section")
	}
	body := lines[start+1:]
	for i, l := range body {
		if sectionStartRe.MatchString(l) {
			body = body[:i]
			break
		}
	}
	if strings.HasPrefix(lines[start], ".Sh") {
		return parseMdocNameBody(body)
	}
	return parseManNameBody(body)
}

// parseManNameBody groups the lines of a man(7) NAME section into entries.
// Text lines and font-macro lines contribute content; blank lines and any
// other request (`.PP`, `.br`, `.sp`) end the current entry; comments are
// ignored. Every physical line that contributes counts toward the entry's
// LineCount, so a description continued on a second line is detectable.
func parseManNameBody(body []string) ([]Entry, error) {
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
	for _, raw := range body {
		line := strings.TrimRight(raw, " \t")
		switch {
		case line == "":
			flush()
		case isRoffComment(line):
			// Ignored without ending the entry: a comment between two
			// continuation lines is still one wrapped description.
		case line[0] == '.' || line[0] == '\'':
			name, args := splitRequest(line)
			if fontMacros[name] {
				current = append(current, cleanRoff(strings.Join(args, " ")))
			} else {
				flush()
			}
		default:
			current = append(current, cleanRoff(line))
		}
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

// parseMdocNameBody reads an mdoc(7) NAME section: `.Nm` lines name the
// features and `.Nd` carries the description. Any content line following
// the `.Nd` before the next section — whether text or another inline
// macro such as `.Li` — continues the description, and counts as a wrapped
// line.
func parseMdocNameBody(body []string) ([]Entry, error) {
	var (
		names    []string
		descSeen bool
		desc     []string
	)
	for _, raw := range body {
		line := strings.TrimRight(raw, " \t")
		if line == "" || isRoffComment(line) {
			continue
		}
		if line[0] == '.' || line[0] == '\'' {
			name, args := splitRequest(line)
			switch name {
			case "Nm":
				if !descSeen && len(args) > 0 {
					names = append(names, args[0])
				}
				continue
			case "Nd":
				descSeen = true
				desc = append(desc, cleanRoff(strings.Join(args, " ")))
				continue
			}
			if descSeen {
				desc = append(desc, cleanRoff(strings.Join(args, " ")))
			}
			continue
		}
		if descSeen {
			desc = append(desc, cleanRoff(line))
		}
	}
	if !descSeen {
		return nil, errors.New("mdoc NAME section has no .Nd description")
	}
	if len(names) == 0 {
		return nil, errors.New("mdoc NAME section has no .Nm name")
	}
	return []Entry{{
		Names:       names,
		Description: strings.Join(strings.Fields(strings.Join(desc, " ")), " "),
		LineCount:   len(desc),
	}}, nil
}

// isRoffComment reports whether a line is a roff comment (`.\"`, `'\"`,
// or a bare `\"` line), which carries no content.
func isRoffComment(line string) bool {
	return strings.HasPrefix(line, `.\"`) || strings.HasPrefix(line, `'\"`) || strings.HasPrefix(line, `\"`)
}

// splitRequest splits a request line into its macro name and arguments,
// honouring double-quoted arguments (`.BR foo "\- bar"`).
func splitRequest(line string) (string, []string) {
	fields := splitQuoted(strings.TrimSpace(line[1:]))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

// splitQuoted splits on whitespace, keeping double-quoted runs together
// with their quotes removed.
func splitQuoted(s string) []string {
	var (
		fields []string
		cur    strings.Builder
		inQ    bool
		has    bool
	)
	flush := func() {
		if has {
			fields = append(fields, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			has = true
		case (r == ' ' || r == '\t') && !inQ:
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return fields
}
