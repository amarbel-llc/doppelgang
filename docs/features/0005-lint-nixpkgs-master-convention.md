---
status: exploring
date: 2026-07-04
promotion-criteria: |
  Promote to `proposed` once: (1) eng's `update-repo-in-session.bash`
  replaces its `sed` + refusal with a single
  `doppelgang lint --fix --checks nixpkgs-master --nixpkgs-master-sha $SHA`
  invocation and a full cascade run self-onboards at least one
  previously-halting repo (the input ADDED in the same bump commit) without
  regressing a conforming repo's pin cascade; (2) the check + repair is
  validated against the real fleet shapes — the flat-in-block form (most
  repos), the top-level flat `inputs.nixpkgs-master.url` form (gomod2nix),
  and a nested sub-attrset form — confirming byte-preservation on each; (3)
  the locking decision (repair edits flake.nix only, caller re-locks) holds
  up in the cascade, i.e. the following `nix flake update` reliably
  materializes the added/rewritten input into flake.lock.
---

# `lint` — nixpkgs-master convention check + repair

> Implementation note: landed concurrently with this FDR (per the repo's
> convention of shipping in `exploring`). The check and both repair modes
> (splice-when-missing, rewrite-when-present) are implemented; the eng-side
> cascade integration is a separate follow-up (issue #16's "deeper
> integration"), which is why this stays `exploring`.

## Problem Statement

eng's update-nix cascade requires every member repo to carry the convention

```nix
nixpkgs-master.url = "github:NixOS/nixpkgs/<40-hex sha>";
```

`eng/bin/update-repo-in-session.bash` REFUSES a repo lacking it (correct
fail-loud) and cascades the pin with a raw `sed` when present (fragile: the
sed only matches the `nixpkgs-master.url = "…"` single-line form). With the
forge's public read plane live, new repos enter cascade discovery
automatically, and any repo missing the convention halts the run.

This is the same failure class as the check-only gates the 2026-07-03/04
convergence work kept hitting (tommy stamps, dagnabit facades, rustfmt
drift): a convention gate with no repair lane blocks the cascade until the
repair is added by hand. doppelgang already owns two such lanes
(follows/dead-overrides), so the nixpkgs-master convention is its natural
third.

## Interface

```
doppelgang lint [--flake .] --checks nixpkgs-master [--fix --nixpkgs-master-sha <40-hex>]
```

The feature adds a fourth check to the `--checks` vocabulary and one repair
parameter:

- **New check: `nixpkgs-master`.** Reads `<flake>/flake.nix` alone (no lock)
  and classifies the top-level `nixpkgs-master` input's url against
  `^github:NixOS/nixpkgs/[0-9a-f]{40}$`. A conformant input yields no
  finding; otherwise one of three failure modes is reported with a precise
  diagnostic:
  - **missing** — no `nixpkgs-master.url` input is declared (a bare
    `X.inputs.nixpkgs-master.follows` override does not count — that is not a
    top-level input declaration);
  - **floating** — a `github:NixOS/nixpkgs` ref not pinned to a full 40-hex
    revision (no rev, a branch/tag name, a short rev, or uppercase hex);
  - **non-github** — a url that is not a `github:NixOS/nixpkgs` ref at all.
- **Opt-in, not a default check.** Unlike follows/multi-version/dead-overrides
  (universal reducible-duplication findings), this encodes an amarbel-llc
  *fleet policy* — a specific input pinned a specific way — so a plain `lint`
  on an arbitrary flake must not fail for lacking it. It is excluded from the
  default `--checks` set and runs only when named (`--checks nixpkgs-master`)
  or via the `all` alias. This keeps every existing consumer's default
  behavior — exit code, output, NDJSON plan count of 3 — unchanged.
- **`--fix` repairs it, `--nixpkgs-master-sha` is the target.** When the input
  is **missing**, the binding
  `nixpkgs-master.url = "github:NixOS/nixpkgs/<sha>";` is spliced into the
  top-level `inputs` attrset (block or flat form) with the same
  byte-preserving PEG surgery the follows-collapse uses. When it is **present
  but floating/non-github**, only that url's string literal is rewritten in
  place. Idempotent: a no-op when already conformant. The sha is a required
  parameter under `--fix` when the check is selected; `--fix` without it (or
  with a non-40-hex value) exits `2` before any analysis — fail-loud.
- **Works without a lock.** Because detection needs only `flake.nix`, `lint`
  loads `flake.lock` only when a lock-dependent check
  (follows/multi-version/dead-overrides) is selected. So
  `--checks nixpkgs-master` runs on a freshly-cloned, not-yet-locked repo —
  the self-onboarding case.
- **Exit code.** Non-zero when the selected `nixpkgs-master` check reports a
  finding (like the other checks). After `--fix`, `0` only when the input is
  conformant.

### Locking decision — repair edits flake.nix, caller re-locks

The follows-collapse and dead-override `--fix` paths re-lock
(`nix flake lock`) because they rewrite the lock graph the finding was
derived from. **The nixpkgs-master pin deliberately does NOT re-lock.** It
edits `flake.nix` and stages it, leaving materialization of the new/updated
input into `flake.lock` to the caller. Rationale:

- eng's cascade runs `nix flake update` immediately after the repair, which
  re-locks everything; a repair-side re-lock would be redundant.
- Re-locking a freshly *added* `github:NixOS/nixpkgs/<sha>` input forces a
  network fetch of that exact revision — work the check/repair should not
  compel, especially in a `--checks nixpkgs-master`-only invocation intended
  to be cheap and offline-capable.

When a nixpkgs-master edit rides alongside a follows/dead edit in one `--fix`
run, the re-lock those require picks up the new input as a side effect.
`flake.nix` is always staged; `flake.lock` is staged only when it exists (a
missing lock must not make `git add` fail atomically and leave `flake.nix`
unstaged).

## Examples

Checking a repo missing the convention:

```
── nixpkgs-master convention ──
nixpkgs-master input missing: declare `nixpkgs-master.url = "github:NixOS/nixpkgs/<40-hex sha>"`
```

Repairing it (the cascade's invocation shape):

```
$ doppelgang lint --fix --checks nixpkgs-master --nixpkgs-master-sha 567a49d…875f
doppelgang lint --fix: pinned nixpkgs-master in ./flake.nix to github:NixOS/nixpkgs/567a49d…875f
```

*Decision served:* a newly-discovered repo missing the convention gets it
ADDED in the same bump commit rather than halting the run; a repo whose pin
merely floats or lags gets it rewritten. Membership policy (which repos are
cascade members at all) stays a separate filter in eng's clone step.

## Implementation outline (technical detail; not user-facing)

- **Read side (`internal/0/nixedit`, `InputURL`).** Walks the top-level
  `inputs` for the binding whose full attr-path is `inputs.<input>.url`,
  across the three fleet shapes (flat top-level, flat-in-block, nested
  sub-attrset value), and returns its string value. Reuses the existing
  shallow-PEG walker + sub-attrset re-parse machinery (`keyValPath`,
  `soleGroup`, the base-offset recursion `collectFollows`/`locateBindings`
  use).
- **Write side (`internal/0/nixedit`, `SetInputURL`).** Locates the url
  string literal's byte span; when present, replaces just that span
  (byte-preserving the rest); when absent, splices `inputs.<input>.url = "…"`
  via the existing `Apply` (which already handles block vs flat form and
  idempotency). Returns `changed=false` when already equal.
- **Classification (`internal/alfa/lint`, `ClassifyNixpkgsMaster`).** Pure
  function over `(url, present)` → `*NixpkgsMasterFinding` (nil when
  conformant), plus `ValidNixpkgsSHA` and `NixpkgsMasterURL` helpers. No I/O.
- **Selection (`internal/alfa/lint`).** `CheckNixpkgsMaster` joins
  `AllChecks`; a new `DefaultChecks` (the original three) backs the
  absent-`--checks` default so the new check is opt-in.
- **Rendering (`internal/bravo/render`).** A fourth text section, a
  `nixpkgsMaster` JSON key (`{conformant, status?, url?}`), and a fourth
  NDJSON `test` record — all gated by the selection like the others.
- **CLI (`cmd/doppelgang`).** `--nixpkgs-master-sha` flag; upfront validation
  under `--fix`; lock-load made conditional; `nixpkgsMasterFinding` helper;
  `lintFix` extended with the `SetInputURL` pass and the no-relock policy.

## Limitations

- **Opt-in means it does not gate a plain `lint`.** A repo that should carry
  the convention but is never linted with `--checks nixpkgs-master` (or the
  eng cascade) is not flagged. This is deliberate: the convention is fleet
  policy, not a universal lint, and baking it into the default would fail
  every non-fleet flake.
- **No re-lock (by design).** The repair leaves `flake.lock` stale until the
  caller re-locks. A `--checks nixpkgs-master --fix` run that is *not*
  followed by `nix flake update` produces a `flake.nix` whose new/rewritten
  input is not yet in the lock. Documented as the caller's responsibility.
- **Only plain-string urls.** A `nixpkgs-master` input whose url value is a
  non-string expression (interpolation, `let … in`) is treated as
  not-present (⇒ missing). The fleet's inputs are all plain double-quoted
  urls, so this is safe in practice; a splice in that exotic case could
  produce a duplicate key that Nix rejects (surfaced on the next lint).
- **`--checks all --fix` now needs the sha.** Because `all` includes
  nixpkgs-master, `--checks all --fix` without `--nixpkgs-master-sha` exits
  `2`. The default `--fix` (three checks) is unaffected.

## Relationship to prior FDRs and the existing `--fix`

This extends FDR-0002/0003/0004 (`lint` follows / multi-version /
dead-overrides / input-ordering). It reuses their `nixedit` PEG surgery,
selection vocabulary, and render pipeline. The key differences: it is the
first *opt-in* (non-default) check; the first check that needs no
`flake.lock` at all; the first repair driven by a caller-supplied parameter
(`--nixpkgs-master-sha`) rather than derived from the lock; and the first
repair that deliberately does not re-lock.

FDR-0004 (input ordering, still `exploring`) reserves the "NDJSON plan count
3 → 4" framing for *its* future check. Because this check is opt-in and
excluded from the default set, the **default** plan count stays 3 — so
FDR-0004's assumption is preserved: when input-ordering lands as a default
check, it is the one that moves the default count 3 → 4. nixpkgs-master only
raises the count under an explicit `--checks nixpkgs-master` / `all`.

## More Information

- Issue #16 (this feature request and the eng-side deeper integration).
- `eng/bin/update-repo-in-session.bash` — the cascade script whose `sed` +
  refusal this replaces (eng-side follow-up, not in this repo).
- Existing implementation reused/extended: `internal/0/nixedit`
  (`Apply`/`Overrides`/`DeleteBindings` surgery, now + `InputURL`/`SetInputURL`),
  `internal/alfa/lint` (`Selection`, `Report`), `internal/bravo/render`
  (`LintText`/`LintJSON`/`LintNDJSON`).
