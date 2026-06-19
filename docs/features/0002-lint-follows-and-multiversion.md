---
status: exploring
date: 2026-05-25
promotion-criteria: |
  Promote to `proposed` once: (1) the follows recommendations have been
  validated against a larger real-world flake.lock than this repo's (confirm
  the emitted `inputs.X...follows = "Y"` lines apply cleanly and produce a
  `nix flake lock` with fewer nodes); (2) the canonical-node selection
  heuristic (shortest attr-path, lexical tie-break) is confirmed to pick a
  target that actually exists as a writable input edge in the user's
  `flake.nix`; (3) the multi-parent case (a duplicate node reached by more
  than one input edge) is exercised on a real lock and the "repeat for each"
  note is judged sufficient or replaced with per-edge lines.
---

# `lint` — follows opportunities and multi-version inputs

> Implementation note: per issue #4 and explicit user direction, the v1
> described here is landing concurrently with this FDR rather than waiting on
> promotion. The `exploring` status reflects that the heuristics (canonical
> selection, multi-parent handling) are still open to revision, not that the
> command is unimplemented.

## Problem Statement

Flakes accumulate input duplication silently. When two inputs each declare
their own `nixpkgs` (or `flake-utils`, `systems`, `treefmt-nix`, …) without a
`follows`, Nix records each as a separate node in `flake.lock` — often visible
as `_2` / `_3` suffixes. The result is multiple byte-identical sources pinned
under different node names, and sometimes multiple *revisions* of the same
repository coexisting in one lock graph. Both inflate the lock, the eval, and
(downstream) the closure.

Today `doppelgang` answers "what is duplicated in the realized closure?"
(`dupes`) but nothing inspects `flake.lock` itself, where the duplication
originates and where the mechanical fix lives. A reader who sees two
`nixpkgs-2.40` copies in a `dupes` run still has to open `flake.lock`, trace
which inputs pinned them, and hand-write the `follows` lines to collapse them.

`lint` closes that gap with two flake.lock analyses:

1. **Follows opportunities.** Identify nodes that pin a *byte-identical*
   source and emit the concrete `inputs.X.follows = "Y"` line(s) that collapse
   them onto one node. Identical source = safe, mechanical fix.
2. **Multi-version inputs.** Identify source repositories pinned at *more than
   one revision* and surface them. Picking a winning revision is a judgment
   call, so these are highlighted, not auto-fixed.

`lint` is a pure offline lockfile analysis. (The closure-level version-drift
view of the same underlying problem lives in `dupes`; an earlier revision of
this command appended it, but it was dropped so `lint` needs neither a built
installable nor `nix` on `PATH`.)

## Interface

```
doppelgang lint [--flake .] [--format auto|text|json|ndjson] [--fix]
```

- `--flake` (default `.`) — directory containing `flake.lock`. Read offline;
  no `nix` invocation (except under `--fix`, see below).
- `--format` (default `auto`) — output format. `text` is the bordered
  human-readable view; `ndjson` is the amarbel-llc/tap test-result NDJSON
  schema; `json` is a single indented JSON document. `auto` emits `text` when
  stdout is a TTY and `ndjson` otherwise, so piped/redirected output is
  machine-readable without a flag.
- `--fix` (default off) — apply the follows-opportunity edits to
  `<flake>/flake.nix`, re-lock, and stage the touched files. See *Repair mode*
  below. Needs `nix` on `PATH`; orthogonal to `--format` (the report is still
  rendered, then the fix runs, with all fix progress on stderr).

### Repair mode (`--fix`)

`--fix` promotes the follows opportunities from "print" to "apply" (issue #9):

1. Gather the `inputs.X…follows = "Y"` line(s) `lint` already computed for the
   **follows opportunities**, and splice them into `<flake>/flake.nix`'s
   top-level `inputs` attribute set.
2. Re-lock via `nix flake lock` so `flake.lock` reflects the collapsed graph.
3. `git add flake.nix flake.lock` (self-staging, per the conformist/dewey
   repair-command convention, so it composes with a `nix fmt` / pre-commit
   `--staged` flow). A non-git target makes staging a non-fatal warning.
4. Reload and re-analyze the regenerated lock for an honest exit code.

**Multi-version inputs stay report-only.** Choosing a revision changes
behavior, so `--fix` never collapses them; it fixes only the byte-identical
follows opportunities and still exits non-zero if any multi-version finding —
or any residual follows opportunity — remains afterward. Exit is `0` only when,
post-fix, neither follows nor multi-version findings remain.

**Idempotent.** Re-running `--fix` on an already-collapsed flake is a no-op:
the follows are already effective, so `lint` emits no lines, so nothing is
applied. (A defensive check also skips a line whose attr-path is already bound
under `inputs`, covering a half-applied `flake.nix` whose lock wasn't
regenerated.)

**flake.nix surgery.** Editing `flake.nix` is Nix-expression surgery, not a
lockfile rewrite. `internal/0/nixedit` parses `flake.nix` with an embedded
shallow Nix PEG (`nix.peg`) via amarbel-llc/langlang's runtime matcher, locates
the top-level `inputs` attrset by parse-tree span, and splices the `follows`
bindings by byte offset, preserving the rest of the file. Two `flake.nix`
shapes are handled: an `inputs = { … }` block (bindings spliced inside it,
before the closing brace, without the redundant `inputs.` prefix) and flat
top-level `inputs.x.… = …` bindings (a sibling flat binding is appended). The
grammar is deliberately partial — it parses only the structure needed to find
`inputs`, skipping every binding *value* opaquely (balanced-, string-, and
comment-aware) — and any file it cannot parse, or that has no editable `inputs`
attrset, makes `--fix` print the lines to apply by hand and exit non-zero
rather than risk corrupting the file. This is `--fix`'s only dependency on
`nix` (the re-lock) and on a writable, parseable `flake.nix`; plain `lint`
stays offline and lockfile-only.

### Output sections (`--format text`)

- `── follows opportunities ──` — one block per identical-source group:
  a header naming the shared source and how many times it is pinned, followed
  by the concrete `follows` line(s) to add.
- `── multi-version inputs ──` — one line per `owner/repo` pinned at multiple
  revs, listing each short rev and a sample attr-path that reaches it.

Each section prints a positive "nothing found" line when empty so the reader
knows the pass ran.

### NDJSON mapping (`--format ndjson`)

The two checks map onto the tap test-result schema as two top-level `test`
records — `"follows opportunities"` and `"multi-version inputs"` — each `ok`
when its check found nothing. Every finding becomes a nested subtest carrying
a structured `diagnostic` object (the follows group's identity / canonical /
node-count / lines, or the multi-version source / revs). A leading `plan`
record announces the two checks up front, and a trailing `summary` record
reports the check pass/fail counts (`total` = `plan_count` = 2); subtests do
not count toward the summary, per the schema. The document is always
`valid: true` and `bailed: false`: `lint` generates records rather than
transforming a TAP stream, so there are no parse diagnostics.

The `plan` record is normative in the tap NDJSON schema (`tap-ndjson(7)`): a
producer that knows its plan up front SHOULD emit it as the first record, and
when both a plan record and a summary are present, `plan_count` MUST equal the
plan record's `count`. `lint` always runs exactly two checks, so it emits
`{"type":"plan","count":2}` as the first line and reports `plan_count: 2` in the
summary. (The plan record was formalized via amarbel-llc/tap#30, which also
moved the whole schema from the proposed RFC 0001 into the `tap-ndjson(7)`
manpage; `lint`'s output was already aligned with the result.)

### Offline; no closure pass

A missing `flake.lock` is a hard error (exit `1`) — that is the input the
command exists to analyze. There is no closure pass, so a lint run is useful
in an unbuilt checkout with no `nix` available.

## Examples

Run against this repo's own `flake.lock`:

```
── follows opportunities ──
NixOS/nixpkgs @ d233902 pinned 2× — collapse onto "nixpkgs-master":
    inputs.nixpkgs.inputs.nixpkgs-master.follows = "nixpkgs-master"
nix-systems/default @ da67096 pinned 2× — collapse onto "nixpkgs/systems":
    inputs.utils.inputs.systems.follows = "nixpkgs/systems"
numtide/treefmt-nix @ 790751f pinned 2× — collapse onto "treefmt-nix":
    inputs.nixpkgs.inputs.treefmt-nix.follows = "treefmt-nix"

── multi-version inputs ──
(no source repository is pinned at more than one rev)
```

*Decisions served:* each follows line is a copy-pasteable edit. The canonical
target is the shorter input path (a direct root input where possible), so the
deeper, transitively-pinned copy is the one redirected.

## Implementation outline (technical detail; not user-facing)

- **Parser** (`internal/0/flakelock`): models lockfile v7 — `nodes`, `root`,
  `version`. A node's `inputs` value is polymorphic: a string is a node-table
  key (a real edge that introduces a node); an array is an already-resolved
  follows path (no node). A custom `InputRef.UnmarshalJSON` dispatches on the
  JSON token (`"` vs `[`).
- **Attr-paths** (`internal/alfa/lint`): BFS from `root` over string-form
  edges only, recording each reachable node's shortest attribute path (root =
  empty). Equal-length ties break lexically on the slash-join for determinism.
  This path generates both the follows LHS (`inputs.a.inputs.b.follows`) and
  RHS (`"a/b"`).
- **Follows recs**: group reachable pinned nodes by identity — `narHash` when
  present (content hash), else `(type, owner, repo, rev)`, else `url`. Any
  group >1 yields a rec; canonical = the `less`-minimal attr-path; every other
  member gets a `follows` line pointing at the canonical path.
- **Multi-version**: group reachable pinned nodes by `owner/repo` (url-only
  sources are excluded — their urls embed the rev, so equal urls are an
  identity match and unequal urls aren't comparable without fragile parsing).
  A group spanning >1 rev is flagged.
- **Rendering** (`internal/bravo/render`): `LintText` / `LintJSON` /
  `LintNDJSON` consume the `lint.Report`. `lintMain` picks one via `--format`
  (defaulting to `text` on a TTY, `ndjson` otherwise). The NDJSON renderer
  maps the report onto the amarbel-llc/tap test-result schema (`tap-ndjson(7)`).
- **Repair** (`internal/0/nixedit`, `--fix`): `nixedit.Apply(src, lines)`
  parses `flake.nix` with the embedded `nix.peg` (amarbel-llc/langlang runtime
  matcher, `grammar.handle_spaces` disabled for explicit CST-mode whitespace),
  walks the parse tree to the top-level `inputs` attrset, and splices the
  `follows` bindings by byte offset (block-form: inside the `inputs = { … }`
  block, `inputs.` prefix stripped; flat-form: a sibling top-level binding).
  `lintMain` then re-locks (`nix flake lock`), `git add`s the touched files,
  and re-analyzes the regenerated lock for the exit code.

## Limitations

- **Identical-source only for auto-fix.** Follows recs fire only for
  byte-identical sources (same `narHash`/`rev`). Same-repo-different-rev is
  surfaced under multi-version but never auto-collapsed, because choosing a
  revision changes behavior.
- **One line per duplicate node.** A duplicate node reached by more than one
  input edge gets a single line (its shortest path) plus a "node has multiple
  parents; repeat for each" note rather than one line per parent edge. `--fix`
  strips that note and applies the single line, so a genuinely multi-parent
  node may need a second `lint --fix` pass (the residual duplicate resurfaces
  and is fixed on the next run); `--fix`'s non-zero exit on residual findings
  signals when another pass is warranted.
- **Canonical may be transitive.** The shortest attr-path can still be a
  transitive edge (e.g. `nixpkgs/systems`), producing a slash-path follows
  target. This is valid Nix follows syntax but assumes the user is willing to
  anchor on that transitive input.
- **url-type inputs are excluded from multi-version.** See above.

## Relationship to `dupes` / version-drift (FDR-0001, issue #3)

`dupes` and its version-drift section operate on the *realized closure*
(store paths). `lint`'s follows / multi-version analyses operate on
*flake.lock* (input pins). They are complementary lenses on the same problem:
the lock is where duplication is *introduced* and *fixed*; the closure is
where its *cost* is paid. The closure-level version-drift view is exclusive to
`dupes` — `lint` deliberately stays offline and lockfile-only, and stays out of
the cache / cost-class / publisher-attribution surface that FDR-0001 specifies
for `dupes` (and which issue #1 gates).

## More Information

- Issue #4 (the original feature request); issue #9 (the `--fix` repair mode).
- Existing implementation reused: `internal/0/flakelock` (lockfile parser),
  `internal/bravo/render` (`shortRev`, `truncList`).
- `--fix` depends on amarbel-llc/langlang (`github.com/clarete/langlang/go`,
  pinned at `v0.0.12`) for the embedded Nix PEG matcher — doppelgang's first
  third-party Go dependency.
- NDJSON output conforms to the amarbel-llc/tap test-result schema,
  normatively specified in the `tap-ndjson(7)` manpage (amarbel-llc/tap;
  `docs/rfcs/0001-test-result-ndjson-schema.md` is a superseded redirect stub).
- Live fixture: this repo's `flake.lock` carries three duplicate-source node
  pairs (`nixpkgs-master`, `systems`, `treefmt-nix`) exercised by the lint
  package tests.
