---
status: exploring
date: 2026-05-03
promotion-criteria: |
  Promote to `proposed` once: (1) the output sketch in Interface has been
  validated against the user's `~/eng` closure (run a prototype and confirm
  each of the five decisions below is answerable from the output alone);
  (2) the path→flake index strategy is decided (eager batch fetch vs lazy
  per-lookup); (3) failure modes for the FlakeHub API (rate limits, private
  flakes, latest-only paths) have been characterized.
---

# `dupes` decision-driven output

## Problem Statement

`doppelgang dupes` today lists groups of duplicate store paths with their
immediate referrers. The output answers "what is duplicated?" and "who
consumes each copy?" but it does not answer the questions a human or agent
actually asks when looking at the report:

1. **Is this duplicate worth my time?** A duplicate that costs only
   storage (both copies are substituted from a binary cache) is much
   cheaper than one that forced a local rebuild — but the current
   `wasted` column treats them identically.
2. **What lever do I pull to eliminate it?** The current output names
   *consumers* of each copy. The lever lives at the *producer* side
   (flake input pin, `inputs.X.follows = "Y"`, version override).
   Translating consumers → producers is a manual exercise the reader
   has to do every time.
3. **Why does this duplicate exist at all?** Two copies of `glibc-2.40`
   with the same name and different content means something diverged
   upstream — usually two different nixpkgs revisions. The current
   output makes the reader infer this from version skew in the
   referrer lists, often hidden behind `... (54 more)` truncation.
4. **Is this duplicate a symptom of something else being broken?**
   When a copy is local-only (not on any configured cache) but its
   producer is supposed to be publishing, that's a stale-CI signal —
   and the report should say so directly.
5. **Should I fix this one or that one first?** With cost classes and
   provenance attached, the `wasted` ranking can be weighted by
   actual user cost (a forced-rebuild duplicate is worth more than a
   storage-only one) and by lever quality (a duplicate where both
   copies are attributed to flakes the user controls is fixable; one
   where the divergence is upstream-only is not).

In short: the current output is a *report*, not a *recommendation
surface*. Each row should let a human or an agent take an action
without doing additional graph reasoning by eye.

## Interface

`dupes` keeps its current shape (group-per-name, copies under each
group) and gains:

### Group header — cost class tag

Each group's header line gains a parenthetical describing aggregate
cost class:

- `(storage-only)` — every copy in the group is available on a
  configured substituter; eliminating the duplicate reclaims disk but
  no compile time.
- `(forced-rebuild)` — at least one copy is `local-only` (no
  substituter has it); next clean rebuild will pay the compile cost
  again.
- `(mixed)` — some copies cached, others not.

### Per-copy line — `cache=` and `via=` fields

Each copy line carries two structured fields:

- `cache=<status>:<which>[@<owner>/<repo>]` — substituter availability
  and (when knowable) publishing flake. Status values:
  `hit` — substitutable from the named cache.
  `miss` — not on any configured substituter.
  `auth-fail` — substituter would require authentication that
  failed; we do not know hit vs miss.
  `unreachable` — substituter did not respond.
  `@<owner>/<repo>` — present only when the publishing flake is
  identifiable. See implementation outline.
- `via=<consumer>` — kept from the existing output. The immediate
  referrer in the closure graph.

### Group-level annotations

When the tool detects patterns that *explain* a group's duplication,
it emits a single annotation line beneath the header (before the
copy list):

- `↻ shared with: <other-group-name>` — surface hidden duplicate
  chains. If a referrer name appears under more than one copy of the
  current group with different store paths, that referrer is itself
  duplicated. Link to its own group.
- `⚠ stale-publish: <owner>/<repo>` — a copy is `local-only` but
  its consumers' provenance suggests a flake the user publishes.
  Either the latest publish hasn't run, or the flakehub-publish job
  is broken. (Optional sub-cue: pair with `doppelgang ci-status` or
  similar — out of scope for v1.)
- `divergence-stem: stdenv@<short-hash>` — when copies trace back
  to different `stdenv` derivations, list the short hashes.
  Different stdenv hashes ≈ different nixpkgs.

### Flags

Existing flags unchanged. New flags:

- `--cache-status` — enable substituter queries. Off by default to
  preserve the current fast offline behavior.
- `--substituter <url>` — repeatable; overrides the substituter list
  read from `nix show-config`. Empty list = no cache lookups even if
  `--cache-status` is set.
- `--flakehub-flake <owner>/<repo>` — repeatable; flakes whose
  published `store_paths.out` should populate the path→flake index.
- `--flakehub-org <org>` — convenience: enumerate all public flakes
  under an org and use them as if each were passed via
  `--flakehub-flake`.

### Sort key

`wasted` (current) becomes one term in a composite weighted-cost
score:

```
score(group) = sum over copies of
                 narSize × cache_class_weight(copy)
```

`cache_class_weight` defaults: `local-only=1.0`, `cached=0.2`,
`auth-fail/unreachable=0.5`. Weights tunable via flags later.
Grouping that previously sorted purely by raw bytes now sorts by
"how much the user would actually save by collapsing this."

### Implementation outline (technical detail; not user-facing)

- **Hit/miss**: shell out to `nix path-info --store <url> --json
  <paths>`. Auth inherits from `nix.conf` / `determinate-nixd login`,
  so FlakeHub Cache works automatically when the user is logged in.
  The `signatures` field on local `nix path-info` already records
  which substituter delivered each path during prior fetches, so
  copies that came from FlakeHub can be tagged before any network
  call (verify whether this is reliable — locally rebuilt paths
  won't carry cache signatures).
- **Path → flake index**: `GET https://api.flakehub.com/f/<owner>/<repo>`
  is unauthenticated for public flakes and returns
  `outputs.<type>.outputs[*].outputs[*].store_paths.out` for every
  published output, keyed by `for_systems`. We confirmed the path
  reported for `amarbel-llc/doppelgang`'s `x86_64-linux` package
  matches the user's local build exactly. Index entries are
  `(store_path, system) → (owner, repo)`. Cache the raw API
  response under `~/.cache/doppelgang/flakehub/<owner>-<repo>.json`
  and respect ETag / If-None-Match.
- **Stdenv fingerprint**: walk each copy's deriver via
  `nix-store -q --requisites <drv>`, find the closest
  `stdenv-*.drv` ancestor, and tag the copy. Two copies with the
  same stdenv hash share a build environment; different stdenv
  hashes typically indicate different nixpkgs revisions.
- **Hidden-duplicate chaining**: when populating the per-copy
  parents list, also build a reverse map of `referrer-name →
  set(copy-name)` for the current run; any referrer whose name
  appears in more than one copy's set with different store paths
  is itself a duplicate. Cross-reference to its group.

## Examples

The five duplicate groups currently reported on the user's `~/eng`
closure (1623 paths, 20.6G runtime), reformatted under this design.
All `cache=` and `@owner/repo` values are illustrative — the real
values will be filled in by the prototype.

```
── openjdk-21.0.10+7   ×2   per-copy=566.8M   wasted=566.8M (storage-only)
    [#1] cache=hit:flakehub@NixOS/nixpkgs    via plantuml-1.2026.2
    [#2] cache=hit:flakehub@NixOS/nixpkgs    via google-java-format-1.32.0
```
*Decision served:* both copies are cache-substituted, so the cost is
disk only. If `(storage-only)` is too cheap to bother with, skip.
The shared publisher tells the reader these are both genuinely
"upstream nixpkgs" copies — divergence is in how plantuml and
google-java-format wrap their JDK, not in the input pin.

```
── pandoc-cli-3.7.0.2   ×3   per-copy=207.8M   wasted=415.6M (mixed)
    ↻ shared with: maneater-wrapped-2026.04.* (×2 in this run)
    [#1] cache=hit:flakehub@amarbel-llc/maneater    via …
    [#2] cache=hit:flakehub@amarbel-llc/maneater    via maneater-wrapped
    [#3] cache=miss                                  via eng
```
*Decision served:* the `↻ shared with:` line connects the dots —
the same-named `maneater-wrapped` referrer in `[#1]` and `[#2]` is
itself duplicated. Copies #1 and #2 are both attributed to the
user's own `maneater` flake (different builds), which is a real
lever. Copy #3 is local-only via `eng`, suggesting `eng` itself
evaluated `pandoc-cli` from a third nixpkgs.

```
── python3-3.13.12   ×3   per-copy=126.7M   wasted=253.5M (forced-rebuild)
    divergence-stem: stdenv@abc123, stdenv@def456, stdenv@789xyz
    [#1] cache=miss                                 via git-2.53.0, …
    [#2] cache=miss                                 via fish-4.6.0, git-2.53.0, …
    [#3] cache=miss                                 via git-2.51.2, …
```
*Decision served:* `forced-rebuild` and three distinct stdenv
fingerprints make the diagnosis explicit — three nixpkgses are
in play, none of them substituted, every copy was built locally.
This is the highest-cost group in the report regardless of raw
byte count.

```
── blas-3   ×3   per-copy=66.3M   wasted=132.6M (mixed)
    [#1] cache=hit:flakehub@NixOS/nixpkgs    via llama-cpp-8983
    [#2] cache=hit:flakehub@NixOS/nixpkgs    via llama-cpp-8864
    [#3] cache=miss                                 via llama-cpp-6981
```
*Decision served:* three different llama-cpp versions; the lever is
"pick one llama-cpp." The `cache=miss` on `[#3]` further suggests
that version is no longer published anywhere — additional reason
to drop it.

```
── nodejs-22.22.2   ×2   per-copy=89.1M   wasted=89.1M (storage-only)
    [#1] cache=hit:flakehub@NixOS/nixpkgs    via prettier-3.6.2
    [#2] cache=hit:flakehub@NixOS/nixpkgs    via zx-8.8.5
```
*Decision served:* both cached, both upstream nixpkgs. Cheap to
ignore unless storage is tight.

## Limitations

- **Latest-published only.** `api.flakehub.com/f/<owner>/<repo>`
  appears to return the most recently published outputs. A locally
  substituted output from an earlier version of a flake will show
  `cache=hit` (substituter check passes) but no `@<owner>/<repo>`
  attribution (the API doesn't list its store path anymore). The
  output should distinguish "cached, attributed" from "cached,
  unattributed" so the reader doesn't think attribution failed.
- **Public flakes only without auth.** Private flakes need a
  FlakeHub JWT for both the cache lookup and the API. Substituter
  hits inherit auth from `nix.conf` automatically; the publisher
  index would need explicit auth handling. Defer to a later
  iteration.
- **Substituter list is closure-wide, not per-copy.** We cannot
  know which substituter a path was *originally* fetched from in
  isolation — the local `signatures` field tells us, but only for
  paths that were actually substituted (locally built paths have no
  cache signature). When multiple substituters hold the same path,
  the one named first in `cache=hit:<which>` is arbitrary.
- **Stdenv-fingerprinting only works for stdenv-built derivations.**
  Bootstrap paths, fixed-output derivations, and toolchain primitives
  may have no recoverable stdenv ancestor. Those copies omit the
  divergence-stem annotation.
- **Cost weights are heuristic.** `local-only=1.0` vs `cached=0.2`
  is a starting point, not measured. Sort behavior may need tuning
  against real workloads before being trusted.
- **No build-scope cache lookup.** Cache substituters serve runtime
  closures. `--scope build` duplicates can still be reported, but
  `cache=` will frequently be `miss` for build-time-only paths even
  when the runtime artifact is cached. Document this and prefer
  `--scope runtime` for cost-class triage.

## Relationship to version-drift detection (issue #3)

Issue #3 adds a sibling lens to the `dupes` output: groups of *different
versions of the same logical derivation* coexisting in the closure
(e.g. `clang-21.1.7` alongside `clang-21.1.8`). Exact-duplicate
detection ("two copies with the same name and different hash") and
version-drift detection ("multiple versions of the same `pname`") are
distinct signals that motivate distinct levers, so version drift lands
as its own additive section beneath the existing duplicate list rather
than being folded into the per-group decision columns this FDR
specifies.

First-pass grouping is by `pname` only — split a closure-path name at
its first dash-followed-by-digit, the canonical `lib.parseDrvName`
rule. This will over-merge across output-suffixed names (`jq-1.8.1-bin`
vs `jq-1.8.1-dev` parse to the same `pname` with different versions
`1.8.1-bin` / `1.8.1-dev`) and may need an output-suffix strip pass
informed by real-closure validation. The FDR does not pre-commit to a
particular heuristic; the v1 implementation explicitly accepts some
imprecision and treats validation against `~/eng` as the gating signal.

How this FDR's design composes with the drift section, once both ship:

- **Cost class** generalizes. A drift group whose every version is
  `cache=hit` is `storage-only`; one with any `local-only` copy is
  `forced-rebuild`; the rest are `mixed`. Reuses the same vocabulary.
- **`cache=` and `@owner/repo` attribution** attach to individual
  versions within a drift group exactly as they do to copies within an
  exact-duplicate group, so the publisher lever is visible at the
  same place.
- **Overlap** between the two sections is intentional and called out
  inline. A `pname` that has both inter-version drift *and* intra-
  version duplication appears in both the exact-duplicate list (per
  version) and the drift list (across versions), with the drift entry
  annotating which versions are also exact dupes.
- **Sort key**. Drift groups carry their own weighted-cost score
  (sum of `narSize × cache_class_weight` across all versions and all
  copies of each version). The two sections sort independently.

Promotion of this FDR to `proposed` does not block #3; #3 ships first
as a standalone pname-grouped section, and this FDR's eventual
implementation extends it with the cache / cost / attribution columns
above.

## More Information

- Probe results that established cache hit/miss and publisher
  attribution feasibility (FlakeHub Cache `nix path-info --store`,
  `api.flakehub.com/f/<owner>/<repo>` exposing `store_paths.out`):
  conversation history, 2026-05-03 doppelgang session.
- Related FlakeHub guidance learned in `~/eng` session
  `a00e6cca-9deb-46d9-bd6a-641c30e808d4` (2026-05-03): both
  `cache.nixos.org` and `cache.flakehub.com` serve runtime closures
  only; build intermediates land in the local store only when the
  derivation is built locally.
- Existing implementation: `internal/alfa/dupes/dupes.go` (group
  building, parent inversion), `internal/alfa/attribute/owners.go`
  (closure-top owner walk), `internal/0/closure/load.go` (build vs
  runtime closure loading). The new column logic plugs into the
  existing `Group` / `Copy` data model — see Implementation Outline.
