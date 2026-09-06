package nixedit

import "strings"

// formalsScan is the result of scanning a `{ … }` formals group: the names it
// binds, whether it carries `...`, and where the last meaningful token ends.
//
// lastEnd is an offset relative to the START of the group text and points just
// past the final non-trivia token at formals depth — the insertion point for a
// new formal. It is tracked during a forward scan rather than recovered by
// walking backwards from the closing brace, so a trailing comment inside the
// formals cannot be mistaken for the last token (an insertion after a `#`
// comment's text would land inside the comment).
type formalsScan struct {
	names       []string
	hasEllipsis bool
	lastEnd     int
	lastByte    byte // the final non-trivia byte, or '{' when the set is empty
}

// scanFormals walks a formals group's source (including both braces),
// collecting the names bound at formals depth and noting an `...` ellipsis.
//
// Only depth 1 counts: a default value may itself contain braces and an
// ellipsis (`{ pkgs ? import <nixpkgs> { ... } }`), and those inner tokens
// bind nothing in the outer signature. Strings and comments are skipped so a
// `,` or `}` inside either is not read as structure.
func scanFormals(text string) formalsScan {
	sc := formalsScan{lastByte: '{'}
	depth := 0
	expectName := false
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == '{' || c == '[' || c == '(':
			depth++
			if depth == 1 {
				expectName = true
				sc.lastEnd = i + 1
				sc.lastByte = c
			}
			i++
		case c == '}' || c == ']' || c == ')':
			depth--
			i++
		case c == '"':
			i = skipDoubleQuoted(text, i)
			sc.noteToken(i, '"', depth)
		case strings.HasPrefix(text[i:], "''"):
			i = skipIndentedString(text, i)
			sc.noteToken(i, '\'', depth)
		case c == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case strings.HasPrefix(text[i:], "/*"):
			j := strings.Index(text[i+2:], "*/")
			if j < 0 {
				i = len(text)
			} else {
				i += 2 + j + 2
			}
		case depth == 1 && strings.HasPrefix(text[i:], "..."):
			sc.hasEllipsis = true
			i += 3
			sc.noteToken(i, '.', depth)
		case c == ',':
			if depth == 1 {
				expectName = true
			}
			i++
			sc.noteToken(i, ',', depth)
		case isIdentStart(c):
			j := i
			for j < len(text) && isIdentByte(text[j]) {
				j++
			}
			if depth == 1 && expectName {
				sc.names = append(sc.names, text[i:j])
				expectName = false
			}
			sc.noteToken(j, text[j-1], depth)
			i = j
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		default:
			i++
			sc.noteToken(i, c, depth)
		}
	}
	return sc
}

// noteToken records the end of a token that sits at formals depth, so lastEnd
// tracks the insertion point for a new formal.
func (s *formalsScan) noteToken(end int, b byte, depth int) {
	if depth != 1 {
		return
	}
	s.lastEnd = end
	s.lastByte = b
}

// ellipsisInsertion returns the absolute byte offset at which to insert an
// ellipsis into this closed formals set, and the text to insert there.
//
// The text mirrors the set's existing layout so the result needs no
// reformatting: a multi-line set gets `...` on its own line at the same indent
// as the formals around it, a single-line set gets it inline. A set with no
// trailing comma gets one, since `...` must follow a separator.
func (f *outputsFormals) ellipsisInsertion(src []byte) (int, string) {
	group := string(src[f.span.start:f.span.end])
	sc := scanFormals(group)
	at := f.span.start + sc.lastEnd

	// Multi-line when a newline separates the last token from the closing
	// brace — i.e. the set is written with each formal on its own line.
	multiline := strings.Contains(group[sc.lastEnd:], "\n")

	if sc.lastByte == '{' {
		// Empty formals: `{ }` or `{}`.
		if strings.TrimSpace(group[sc.lastEnd:]) == "}" && !multiline {
			if strings.HasPrefix(group[sc.lastEnd:], " ") {
				return at, " ..."
			}
			return at, " ... "
		}
		return at, " ..."
	}

	sep := ""
	if sc.lastByte != ',' {
		sep = ","
	}
	if multiline {
		return at, sep + "\n" + lineIndent(src, at) + "..."
	}
	return at, sep + " ..."
}

// skipDoubleQuoted returns the offset just past the double-quoted string
// starting at i, honouring backslash escapes.
func skipDoubleQuoted(text string, i int) int {
	i++ // opening quote
	for i < len(text) {
		if text[i] == '\\' {
			i += 2
			continue
		}
		if text[i] == '"' {
			return i + 1
		}
		i++
	}
	return i
}

// skipIndentedString returns the offset just past the ”…” string starting at
// i, honouring the ”-prefixed escapes Nix uses inside them.
func skipIndentedString(text string, i int) int {
	i += 2 // opening ''
	for i < len(text) {
		if strings.HasPrefix(text[i:], "''") {
			if i+2 < len(text) {
				switch text[i+2] {
				case '$', '\\', '\'':
					i += 3
					continue
				}
			}
			return i + 2
		}
		i++
	}
	return i
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentByte(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '-' || c == '\''
}

// isBlank reports whether s is empty or only whitespace.
func isBlank(s string) bool { return strings.TrimSpace(s) == "" }
