package nixedit

import (
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// matcherEntry is the in-memory loader path under which the embedded
// grammar is registered.
const matcherEntry = "nix.peg"

// nodeName returns the grammar rule name of a node, or "" for nodes that
// carry no name. langlang's Tree.Name panics (index [-1]) on String,
// Sequence, and other non-Node nodes because their nameID is -1, so all
// name lookups go through this guard.
func nodeName(tree langlang.Tree, n langlang.NodeID) string {
	if tree.Type(n) == langlang.NodeType_Node {
		return tree.Name(n)
	}
	return ""
}

// newMatcher compiles the embedded nix.peg into a langlang Matcher via
// the in-memory loader. This lower-level path (loader + database +
// QueryMatcher) is used instead of the MatcherFromBytes convenience so
// the code works against langlang v0.0.12, where that helper does not
// yet exist.
func newMatcher() (langlang.Matcher, error) {
	cfg := langlang.NewConfig()
	// nix.peg manages whitespace explicitly via Trivia rules (CST mode),
	// so disable langlang's automatic Spacing injection — the equivalent
	// of the -disable-spaces CLI flag the toml.peg grammar relies on.
	// Without this, langlang doubles up whitespace handling and the parse
	// fails on the second binding.
	cfg.SetBool("grammar.handle_spaces", false)
	loader := langlang.NewInMemoryImportLoader()
	loader.Add(matcherEntry, nixGrammar)
	db := langlang.NewDatabase(cfg, loader)
	return langlang.QueryMatcher(db, matcherEntry)
}

// findInputsAttrSet locates the editable `inputs` region of a parsed
// flake.nix and returns where to splice new follows bindings.
//
// Two file shapes are supported:
//
//   - Block form: a top-level `inputs = { … }` binding. New follows are
//     spliced inside that attrset (just before its closing brace), and
//     rendered without the leading `inputs.` segment (the block already
//     supplies it). blockMode is true.
//
//   - Flat form: top-level `inputs.<x>.… = …` bindings with no `inputs =
//     { … }` block. New follows are spliced as sibling top-level
//     `inputs.<…>.follows = …` bindings after the last inputs-prefixed
//     binding. blockMode is false.
//
// Mixing the two at one level is a Nix error, so exactly one shape
// applies. Returns ok=false when neither is found, so the caller falls
// back to print-only.
//
// matcher is passed through to blockInsert so it can re-parse the block's
// inner content to compute per-input chunk offsets. After this call returns,
// matcher may have been used for a sub-parse and the caller's tree node IDs
// are stale — do not use tree after calling findInputsAttrSet.
func findInputsAttrSet(tree langlang.Tree, src []byte, matcher langlang.Matcher) (inputsAttrSet, bool) {
	root, ok := tree.Root()
	if !ok {
		return inputsAttrSet{}, false
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return inputsAttrSet{}, false
	}

	existing := map[string]bool{}
	var (
		blockGroup   langlang.NodeID
		haveBlock    bool
		lastFlatKey  langlang.NodeID // the KeyVal of the last inputs.* flat binding
		haveFlat     bool
		flatIndent   string
		flatChunkEnd map[string]int // input name → abs offset after its last flat binding
	)

	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, kvOK := bindingKeyVal(tree, child)
		if !kvOK {
			continue
		}
		path, val, pOK := keyValPath(tree, kv, src)
		if !pOK || len(path) == 0 || path[0] != "inputs" {
			continue
		}
		if len(path) == 1 {
			// `inputs = <value>`: block form if value is an attrset.
			if g, gOK := soleGroup(tree, val); gOK {
				blockGroup = g
				haveBlock = true
			}
			continue
		}
		// `inputs.<x>… = …`: flat form. Record the full attr-path.
		existing[strings.Join(path, ".")] = true
		lastFlatKey = kv
		haveFlat = true
		flatIndent = lineIndent(src, tree.Span(child).Start.Cursor)
		// Track per-input last-binding position for location-preserving splice.
		if len(path) >= 2 {
			if flatChunkEnd == nil {
				flatChunkEnd = map[string]int{}
			}
			flatChunkEnd[path[1]] = afterSemicolon(src, tree.Span(kv).End.Cursor)
		}
	}

	switch {
	case haveBlock:
		return blockInsert(tree, src, blockGroup, matcher)
	case haveFlat:
		// Insert after the last flat binding's terminating ';' (which sits
		// just past the KeyVal span). The caller writes a leading newline +
		// indent so the new binding lands on its own line.
		off := afterSemicolon(src, tree.Span(lastFlatKey).End.Cursor)
		return inputsAttrSet{
			existing:     existing,
			insertOffset: off,
			indent:       flatIndent,
			blockMode:    false,
			leadNewline:  true,
			chunkEnd:     flatChunkEnd,
		}, true
	default:
		// No editable `inputs` (block or flat) found — bail to print-only.
		return inputsAttrSet{}, false
	}
}

// blockInsert computes the splice point inside a block-form `inputs = {
// … }` attrset: just before its closing brace (the global fallback), plus
// per-input chunk offsets for location-preserving splice.
//
// Existing inner binding keys are recovered from the group's opaque text
// (via scanBlockKeys) and normalized to the full `inputs.`-prefixed form
// for idempotency comparison.
//
// matcher is used to re-parse the group's content to compute per-input
// chunk offsets (blockChunkOffsets). This re-parse invalidates the node
// IDs of the outer `tree`, so callers must not use `tree` after this call
// returns.
func blockInsert(tree langlang.Tree, src []byte, group langlang.NodeID, matcher langlang.Matcher) (inputsAttrSet, bool) {
	var (
		brace    langlang.NodeID
		haveCl   bool
		innerTxt string
	)
	// Group → Sequence → [BraceOpen, Inner, BraceClose].
	for _, n := range tree.Children(group) {
		if tree.Type(n) == langlang.NodeType_Sequence {
			for _, c := range tree.Children(n) {
				switch nodeName(tree, c) {
				case "BraceClose":
					brace = c
					haveCl = true
				case "Inner":
					innerTxt = tree.Text(c)
				}
			}
		}
	}
	if !haveCl {
		return inputsAttrSet{}, false
	}
	closeOff := tree.Span(brace).Start.Cursor
	braceIndent := lineIndent(src, closeOff)

	existing := map[string]bool{}
	for _, key := range scanBlockKeys(innerTxt) {
		// Inner keys omit the `inputs.` prefix the block supplies.
		existing["inputs."+key] = true
	}

	// Extract group text and base offset before calling blockChunkOffsets:
	// that re-parse invalidates the current tree's node IDs, so all tree
	// navigation must be complete first.
	groupBase := tree.Span(group).Start.Cursor
	groupText := []byte(tree.Text(group))

	ins := inputsAttrSet{
		existing:  existing,
		indent:    braceIndent + "  ", // one level deeper than the brace
		blockMode: true,
		chunkEnd:  blockChunkOffsets(matcher, groupText, groupBase),
	}

	if onlyBlankBefore(src, closeOff) {
		// Own-line closing brace: splice at the start of its line so the
		// brace's own indentation is preserved and each new binding is
		// written as a full `<indent><text>;\n` line above it.
		ins.insertOffset = lineStart(src, closeOff)
	} else {
		// Closing brace shares its line with content (e.g. a single-line
		// `inputs = { a.url = "x"; };`). Splice right at the brace with a
		// leading newline so new bindings land on their own lines just
		// before the `}` instead of being injected mid-line.
		ins.insertOffset = closeOff
		ins.leadNewline = true
		ins.trailNewlineIndent = braceIndent
	}
	return ins, true
}

// blockChunkOffsets re-parses the content of an `inputs = { … }` block
// and returns a map from each top-level input name to the absolute byte
// offset in the original file immediately after the semicolon of that
// input's last binding. This is the location-preserving splice point: new
// follows bindings for input X are inserted there rather than at the
// global block-end, keeping them adjacent to X's url, existing follows,
// or nested sub-block.
//
// groupText is the full text of the Group node (including its surrounding
// `{` and `}`), and base is its absolute start offset in the original
// file. The returned offsets are absolute (base-relative) so they can be
// used directly as splice points into the original source.
//
// Returns nil when the re-parse fails or the group has no bindings —
// callers fall back to the global insertOffset in that case.
func blockChunkOffsets(matcher langlang.Matcher, groupText []byte, base int) map[string]int {
	tree, seq, ok := reparseGroupSequence(matcher, groupText)
	if !ok {
		return nil
	}
	chunkEnd := map[string]int{}
	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != "Binding" {
			continue
		}
		kv, ok := bindingKeyVal(tree, child)
		if !ok {
			continue
		}
		path, _, ok := keyValPath(tree, kv, groupText)
		if !ok || len(path) == 0 {
			continue
		}
		inputName := path[0]
		kvEnd := tree.Span(kv).End.Cursor
		chunkEnd[inputName] = base + afterSemicolon(groupText, kvEnd)
	}
	if len(chunkEnd) == 0 {
		return nil
	}
	return chunkEnd
}

// reparseGroupSequence re-parses groupText — the text of an `inputs = { … }`
// group, re-parsed in isolation as its own file — and returns its top-level
// Binding sequence. ok is false when the re-parse or either structural
// lookup fails, in which case callers fall back to whatever "nothing found"
// behavior fits their caller (nil map, nil slice, ...). Shared by every
// caller that needs to walk such a group's direct bindings a second time
// (blockChunkOffsets here; canonical.go's blockBindingOrder).
func reparseGroupSequence(matcher langlang.Matcher, groupText []byte) (tree langlang.Tree, seq langlang.NodeID, ok bool) {
	tree, _, err := matcher.Match(groupText)
	if err != nil {
		return tree, 0, false
	}
	root, rootOK := tree.Root()
	if !rootOK {
		return tree, 0, false
	}
	seq, seqOK := topAttrSetSequence(tree, root)
	return tree, seq, seqOK
}

// onlyBlankBefore reports whether everything between the start of off's
// line and off is whitespace — i.e. off (a closing brace) sits alone on
// its own line, so a line-start splice is safe.
func onlyBlankBefore(src []byte, off int) bool {
	for i := lineStart(src, off); i < off; i++ {
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}
	return true
}

// scanBlockKeys extracts the LHS attr-paths of `a.b.c = …;` bindings from
// the opaque text of an `inputs` attrset block. It is a deliberately
// simple line scanner (not a parser): for each `;`-terminated segment it
// takes the text before the first `=`, drops `#` line comments, and uses
// the last non-blank line — where the attr-path sits next to the `=` —
// recording it when it looks like a dotted attr-path. Used only for
// idempotency, so a missed key at worst re-adds a line that Nix would
// reject — callers run lint again after fixing, which would surface that.
func scanBlockKeys(inner string) []string {
	var keys []string
	for _, seg := range strings.Split(inner, ";") {
		eq := strings.Index(seg, "=")
		if eq < 0 {
			continue
		}
		lhs := attrPathBeforeEquals(seg[:eq])
		if lhs == "" || !isAttrPath(lhs) {
			continue
		}
		keys = append(keys, lhs)
	}
	return keys
}

// attrPathBeforeEquals returns the bare attr-path that immediately
// precedes a binding's `=`, given the text before that `=`. A binding's
// key and `=` share a line, so it drops `#` line comments and earlier
// lines (preceding bindings' trailing content, blank lines) and returns
// the trimmed last non-blank line. Returns "" if none remains.
func attrPathBeforeEquals(before string) string {
	lines := strings.Split(before, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if c := strings.Index(line, "#"); c >= 0 {
			line = line[:c]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// isAttrPath reports whether s is a dotted run of bare identifiers, e.g.
// "igloo.inputs.systems.follows". Quoted segments and interpolation are
// not recognized (conservative: such a key just won't dedupe).
func isAttrPath(s string) bool {
	for _, seg := range strings.Split(s, ".") {
		if seg == "" {
			return false
		}
		for i, r := range seg {
			ok := r == '_' || r == '-' || r == '\'' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(i > 0 && r >= '0' && r <= '9')
			if !ok {
				return false
			}
		}
	}
	return true
}

// topAttrSetSequence returns the Sequence node holding the top-level
// attribute set's children (BraceOpen, Trivia, Binding*, BraceClose).
// Walks File → AttrSet → Sequence.
func topAttrSetSequence(tree langlang.Tree, root langlang.NodeID) (langlang.NodeID, bool) {
	attrset, ok := childNamed(tree, root, "AttrSet")
	if !ok {
		return 0, false
	}
	return firstSequence(tree, attrset)
}

// bindingKeyVal returns the KeyVal node inside a Binding (Binding →
// Sequence → [KeyVal|Inherit, Trivia]). Inherit bindings yield false.
func bindingKeyVal(tree langlang.Tree, binding langlang.NodeID) (langlang.NodeID, bool) {
	seq, ok := firstSequence(tree, binding)
	if !ok {
		return 0, false
	}
	return childNamed(tree, seq, "KeyVal")
}

// keyValPath returns the attr-path segments and the Value node of a
// KeyVal (KeyVal → Sequence → [AttrPath, Trivia, Equals, Trivia, Value,
// Semi]).
func keyValPath(tree langlang.Tree, kv langlang.NodeID, src []byte) ([]string, langlang.NodeID, bool) {
	seq, ok := firstSequence(tree, kv)
	if !ok {
		return nil, 0, false
	}
	ap, ok := childNamed(tree, seq, "AttrPath")
	if !ok {
		return nil, 0, false
	}
	val, ok := childNamed(tree, seq, "Value")
	if !ok {
		return nil, 0, false
	}
	return attrPathSegments(tree, ap), val, true
}

// attrPathSegments returns the identifier/string segments of an AttrPath
// node in order.
func attrPathSegments(tree langlang.Tree, ap langlang.NodeID) []string {
	var segs []string
	tree.Visit(ap, func(n langlang.NodeID) bool {
		switch nodeName(tree, n) {
		case "Identifier":
			segs = append(segs, tree.Text(n))
		case "String":
			segs = append(segs, unquote(tree.Text(n)))
		}
		return true
	})
	return segs
}

// soleGroup returns the Group node when a Value is *exactly* one Group
// (i.e. `inputs = { … }`), false otherwise. The match is strict: the
// Value's only non-trivia content must be the Group. An `inputs` value
// that is a larger expression containing a group — `let … in { … }`,
// `f { … }`, `{ … } // { … }` — is rejected so block mode is not
// mis-applied to it (the caller then bails to print-only rather than
// splicing into the wrong braces). OuterText that is only whitespace is
// tolerated so surrounding blank lines/newlines around the `{ … }` do
// not disqualify it.
func soleGroup(tree langlang.Tree, val langlang.NodeID) (langlang.NodeID, bool) {
	// Value <- (Group / String / Comment / OuterText)* — its direct
	// children are the alternation members in order. A bare attrset value
	// is a single Group child; anything else (extra Groups, Strings,
	// non-blank OuterText) means the value is a compound expression.
	var (
		group   langlang.NodeID
		haveOne bool
	)
	for _, c := range valueItems(tree, val) {
		switch nodeName(tree, c) {
		case "Group":
			if haveOne {
				return 0, false // more than one group → compound expr
			}
			group = c
			haveOne = true
		case "OuterText":
			if strings.TrimSpace(tree.Text(c)) != "" {
				return 0, false // real tokens around the group
			}
		default:
			// A String, Comment, or anything else as a sibling of the
			// group means this is not a bare attrset value.
			return 0, false
		}
	}
	return group, haveOne
}

// valueItems returns the direct alternation members of a Value node,
// descending through a single anonymous Sequence wrapper if present
// (langlang renders a multi-item `(A / B)*` as a Sequence, a single item
// as the item directly under the named Value node).
func valueItems(tree langlang.Tree, val langlang.NodeID) []langlang.NodeID {
	children := tree.Children(val)
	if len(children) == 1 && tree.Type(children[0]) == langlang.NodeType_Sequence {
		return tree.Children(children[0])
	}
	return children
}

// childNamed returns the first direct (or single-child) descendant of n
// whose rule name matches. It descends through anonymous wrapper nodes
// (Sequence/Node with one child) to find a named child one level down.
func childNamed(tree langlang.Tree, n langlang.NodeID, name string) (langlang.NodeID, bool) {
	for _, c := range tree.Children(n) {
		if nodeName(tree, c) == name {
			return c, true
		}
		// Descend one level through an anonymous wrapper (e.g. a Node
		// whose child is the named rule, or a Sequence).
		if nodeName(tree, c) == "" || tree.Type(c) == langlang.NodeType_Sequence {
			if inner, ok := childNamed(tree, c, name); ok {
				return inner, true
			}
		}
	}
	return 0, false
}

// firstSequence returns the first Sequence node at or just below n.
func firstSequence(tree langlang.Tree, n langlang.NodeID) (langlang.NodeID, bool) {
	if tree.Type(n) == langlang.NodeType_Sequence {
		return n, true
	}
	for _, c := range tree.Children(n) {
		if tree.Type(c) == langlang.NodeType_Sequence {
			return c, true
		}
	}
	return 0, false
}

// lineStart returns the byte offset of the start of the line containing
// off (the index just after the preceding newline, or 0).
func lineStart(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	i := off
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

// lineIndent returns the leading whitespace of the line containing byte
// offset off in src.
func lineIndent(src []byte, off int) string {
	if off > len(src) {
		off = len(src)
	}
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	i := start
	for i < off && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return string(src[start:i])
}

// afterSemicolon advances off past an immediately-following ';' (skipping
// intervening whitespace, including newlines), so a flat-binding insert
// lands after the last binding's terminator even when the ';' is written
// on a following line. If no ';' is found it returns off unchanged.
func afterSemicolon(src []byte, off int) int {
	i := off
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	if i < len(src) && src[i] == ';' {
		return i + 1
	}
	return off
}

// unquote strips surrounding double quotes from a quoted attr-name
// segment. Best-effort; leaves the text as-is if not double-quoted.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
