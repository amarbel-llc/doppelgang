---
status: exploring
date: 2026-07-21
promotion-criteria: |
  Promote to `proposed` once: (1) the location-preserving splice (tier 1)
  has been exercised on a real repair — a follows edit landing inside an
  existing nested sub-block's chunk rather than at the inputs block's tail
  — and confirmed readable; (2) the canonical-form check + `--fix` (tier 2)
  is confirmed to reach a fixed point (a canonical fixture round-trips
  through `--fix` unchanged) in the table-driven test suite; (3) at least
  one non-eng flake in the fleet opts in via `# canonical-form` and its
  `--fix` output is confirmed `nix fmt`-stable; (4) eng's own opt-in
  (eng#52) is scoped as a separate follow-up, not a precondition of this
  FDR landing.
---

# `nixedit`/`lint` — canonical form for the flake `inputs` block

## Problem

Two related gaps in nixedit/lint's handling of a flake's top-level `inputs`
attribute set:

1. **Location-preserving splice (bug).** `nixedit.Apply`'s follows-splice
   inserted every new binding at a single fixed offset (just before the
   `inputs` block's closing brace), regardless of which input the binding
   targeted. When a repair to a nested sub-block (e.g. pruning a dead
   override from `dodder = { inputs.treelint.follows = "conformist"; }`
   after a treelint→conformist rename) also required adding a replacement
   follows for `dodder`, the replacement landed at the bottom of the whole
   `inputs` block — divorced from the `dodder = { }` chunk and its leading
   comment. The result was correct but unreadable: related edits no longer
   read as related.

2. **No canonical layout.** Nothing enforces (or can enforce) that an
   `inputs` block is laid out consistently: one chunk per input (leading
   comment, `url`, `follows`/`overrides`, nested sub-attrset if present),
   contiguous and in a stable order. Flakes accrete follows lines wherever
   a previous `--fix` run happened to place them, so two flakes with
   identical logical inputs can look arbitrarily different on disk, and a
   single flake can drift further from readable with every repair.

This FDR covers both: a bug fix (tier 1, must ship) and a check + codemod
that produces and enforces a canonical layout (tier 2, opt-in).

## Decisions

These were supplied by the coordinating session for doppelgang#24 and are
recorded here rather than re-derived:

- **Opt-in per flake, via a structured directive comment.** Following the
  FDR-0004 (`# keep sorted`) precedent, canonical-form enforcement is
  opt-in per flake via a marker comment placed on its own line immediately
  above the `inputs = { ... }` (or `inputs.` flat block) binding. The
  shipped spelling is **`# doppelgang: canonical`** — a structured,
  namespaced directive (`# doppelgang: <name>`) rather than a bespoke
  string per check, so a future check (e.g. an eventual FDR-0004
  implementation) can reuse the same `# doppelgang: <name>` convention
  instead of inventing its own. This was a refinement made mid-session,
  after initial implementation shipped with a bespoke `# canonical-form`
  sentinel; that spelling is kept working as a deprecated alias (see
  "Legacy sentinel migration" below) rather than being a breaking change.
  Absent either spelling, the check reports nothing for layout and `--fix`
  does not re-shape the block — only the tier-1 location-preserving
  behavior (which is not a layout-imposition, just a smarter insertion
  point) applies unconditionally. Third-party flakes are never re-shaped
  since they cannot carry the directive; eng's own `flake.nix` opts in
  later, tracked by eng#52 — this change does not touch eng.
- **Nested vs. flat spelling is a per-chunk rule, not a block-wide one.**
  An input already expressed as a nested sub-attrset (`X = { url = ...;
  inputs.Y.follows = "Z"; }`) keeps that nested spelling; an input with no
  existing nested block is written flat (`inputs.X.url = ...`;
  `inputs.X.inputs.Y.follows = "Z"`). Canonicalization never converts one
  spelling to the other — it only reorders and regroups chunks that already
  use whichever spelling they use.
- **Scope: a single top-level `inputs` block.** No handling of multiple
  `inputs` attrsets, `let`-bound inputs, or non-top-level flakes (e.g.
  flake-parts submodules).
- **Layering.** Canonical-form chunk placement is a shared primitive:
  `Apply`'s location-preserving splice (tier 1) and the canonical-form
  `--fix` codemod (tier 2) both resolve "where does input X's next binding
  go" through the same chunk-offset machinery in nixedit. The other
  checks' `--fix` paths (follows, dead-overrides) already produce edits
  through `Apply`/`DeleteBindings`, so they inherit chunk placement for
  free once `# canonical-form` is present — no separate wiring needed.

## Tier 1: location-preserving splice

Implemented in `internal/0/nixedit`:

- `walk.go`: `findInputsAttrSet` and its block-mode helper `blockInsert`
  now build a `chunkEnd map[string]int` — for every input name already
  bound under `inputs`, the byte offset in `src` immediately after the
  semicolon of that input's *last* binding. Flat mode computes this
  directly while walking top-level `inputs.*` bindings
  (`afterSemicolon(src, ...)`); block mode's `blockChunkOffsets` re-parses
  the `{ ... }` group's text in isolation (a fresh `matcher.Match` call)
  and maps each binding back to an absolute offset via the group's base
  cursor.
- `nixedit.go`: `Apply` now groups every follows line to splice by
  *destination offset* rather than always targeting the block's single
  insertion point. `destOf` resolves a line's destination to
  `chunkEnd[input]` when that input already has a chunk, falling back to
  the global `insertOffset` otherwise. Groups are applied in ascending
  offset order in one left-to-right pass over `src`, so multiple splices
  never invalidate each other's offsets.
- A langlang-specific hazard drove the ordering inside `blockInsert`:
  `matcher.Match()` is stateful and invalidates node IDs from the
  *previous* call. Since `blockChunkOffsets` re-parses the group text (a
  second `Match` call), all navigation of the *outer* tree must complete
  and have its results (`groupBase`, `groupText`, `braceIndent`, etc.)
  extracted into plain values *before* `blockChunkOffsets` runs. The outer
  `tree` handle must not be touched afterward.

Tests: `TestApplyLocationPreservingBlockChunk`,
`TestApplyLocationPreservingFlatChunk`,
`TestApplyLocationPreservingNestedBlock` (the motivating dodder case),
`TestApplyMultipleInputsLocationPreserving`, all in
`internal/0/nixedit/nixedit_test.go`.

This tier is unconditional — it changes where a binding is spliced, not
whether the block as a whole gets re-laid-out, so it applies regardless of
the `# canonical-form` sentinel and needs no opt-in.

## Tier 2: canonical form check + codemod

Tier 2 landed narrower than the full canonical-form vision below, scoped to
what could be implemented cleanly on top of the existing primitives — per
the working decision to "implement as far as cleanly possible." The full
definition is recorded first as the target; the "Shipped" and "Deferred"
subsections describe what this change actually does.

### Canonical form, full definition (target)

Given an `inputs` block that opts in via `# canonical-form`:

1. **Chunking.** Each input's leading comment (if any, contiguous
   comment lines immediately above its first binding), `url` binding,
   `follows`/`overrides` bindings, and nested sub-attrset (if the input
   uses nested spelling) form one chunk. A chunk's bindings are
   contiguous in the source — no interleaving with another input's
   bindings.
2. **Follows placement.** A follows binding for input `X` is placed
   inside `X`'s own chunk, not wherever it was last written — including,
   for a nested-spelling input, *inside* its `X = { … }` sub-attrset
   rather than merely adjacent to it.
3. **Form consistency.** Within a chunk, every binding for that input
   uses the same spelling (flat or nested) as the chunk's existing form
   (see Decisions — canonicalization does not convert spelling).
4. **Chunk ordering.** Chunks are ordered alphabetically by input name,
   scoped to the same opt-in region FDR-0004 envisions (i.e.
   canonical-form ordering would compose with, not replace, `# keep
   sorted`).

### Shipped in this change

- **Check.** New `internal/alfa/lint` check, `CheckCanonicalForm =
  "canonical-form"`, added to `AllChecks` (opt-in like `nixpkgs-master`
  and `canonical-inputs` — not in `DefaultChecks`). `nixedit.CanonicalForm`
  detects the opt-in directive (a comment reading exactly `# doppelgang:
  canonical`, or the deprecated `# canonical-form` spelling, alone on the
  line immediately above the `inputs` binding); a flake with neither
  produces no findings at all (not even "you should opt in" — silence,
  matching the `# keep sorted` precedent). For an opted-in flake, it walks
  the direct bindings under `inputs` (or, in block form, under the `inputs
  = { … }` group) in file order and flags any input name whose occurrences
  are not *contiguous* — i.e. some other input's binding appears between
  two of its own. This is definition (1)'s core invariant (no
  interleaving) without the comment-attribution refinement.
- **Legacy sentinel migration.** A flake opted in via the deprecated
  `# canonical-form` spelling is still recognized (`CanonicalFormReport.
  LegacySentinel = true`) — the check surfaces this as a finding even when
  nothing is scattered, since there's something actionable — and `--fix`
  rewrites the comment line in place to `# doppelgang: canonical`
  (`nixedit.MigrateLegacySentinel`), independent of whether there was any
  scattering to relocate. No flake is left permanently unable to reach the
  fixed point solely because it used the old spelling.
- **`--fix`.** `nixedit.CanonicalFormFixTargets` identifies, among the
  scattered inputs, which of their bindings are themselves
  follows/override bindings (the only kind `Apply`/`DeleteBindings` can
  relocate); `lintFix` deletes those via the existing `DeleteBindings`
  and re-splices them via the existing tier-1 location-preserving
  `Apply`, which lands each one adjacent to its target input's remaining
  bindings. No new byte-surgery primitive was needed — tier 2's fix is
  entirely a composition of tier 1 + the existing dead-overrides
  machinery.
- **Fixed-point acceptance test.** `TestCanonicalFormFixNoopWhenAlreadyCanonical`
  (`internal/0/nixedit/canonical_test.go`) constructs an opted-in,
  already-contiguous fixture, runs the fix pipeline once, and asserts
  byte-for-byte equality with the input. `TestCanonicalFormFixReachesFixedPoint`
  drives a scattered fixture through the fix pipeline once, confirms it
  is contiguous afterward, and confirms a second `CanonicalFormFixTargets`
  pass finds nothing left to change.
- **nixfmt-stability, at scale.** Promotion criterion (3) below flagged a
  real gap (doppelgang#27): `--fix`'s output was syntactically correct but
  not nixfmt-stable on a real-world-scale flake (eng's 31-input block,
  mixed nested/flat chunks, long comments) — `deleteSpan`'s own-line
  deletion left blank-line scars nixfmt would still collapse on its next
  run. Fixed and verified directly against the real nixfmt-rfc-style
  binary (not just inferred); `TestCanonicalFormFixNixfmtStableAtRealWorldScale`
  now covers this at the scale the earlier fixtures were too thin to catch
  it at. This resolves the code-level blocker criterion (3) names, but the
  criterion itself — an actual non-eng fleet flake opting in and being
  confirmed stable — remains a separate, unmet operational step.

### Deferred (not implemented in this change)

- **Nested-interior placement.** A scattered follows for a
  nested-spelling input is relocated adjacent to its `X = { … }` block
  (contiguous, satisfying the check), not spliced *inside* the block's
  braces. Reaching full definition (2) for nested inputs requires a new
  byte-surgery primitive (splice inside an already-located sub-attrset),
  which this change does not add.
- **Comment attribution.** Chunk contiguity is judged on bindings only;
  a comment immediately preceding an input's binding is not tracked or
  moved with it.
- **Form-consistency enforcement.** Never actively violated by this
  change's `--fix` (it only relocates existing follows text verbatim,
  never rewriting flat↔nested), but there is no check that flags a
  pre-existing form inconsistency independent of scattering.
- **Chunk ordering.** No alphabetical reordering pass exists; chunk
  *order* is whatever the file already has, and only *contiguity* is
  enforced.

A follow-up issue should track completing definitions (1)–(4) once this
narrower check has been exercised on a real flake (see promotion
criteria).

## Relationship to prior FDRs

- FDR-0002/0003 established the follows/dead-overrides `--fix` paths this
  reuses.
- FDR-0004 designed the `# keep sorted` opt-in sentinel pattern and the
  ordering region canonical-form's (deferred) chunk ordering would compose
  with. As of this change FDR-0004 is design-only (status `exploring`, no
  code yet) — canonical-form's sentinel follows its precedent but does not
  depend on any FDR-0004 code existing.
- FDR-0005/0006 are unrelated checks (nixpkgs-master convention,
  canonical-inputs URL) that happen to share the `internal/alfa/lint`
  Check/Selection scaffolding extended here.

## eng#52 (input to)

The directive an eng-side consumer opts in with is **`# doppelgang:
canonical`**, placed on the line immediately above the `inputs` binding it
governs. (The deprecated `# canonical-form` sentinel also still works, and
`--fix` migrates it — but a new adopter should use the structured form
directly.) eng itself does not opt in as part of this change — that is
deferred, per the working decision above, to a follow-up once this ships and
the codemod has been exercised on other flakes first.
