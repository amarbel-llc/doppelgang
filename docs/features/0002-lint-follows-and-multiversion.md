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
doppelgang lint [--flake .] [--format auto|text|json|ndjson]
```

- `--flake` (default `.`) — directory containing `flake.lock`. Read offline;
  no `nix` invocation.
- `--format` (default `auto`) — output format. `text` is the bordered
  human-readable view; `ndjson` is the amarbel-llc/tap test-result NDJSON
  schema; `json` is a single indented JSON document. `auto` emits `text` when
  stdout is a TTY and `ndjson` otherwise, so piped/redirected output is
  machine-readable without a flag.

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

The `plan` record is a deliberate extension beyond tap RFC 0001 (which defines
only `test`/`bailout`/`summary` and forbids other record types). `lint` emits
`{"type":"plan","count":2}` as the first line, treating its fixed two checks as
a `1..2` plan; this is tracked upstream by amarbel-llc/tap#30, and `lint` will
reconcile the record shape with whatever that issue formalizes. Because the
schema's compatibility rules require consumers to ignore unrecognized record
types, the extension is backward-safe for existing consumers.

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
  maps the report onto the amarbel-llc/tap test-result schema (RFC 0001).

## Limitations

- **Identical-source only for auto-fix.** Follows recs fire only for
  byte-identical sources (same `narHash`/`rev`). Same-repo-different-rev is
  surfaced under multi-version but never auto-collapsed, because choosing a
  revision changes behavior.
- **One line per duplicate node.** A duplicate node reached by more than one
  input edge gets a single line (its shortest path) plus a "node has multiple
  parents; repeat for each" note rather than one line per parent edge.
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

- Issue #4 (this feature request).
- Existing implementation reused: `internal/0/flakelock` (lockfile parser),
  `internal/bravo/render` (`shortRev`, `truncList`).
- NDJSON output conforms to the amarbel-llc/tap test-result schema
  (`docs/rfcs/0001-test-result-ndjson-schema.md` in that repo).
- Live fixture: this repo's `flake.lock` carries three duplicate-source node
  pairs (`nixpkgs-master`, `systems`, `treefmt-nix`) exercised by the lint
  package tests.
