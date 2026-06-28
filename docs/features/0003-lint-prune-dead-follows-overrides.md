---
status: exploring
date: 2026-06-28
promotion-criteria: |
  Promote to `proposed` once: (1) the detection model below is validated
  against a real flake carrying a *direct* dead override (e.g. the
  `amarbel-llc/eng` `inputs.nebulous.inputs.chrest.follows` case recovered
  from git history before its by-hand fix) — confirm the dead override is
  flagged and that no false positive fires on a healthy flake; (2) the
  `nixedit` work is scoped — confirm it can both *extract* a nested override
  attr-path (`bats = { inputs.nixpkgs.follows = …; }`, which the current
  grammar parses opaquely) and *delete* an override line cleanly (the current
  grammar only splices lines in), with the re-locked flake evaluating
  warning-free; (3) the best-effort online transitive path behaves correctly —
  it attempts the upstream `flake.nix` fetch when running impurely and
  regresses to a silent no-op (no error, no spurious finding) when the fetch
  is unreachable or `lint` is offline.
---

# `lint` — pruning dead follows overrides

## Problem Statement

A `follows` override that points at an input the dependency has since renamed
or dropped is *dead*: it overrides nothing. Nix surfaces it as an eval warning
(`input 'nebulous' has an override for a non-existent input 'chrest'`), and
across a fleet these accrue continuously every time an upstream input set
changes. `lint --fix` already does the *inverse* repair — it **adds** `follows`
lines to collapse byte-identical duplication (FDR-0002, issues #9/#7/#8). It
cannot yet **remove** dead overrides. Issue #11 documents the symptom and
proposes only a manual fix; #13 asks `lint` to detect dead overrides and `--fix`
to prune them, the symmetric capability.

## Detection model — the load-bearing finding

The issue's premise is that a dead override is detectable from `flake.lock`
("the same condition Nix warns on … the offline `flake.lock`-only analysis
path"). **That premise is empirically false, and it reshapes the whole
design.** A dead override is *not recorded in `flake.lock` at all.* Nix resolves
the lock to each node's *real* declared inputs and silently drops any override
whose target does not exist, emitting only the eval-time warning (which it
derives by re-reading `flake.nix`, not from the lock).

Evidence (live, 2026-06-28): `amarbel-llc/tacky` declares
`inputs.bats.inputs.nixpkgs.follows = "nixpkgs"`, but upstream
`amarbel-llc/bats` dropped its `nixpkgs` input (it now declares `igloo` /
`nixpkgs-master` / `utils` / `treefmt-nix`). The `bats` node in tacky's own
`flake.lock` is:

```json
"bats": { "inputs": {
  "igloo": "igloo",
  "nixpkgs-master": ["nixpkgs-master"],
  "treefmt-nix": "treefmt-nix_2",
  "utils": ["utils"]
}}
```

There is no `nixpkgs` key. This is a *direct* override on a *direct* dependency,
and it still vanished from the lock. So detection cannot read the dead override
out of the lock — it must parse `flake.nix`:

- **Where the dead override lives:** in some flake's `flake.nix`, as a binding
  `inputs.<dep>…inputs.<x>.follows = …`. The overridden input is `<x>`, and it
  attaches to the node reached by walking the override's attr-path prefix
  (`<dep>…`) through the input graph.
- **How to tell it is dead:** the lock *does* still record, for every node, its
  complete set of real declared inputs — the keys of that node's `inputs` map
  (string-form edges and legitimate array-form follows alike). So the override
  is dead iff `<x>` is absent from the keys of the node its prefix resolves to.

This splits the feature cleanly along *where the override line lives*:

| Case | Override line lives in | Detectable offline? | Fixable here? |
|---|---|---|---|
| **Direct** | the linted `flake.nix` | Yes — parse this `flake.nix` + cross-check against the dep node's inputs in this `flake.lock` | Yes — delete the line, re-lock |
| **Transitive** | an *upstream* `flake.nix` (e.g. tacky's, when linting a flake that consumes tacky) | **No** — the line is absent from the linted `flake.nix` *and* from the lock; only a fetch of the upstream `flake.nix` recovers it | No — fix lands upstream |

The issue's "transitive cases are report-only" framing understated the gap:
transitive dead overrides are not merely unfixable here, they are not
*detectable* here without fetching every upstream `flake.nix`. Plain `lint` is
offline, so transitive detection rides the *impure* path (the same one `--fix`
already needs `nix` for): it attempts the upstream `flake.nix` fetch and
regresses to a **silent no-op** — no error, no finding — whenever the fetch is
unreachable or `lint` is run offline. Transitive findings are therefore
best-effort: present when the upstream flake is reachable, silently absent
otherwise. They remain report-only either way (the fix lands upstream).

## Interface

```
doppelgang lint [--flake .] [--format auto|text|json|ndjson] [--fix]
```

No new flags. The feature adds a third finding category and extends `--fix`:

- **New finding category: dead follows overrides.** Surfaced alongside
  *follows opportunities* and *multi-version inputs* in all three formats. Each
  finding names the offending `inputs.<dep>…inputs.<x>.follows` binding, the
  node `<x>` was supposed to attach to, and whether it is direct (fixable here)
  or transitive (report-only).
- **`--fix` prunes direct dead overrides.** For dead overrides that live in the
  linted `flake.nix`, `--fix` deletes the offending binding via `nixedit` PEG
  surgery, re-locks (`nix flake lock`), and self-stages `flake.nix` / `flake.lock`
  — mirroring how `--fix` currently splices follows lines *in*. Idempotent: a
  no-op when there are no direct dead overrides. Transitive dead overrides stay
  report-only and keep `lint` exiting non-zero (the fix must land upstream).
- **Plain `lint` now reads `flake.nix`.** Detecting direct dead overrides
  requires parsing the linted `flake.nix` for its override bindings — something
  plain `lint` did not do before (it was lock-only). This stays fully offline
  (file parse via the existing embedded grammar; no `nix` invocation); only
  `--fix`'s re-lock and any transitive fetch need `nix` / network.
- **Exit code.** Unchanged in spirit: non-zero whenever any actionable finding
  (follows opportunity, multi-version input, or dead override) remains. After
  `--fix`, exit `0` only when no findings of any category remain — so a residual
  transitive dead override keeps the exit non-zero.

### Output sections (`--format text`)

A third block joins the existing two:

- `── dead follows overrides ──` — one line per dead override: the binding, the
  node it failed to attach to, and a `(direct)` / `(transitive)` tag. Prints a
  positive "nothing found" line when empty, like the other sections.

### NDJSON mapping (`--format ndjson`)

The dead-overrides check becomes a third top-level `test` record, so the plan
count rises from **2 to 3**. The leading `plan` record and the trailing
`summary`'s `plan_count` both become `3`; each dead override is a nested subtest
carrying a structured `diagnostic` (the binding, the resolved node, the
direct/transitive tag). This is an observable change to the NDJSON contract —
see Limitations.

## Examples

Linting a flake with a *direct* dead override (the override is in this
`flake.nix`):

```
── follows opportunities ──
(no input pins an identical source more than once)

── multi-version inputs ──
(no source repository is pinned at more than one rev)

── dead follows overrides ──
inputs.nebulous.inputs.chrest.follows: 'nebulous' has no input 'chrest' (direct)
```

```
$ doppelgang lint --fix
doppelgang lint --fix: pruned 1 dead follows override from ./flake.nix:
    inputs.nebulous.inputs.chrest.follows = "chrest"
… nix flake lock …
```

*Decision served:* the dead line is named and removed; the re-locked flake
evaluates without the `non-existent input` warning.

Linting a flake that *consumes* tacky, where the dead override lives upstream
in tacky's `flake.nix` (`inputs.bats.inputs.nixpkgs.follows`):

```
── dead follows overrides ──
tacky → inputs.bats.inputs.nixpkgs.follows: 'bats' has no input 'nixpkgs' (transitive; fix in tacky)
```

*Decision served:* the reader is pointed at the upstream flake to fix, and
`lint` exits non-zero rather than implying the tree is clean. (Surfacing this at
all requires the online transitive path; offline, this finding does not appear.)

## Implementation outline (technical detail; not user-facing)

- **Override extraction (`internal/0/nixedit`, new).** Detection needs the
  override bindings of the linted `flake.nix`, i.e. attr-paths of the form
  `inputs.<dep>.…inputs.<x>.follows`. The current grammar parses *flat-form*
  bindings' attr-paths structurally (`attrPathSegments`) but treats binding
  *values* opaquely, so a *nested block* override
  (`bats = { inputs.nixpkgs.follows = …; }` inside `inputs = { … }`) is not
  extracted — `scanBlockKeys` only sees the outer `bats` key. Both forms occur
  in the wild (this repo and `eng` use flat form; tacky uses nested block), so
  the grammar / walker must learn to descend nested attrsets enough to recover
  the full override attr-path.
- **Dead-override analysis (`internal/alfa/lint`).** Given the override
  attr-paths from `flake.nix` and the parsed lock: for each override, walk its
  prefix through the lock's input graph from root to the node it targets, then
  check the overridden input name against the keys of that node's `inputs` map.
  Absent ⟹ dead. Tag direct (prefix is a binding in *this* `flake.nix`) vs
  transitive. Emit a new `DeadOverride` finding type on `lint.Report`.
- **Deletion surgery (`internal/0/nixedit`, new).** `--fix` needs to *remove* a
  binding, the inverse of today's `Apply` splice. Locate the binding by attr-path
  and excise its byte span (binding through terminating `;`, plus its line's
  leading indentation and trailing newline), preserving the rest of the file.
  Unparseable / not-locatable ⟹ fall back to print-only, exactly as the splice
  path does today.
- **Rendering (`internal/bravo/render`).** Add the dead-overrides block to
  `LintText` / `LintJSON`, and a third `test` record (and `plan`/`plan_count` =
  3) to `LintNDJSON`.
- **`lintFix` (`cmd/doppelgang`).** After the existing follows-splice pass, run
  the dead-override deletion pass on the same `flake.nix`, then the single
  re-lock + self-stage already in place. Re-analyze for the honest exit code.
- **Transitive (online, best-effort).** Recover an upstream override line by
  fetching that flake's `flake.nix` (its rev is in the lock, so it is
  addressable). This is the only part needing network. It is report-only and
  best-effort: attempt the fetch on an impure run, and on *any* failure
  (unreachable, offline, fetch error) regress to a silent no-op rather than
  erroring — the run proceeds exactly as if no transitive finding existed. Not
  deferred; the graceful-degradation contract is what keeps it cheap to ship.

## Limitations

- **Transitive dead overrides are best-effort only.** They live in an upstream
  `flake.nix` that the linted flake neither contains nor records in its lock, so
  surfacing them requires fetching each upstream `flake.nix` — an online,
  report-only path. The impure run attempts this and regresses to a silent
  no-op when unreachable or offline, so a transitive dead override can go
  unreported (and the exit code stay `0`) on an offline or network-restricted
  run. Plain offline `lint` never sees them. This is a deliberate trade: a
  missed transitive finding is preferable to turning every offline run into an
  error.
- **Plain `lint` gains a `flake.nix` read.** Detecting *direct* dead overrides
  means parsing the linted `flake.nix`, which lock-only `lint` never did. It
  stays offline (no `nix`), but a `flake.nix` that the shallow grammar cannot
  parse degrades dead-override detection to "skipped" rather than failing the
  whole run.
- **Grammar must grow two capabilities.** Extracting *nested-block* override
  attr-paths, and *deleting* a binding (not just splicing one in). Until both
  land, nested-form dead overrides are invisible and `--fix` cannot prune.
- **NDJSON plan count changes 2 → 3.** Any consumer asserting exactly two
  top-level checks (per FDR-0002, which states "lint always runs exactly two
  checks") will see three. This is a deliberate, documented contract change.
- **Direct fix may need a second pass in principle.** As with the add-follows
  path, `--fix` re-analyzes after re-locking; if removing one override unmasks
  another finding, the non-zero exit signals that another `lint` pass is
  warranted.

## Tuning Levers

| Lever | Current (proposed) | Rationale | Change signal |
|---|---|---|---|
| transitive detection | best-effort online; silent no-op when unreachable/offline | needs network + every upstream `flake.nix`; must never turn an offline run into an error | the silent skip hides too many real cases and users ask for a hard-fail / `--online-required` mode |
| deletion span | whole binding line incl. indent + trailing newline | leaves no blank-line scar; matches how a human would delete the line | re-locked diffs show stray whitespace or merged lines |

## Relationship to FDR-0002 and the existing `--fix`

This extends FDR-0002 (`lint` follows opportunities and multi-version inputs).
The add-follows `--fix` splices bindings *in* to collapse byte-identical
duplication; this prunes dead bindings *out*. Both share the `nixedit` PEG
surgery, the re-lock, and the self-stage. The key asymmetry FDR-0002 did not
anticipate: add-follows is driven entirely by the lock (the duplication is
visible there), whereas dead-override pruning is driven by `flake.nix` (the
dead binding is *absent* from the lock), so this feature is the first to make
plain `lint` read `flake.nix`.

## More Information

- Issue #13 (this feature request); #11 (the symptom report and its manual
  fix); #9 / #7 / #8 (the add-follows `--fix` work this mirrors); #6 (prune
  shadowed follows from output).
- FDR-0002 (`docs/features/0002-lint-follows-and-multiversion.md`) — the
  command and `--fix` surface this extends.
- Empirical evidence that dead overrides are absent from `flake.lock`:
  `amarbel-llc/tacky@master`'s `flake.lock` `bats` node vs
  `amarbel-llc/bats@master`'s declared inputs, inspected 2026-06-28 (doppelgang
  session). The `amarbel-llc/eng` `nebulous`/`chrest` direct case (fixed by hand,
  removing the line) is the canonical *direct* example.
- Existing implementation to reuse/extend: `internal/0/flakelock` (lock parser,
  node input sets), `internal/0/nixedit` (`flake.nix` grammar, splice surgery),
  `internal/alfa/lint` (`Analyze`, attr-path BFS), `internal/bravo/render`
  (`LintText` / `LintJSON` / `LintNDJSON`).
