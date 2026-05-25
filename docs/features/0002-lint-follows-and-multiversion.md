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

It additionally reuses the existing `dupes` version-drift pass over the
realized closure so a single `lint` run shows both the lockfile-level and the
closure-level views of the same underlying problem.

## Interface

```
doppelgang lint [--flake .] [--installable ./result] [--scope runtime|build]
                [--no-closure] [--json]
```

- `--flake` (default `.`) — directory containing `flake.lock`. Read offline;
  no `nix` invocation for the follows / multi-version analyses.
- `--installable` (default `./result`), `--scope` (default `runtime`) — the
  installable whose realized closure feeds the reused version-drift pass.
- `--no-closure` — skip the closure pass entirely (pure offline lockfile lint).
- `--json` — emit structured JSON instead of text.

### Output sections

- `── follows opportunities ──` — one block per identical-source group:
  a header naming the shared source and how many times it is pinned, followed
  by the concrete `follows` line(s) to add.
- `── multi-version inputs ──` — one line per `owner/repo` pinned at multiple
  revs, listing each short rev and a sample attr-path that reaches it.
- `── Version drift ──` — the existing `dupes` closure section, appended
  verbatim unless `--no-closure` (or the installable can't be resolved).

Each section prints a positive "nothing found" line when empty so the reader
knows the pass ran.

### Best-effort closure pass

A lint run must stay useful in an unbuilt checkout. If the installable cannot
be resolved (no `./result`, `nix` absent, eval failure), the closure pass is
skipped with a one-line stderr warning and the command still exits `0` with
its lockfile findings. A missing `flake.lock`, by contrast, is a hard error
(exit `1`) — that is the input the command exists to analyze.

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
- **Closure reuse**: `lintMain` calls `closure.Load` → `dupes.InvertReferences`
  → `dupes.FindVersionDrift(g, parents, nil)` and renders it via the existing
  `render.writeDrift`.

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
- **Closure pass needs `nix` and a built installable.** Without them the
  version-drift section is skipped (by design).

## Relationship to `dupes` / version-drift (FDR-0001, issue #3)

`dupes` and its version-drift section operate on the *realized closure*
(store paths). `lint`'s follows / multi-version analyses operate on
*flake.lock* (input pins). They are complementary lenses on the same problem:
the lock is where duplication is *introduced* and *fixed*; the closure is
where its *cost* is paid. `lint` reuses `dupes.FindVersionDrift` rather than
reimplementing closure analysis, and deliberately stays out of the cache /
cost-class / publisher-attribution surface that FDR-0001 specifies for
`dupes` (and which issue #1 gates).

## More Information

- Issue #4 (this feature request).
- Existing implementation reused: `internal/0/closure/load.go`,
  `internal/alfa/dupes/dupes.go` (`InvertReferences`, `FindVersionDrift`),
  `internal/bravo/render` (`writeDrift`, `human`, `truncList`).
- Live fixture: this repo's `flake.lock` carries three duplicate-source node
  pairs (`nixpkgs-master`, `systems`, `treefmt-nix`) exercised by the lint
  package tests.
