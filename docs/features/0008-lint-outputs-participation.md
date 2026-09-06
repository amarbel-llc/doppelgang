---
status: exploring
date: 2026-09-06
promotion-criteria: |
  Promote to `proposed` once: (1) a full fleet cascade run self-onboards the
  closed-outputs repo(s) — troupe is the only one today — into the
  nixpkgs-master convention with no hand edit, i.e. the same
  `doppelgang lint --fix --checks nixpkgs-master,canonical-inputs` that
  previously produced a non-evaluating flake now produces one that
  `nix flake check` accepts; (2) eng's `settings.linter.doppelgang-flake`
  stanza carries `outputs-participation` in both its `options` and
  `repair-options` check lists, and a gate run over the fleet reports no
  false positives; (3) the widened output survives a real conformist repair
  pass unformatted-clean — see "Formatting is the caller's problem" below —
  confirming the ordering inference recorded there. Consider promoting the
  check into `DefaultChecks` at that point (see "Why it is opt-in").
---

# `lint` — outputs-participation check + repair

> Implementation note: landed concurrently with this FDR (per the repo's
> convention of shipping in `exploring`). The check, its repair, and the
> unconditional widening invariant on the input-splicing paths are all
> implemented. The eng-side gate wiring is a separate follow-up owned by the
> eng repo, which is why this stays `exploring`.

## Problem Statement

Nix calls a flake's `outputs` function with one attrset holding `self` plus
**every declared input**. A formals set that enumerates names without a
trailing `...` is therefore CLOSED: passing it an input it does not name is a
hard eval failure.

```
error: function 'outputs' called with unexpected argument 'nixpkgs-master'
  at troupe/flake.nix:37
```

That error is what the 2026-09-06 fleet `update-nix` convergence pass
produced on `troupe`. The pass runs

```
doppelgang lint --fix --checks nixpkgs-master,canonical-inputs
```

per repo to self-onboard it into the nixpkgs-master convention (FDR 0005).
The repair spliced `nixpkgs-master` into `inputs` correctly — and left the
`outputs` signature alone:

```nix
outputs =
  {
    self,
    nixpkgs,
    utils,
    treefmt-nix,
    ringmaster,
    bats,
  }:                     # <- closed; `nixpkgs-master` is now rejected
```

Most fleet flakes carry `...`, so the splice is absorbed harmlessly and the
gap went unnoticed. troupe's argument list was closed, and its inputs comment
even asserted that ringmaster's `nixpkgs-master` "has no troupe-level
counterpart" — a de-facto opt-out of the fleet convention.

Two distinct defects, addressed separately below:

1. **The repair could emit a flake that does not evaluate.** A repair lane
   that breaks the thing it repairs is worse than no repair lane, because the
   cascade trusts it.
2. **Nothing detected the condition.** A repo could sit in the
   non-participating state indefinitely, and re-acquire it after a fix.

## The widening invariant (defect 1)

`lintFix` now widens the `outputs` formals whenever an edit ran that can ADD
an input — today the `nixpkgs-master` and `canonical-inputs` URL repairs,
both of which splice through `nixedit.SetInputURL`. It is deliberately NOT
gated on the new check being selected:

> A repair must never leave behind a flake that cannot be evaluated.

The widening is a no-op on the common fleet shape (formals already carry
`...`), so the invariant costs nothing where it is not needed. This is what
migrates troupe and any closed-outputs sibling: a corrected `--fix` pass
onboards them instead of breaking them, with no hand-authored migration.

## The check (defect 2)

```
doppelgang lint --checks outputs-participation [--fix]
```

Reports every input `flake.nix` declares that its `outputs` signature cannot
accept. It is deliberately conservative and stays silent when the signature
already accepts unnamed inputs (`...` or a simple `inputs:` argument), when
there is no recognisable top-level `outputs` binding, and when the shallow
grammar cannot parse the file — matching how every other flake.nix-reading
check degrades.

Note what the check is NOT: it does not assert that `nixpkgs-master` is
present or pinned. That half of "participates in the nixpkgs-master
convention" is already the `nixpkgs-master` check (FDR 0005), and keeping the
two separate is what makes this one runnable in a sandboxed gate — see below.

### Scope: any input, not just nixpkgs-master

The failure class is general — it is a property of the signature versus the
declared input set, and `nixpkgs-master` was merely the input the cascade
happened to splice. Scoping the check to one input name would leave the same
breakage reachable by any other splice, so it checks them all.

## Why it is SHA-free, and why that decides where it runs

eng gates flake-input hygiene through conformist's
`settings.linter.doppelgang-flake` stanza, which runs
`doppelgang lint --checks follows,dead-overrides,canonical-form` as its check
and the same list under `--fix` as its repair.

`nixpkgs-master` is absent from that list and cannot be added: `--fix` with
that check REQUIRES `--nixpkgs-master-sha` (`lintMain` exits 2 without it),
and a sandboxed gate has no source for the fleet revision. `canonical-inputs`
is likewise out — it needs a live PAPI call.

`outputs-participation` was designed to avoid that trap. It reads `flake.nix`
and nothing else: no lock, no network, and no fleet parameter. Its repair
needs no external state either — appending `...` is derivable from the file
alone. So it is the participation half that a sandboxed gate CAN enforce,
which is precisely what the enforcement requirement asked for.

The division of labour that falls out:

| Half of "participates"      | Check             | Needs        | Enforced by            |
| --------------------------- | ----------------- | ------------ | ---------------------- |
| input present + SHA-pinned  | `nixpkgs-master`  | fleet SHA    | eng's update-nix pass  |
| signature accepts the input | `outputs-participation` | nothing | conformist gate        |

## Why it is opt-in

`outputs-participation` is in `AllChecks` but NOT in `DefaultChecks`.

Unlike the other opt-in checks this is not a policy or hygiene preference —
it flags a hard eval failure, so it would be defensible in the default set.
It is held out only to preserve the existing exit-code, output, and NDJSON
plan-count behaviour for every current consumer, the same compatibility
promise that keeps the default at three. Fleet-wide enforcement comes from
eng's stanza instead. Promoting it to `DefaultChecks` is a separate,
deliberate call — see the promotion criteria.

## Formatting is the caller's problem

conformist accumulates `passes-files = false` whole-tree linters during
`Check` and defers them to `Finalize`, explicitly after all batches. A
whole-tree repair therefore runs AFTER the formatters, and its output is not
reformatted in the same pass. `restage-repair-outputs` does not change this —
it governs which files are `git add`ed in the `--staged` lane, not
formatting.

Consequence: this repair must emit already-nixfmt-clean output, or the tree
is left unformatted and the next run reformats it — a commit carrying
unformatted output, or a gate that appears to flip-flop between runs.

So the inserted text mirrors the surrounding layout rather than being
normalised: `...` on its own line at the formals' own indent for a
multi-line set, inline for a single-line one, with a separating comma added
only when the last formal lacks a trailing one.

This was verified against the real troupe flake: the widened file was placed
in a tracked path of this repo and `just lint-fmt` (conformist's read-only
check, nixfmt included) reported no finding for it. The control matters — an
UNTRACKED probe file is invisible to the sandboxed check, so "no finding" is
vacuous until the file is staged; the verification above was re-run after
staging, with a deliberately mis-indented copy confirming the harness does
flag that file when it is wrong.

The ordering claim itself (`Check` defers whole-tree linters to `Finalize`)
is read from conformist's `format/check.go`; repair-mode ordering is
strongly implied by the shared whole-tree handling rather than traced end to
end. The nixfmt-cleanliness of the output is what was verified directly, and
it is the property that matters regardless of the ordering.

## Implementation

Read side, `internal/0/nixedit`:

- `OutputsFormals(src) (OutputsShape, names, err)` — classifies the top-level
  `outputs` argument set as absent / simple-arg / ellipsis / closed, and
  returns the names a formals set binds.
- `InputNames(src) ([]string, error)` — the declared top-level inputs, in
  source order, across both the `inputs = { … }` block form and the flat
  `inputs.x.url = …` form.

Write side:

- `WidenOutputsFormals(src) (out, changed, err)` — appends `...` to a closed
  formals set, byte-preserving and idempotent; a no-op for every shape that
  already accepts unnamed inputs.

Classification, `internal/alfa/lint`:

- `ClassifyOutputsParticipation(flakeNix) (*OutputsParticipationFinding, error)`.
- `CheckOutputsParticipation` joins `AllChecks`; `Report.OutputsParticipation`
  carries the finding.

### Reaching `outputs` through the shallow grammar

`nix.peg` skips a binding's value opaquely, and the grammar's own comments
note that an `outputs = { … }: let … in …` value defeats that skip — an inner
`;` from the `let` terminates the value early and `OpaqueTail` absorbs the
remainder.

That truncation turns out to be harmless here, which is why no grammar change
was needed: the formals group is the FIRST item of the value, long before any
`let`, so the head of the value is intact even when its tail is not. This was
confirmed by parsing the real troupe shape and observing the `outputs`
binding still resolve to a `Binding` with path `[outputs]`.

The formals themselves are then scanned directly rather than parsed, because
they are not modelled by the grammar. The scan tracks brace depth and skips
strings and comments, so `...` inside a default value
(`{ pkgs ? f { ... } }`) does not count as the signature's ellipsis, and the
insertion point is recorded during a forward pass rather than recovered by
walking back from the closing brace — a trailing comment inside the formals
would otherwise swallow the insertion.

## Finding text names its own check

A conformist finding is attributed to the linter stanza name
(`doppelgang-flake`), which carries several check ids at once, so an operator
cannot map a finding back to the check that fired unless the output says so.
The finding therefore leads with its own check id, matching the shape
conformist's native linters use (`git-remotes(#8): …`).

## Limitations

- The repair widens with `...` rather than naming the input explicitly. `...`
  is the least-invasive edit and the fleet-dominant shape; a flake that
  prefers explicit formals must add the name by hand (the finding says so).
- A flake whose `outputs` value the shallow grammar cannot reach at all
  yields no finding rather than an error. Silence is the conservative
  outcome, consistent with the other flake.nix-reading checks — but it does
  mean the check is not a proof of participation, only a detector of
  non-participation.
- The check does not consult `flake.lock`, so an input declared only in the
  lock (never in `flake.nix`) is invisible to it. That state is not
  reachable through normal `nix` usage.
