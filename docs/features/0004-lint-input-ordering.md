---
status: exploring
date: 2026-06-28
promotion-criteria: |
  Promote to `proposed` once: (1) the comment-association rule is validated
  against `eng/flake.nix` and 2–3 other hand-sorted flakes — confirm each
  input's leading comment block and its `inputs.*.follows` lines move with it
  and nothing is orphaned or duplicated; (2) the sort codemod's output is
  confirmed nixfmt/treefmt-stable (running `nix fmt` immediately after a sort is
  a no-op); (3) the region policy is settled — confirm the `# keep sorted`
  sentinel approach (opt-in, sorts only the marked region) is preferred over
  sorting the whole `inputs` block, and that non-amarbel/ungrouped inputs are
  correctly left alone.
---

# `lint` — flake input ordering (check + sort codemod)

## Problem Statement

`eng/flake.nix` (and peer flakes) keep their `inputs` block alphabetically
sorted by hand under a `# keep sorted` comment. The ordering is enforced only by
reviewer discipline — a new input is slotted into the right place by eye (this
came up adding a `nix-cache` input in `amarbel-llc/eng#198`). Nothing flags an
out-of-order input, and nothing reorders them mechanically while preserving each
input's documentation comment and its `inputs.*.follows` overrides. `nix fmt` /
nixfmt format the block but never reorder it, so this is a genuine gap.

## Interface

Proposed as a fourth `lint` check plus a `--fix` codemod, reusing the existing
`nixedit` surgery, the multi-category output, and the CI-gate exit code:

```
doppelgang lint … [--fix]
```

- **New check: input ordering.** Within an opt-in **`# keep sorted` region** of
  the top-level `inputs` (block or flat form), flag inputs whose declaration
  order is not alphabetical by input name. Reported as a fourth finding category
  in `text` / `json` / `ndjson` (plan count would rise 3 → 4), each finding
  naming the misplaced input and where it should go.
- **`--fix` reorders.** Rewrite the sentinel region so its inputs are
  alphabetical, moving each input's whole *chunk* — its leading comment/blank
  lines plus every binding whose first attr segment is that input name
  (`X.url`, `X.inputs.*.follows`, a nested `X = { … }`) — as one unit. Re-stage
  like the other `--fix` repairs. Offline (no `nix` needed for the reorder
  itself, unlike the dead-override re-lock).

### Why opt-in via `# keep sorted`

Sorting is a style choice, not a correctness one — unlike the follows /
multi-version / dead-override checks, which flag real graph defects. Gating on a
`# keep sorted` sentinel means the check fires only where a maintainer has
opted in, so doppelgang never imposes an ordering on a flake that doesn't want
one. It also cleanly answers two of #12's open questions: the **sort region** is
exactly the sentinel-delimited span, and the **ungrouped third-party block**
above the sentinel is out of scope by construction.

### Interface alternative considered

A dedicated `doppelgang sort-inputs` (or a treefmt plugin) instead of a `lint`
check. Rejected for v1: ordering hygiene fits `lint`'s existing check + `--fix`
+ CI-gate model and reuses the `nixedit` machinery, and the sentinel keeps it
from diluting `lint`'s focus. Revisit if the thematic mismatch (lint = graph
defects vs. ordering = style) proves confusing in practice.

## Examples

```
  inputs = {
    nixpkgs-master.url = "github:NixOS/nixpkgs/…";  # ungrouped, above sentinel
    # keep sorted
    utils.url = "…";
    # the fork; auto-applies its overlay
    igloo.url = "github:amarbel-llc/igloo";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
```

`lint` flags `utils` as out of order (should follow `igloo`); `--fix` rewrites
the region:

```
  inputs = {
    nixpkgs-master.url = "github:NixOS/nixpkgs/…";  # ungrouped, above sentinel
    # keep sorted
    # the fork; auto-applies its overlay
    igloo.url = "github:amarbel-llc/igloo";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    utils.url = "…";
  };
```

*Decisions served:* `igloo`'s documentation comment and its `follows` line move
with it as one chunk; the ungrouped `nixpkgs-master` above the sentinel is left
untouched.

## Implementation outline (technical detail; not user-facing)

- **Region location (`internal/0/nixedit`).** Find the `# keep sorted` sentinel
  comment inside the top-level `inputs` attrset (or a flat-form equivalent) and
  the byte span from the line after it to the region's end (the inputs block's
  closing brace, or the last flat `inputs.*` binding). The existing grammar
  already locates the `inputs` attrset; the sentinel is found textually within
  it (the inner content is opaque text today — same constraint `scanBlockKeys`
  works under).
- **Chunking.** Segment the region into per-input chunks at line granularity: a
  chunk is the run of leading comment/blank lines plus every consecutive binding
  whose first attr-path segment is the same input name. Requires each input's
  bindings to be **contiguous** within the region; a non-contiguous input
  (bindings interleaved with another's) is reported as unsortable and `--fix`
  leaves the region alone rather than risk reassociating a comment. Reordering
  is a stable sort of chunks by input name, re-emitted verbatim — byte-preserving
  within each chunk, so nixfmt/treefmt see no change beyond the reorder.
- **Comment association.** A comment/blank run immediately preceding an input's
  first binding (back to the previous chunk's last line) belongs to that input.
  A trailing comment on a binding's own line travels with that binding. A
  blank-line-separated comment block with no following binding before the
  region end is ambiguous — treat as a region-trailing chunk and leave in place.
- **Analysis + render (`internal/alfa/lint`, `internal/bravo/render`).** A new
  `OutOfOrder` finding type; a fourth `test` record in NDJSON (plan/plan_count
  3 → 4). `--fix` applies the reorder through a new `nixedit` reorder primitive.

## Limitations

- **Opt-in only.** No `# keep sorted` sentinel ⇒ the check is a no-op. Sorting
  the whole `inputs` block unconditionally is explicitly out of scope (it would
  impose ordering on flakes that group inputs deliberately).
- **Contiguity required.** An input whose bindings are scattered through the
  region is reported but not auto-reordered — reassociating a stray
  `X.inputs.y.follows` line three inputs away is too risky to mechanize.
- **Single-level only.** Sorts the top-level `inputs` of the linted flake; does
  not recurse into nested `X = { inputs = { … }; }` sub-attrsets.
- **NDJSON plan count 3 → 4.** Another documented contract bump for any consumer
  counting checks (compounds the 2 → 3 change from FDR-0003).
- **Comment heuristics are best-effort.** Comment-to-input association is a line
  heuristic, not semantic; pathological layouts (a comment documenting two
  inputs, inline block comments mid-binding) may associate surprisingly. The
  contiguity + sentinel guards bound the blast radius, and `--fix` bails rather
  than guess when the region doesn't cleanly chunk.

## Relationship to FDR-0002 / FDR-0003

Shares the `nixedit` PEG-surgery substrate and the `lint` check + `--fix` +
NDJSON model with the follows/multi-version (FDR-0002) and dead-override
(FDR-0003) work. The key difference: those checks flag **graph defects**
(duplication, dead pins) and fire unconditionally; this one flags a **style**
deviation and is opt-in via the sentinel. It also needs a new `nixedit`
*reorder* primitive — a coarser, line-granular chunk move, distinct from the
existing line splice (`Apply`) and line excision (`DeleteBindings`).

## More Information

- Issue #12 (the feature request) and its open questions (comment association,
  sort region, treefmt interaction, ungrouped-block scope), each addressed
  above.
- `amarbel-llc/eng#198` — the `nix-cache` input addition that motivated it.
- Existing substrate to extend: `internal/0/nixedit` (grammar, `Apply`,
  `DeleteBindings`, `Overrides`), `internal/alfa/lint` (finding types,
  `Analyze`), `internal/bravo/render` (the three formatters and the NDJSON
  plan/summary).
