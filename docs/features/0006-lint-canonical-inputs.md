---
status: exploring
date: 2026-07-14
promotion-criteria: |
  Promote to `proposed` once: (1) at least one repo in the fleet has its
  `github:` input rewritten to the canonical forge URL via
  `doppelgang lint --fix --checks canonical-inputs --papi-domain linenisgreat.com`
  and the cascade's following `nix flake update` successfully re-locks the
  new URL without regressing the pinned rev; (2) the check is validated
  as a no-op on repos whose inputs already point at canonical forge URLs;
  (3) offline degradation (papi unreachable, domain not set) is exercised
  and confirmed to produce zero findings rather than a hard failure; (4) the
  multi-version dedup violation described in the problem statement is confirmed
  resolved (no two lock nodes for the same repo at the same rev) after a
  fleet-wide cascade run with the check enabled.
---

# `lint` — canonical-inputs check + repair (the forge-migration ref flip)

> Implementation note: landed concurrently with this FDR (per the repo's
> convention of shipping in `exploring`). The check and repair are implemented;
> fleet-wide enablement and the eng-side cascade integration are separate
> follow-up decisions, tracked in issue #17.

## Problem Statement

The fleet's repos migrated from GitHub to a self-hosted Forgejo forge as the
canonical origin (FDR-0016 forge-primary; GitHub lives on as a read-only push
mirror). Every flake input still referencing a repo via its old
`github:<owner>/<name>` form — rather than the canonical forge clone URL
(today's convention: `git+https://code.linenisgreat.com/<name>.git`) — creates
two distinct classes of operational hazard:

**Mixed-forge dedup violations.** When repo A consumes repo B via
`git+https://code.linenisgreat.com/B.git` and repo C consumes repo B via
`github:<owner>/B`, Nix's flake dedup logic cannot collapse them: the lock
types differ (`git` vs `github`), so even at the exact same rev they occupy
two separate lock nodes. doppelgang's `follows` check and splice machinery
*detect and fix* this split in any given repo's lock — but the URL-form
mismatch is the root cause, and the fix needs to land at the URL level.

**Mirror-lag downgrades.** `github:` inputs resolve against GitHub mirrors,
which are push-synchronized from the forge on merge. `nix flake update` runs
may race ahead of the mirror sync and pin a revision that is *older* than the
forge's current head, effectively moving inputs backwards. Canonical forge refs
resolve directly against the forge and never lag.

## Design

### Mapping source: PAPI

The check discovers the canonical URL for each repo from the operator's
published PAPI (Personal API), whose `/papi/repos` resource enumerates every
published repository with its canonical web URL. `papi repos <domain>` queries
this endpoint and returns a JSON array. No owner→forge table is hardcoded in
doppelgang; the mapping is entirely PAPI-discovered.

The canonical nix flake URL is resolved from each entry in two tiers
(papi#56 amendment, rollout-order-independent):

1. **`flake_url` present** — the entry's `flake_url` field is used verbatim as
   the nix input ref (e.g.
   `https://code.linenisgreat.com/crap/archive/master.tar.gz`). This is the
   tarball form: `nix flake update` tracks the branch while the archive's
   `Link rel="immutable"` header pins the revision. The fleet may carry a mix
   of entries with and without `flake_url` during rollout; each entry is
   resolved independently, so no global ordering constraint exists between
   server and consumer upgrades.

2. **`flake_url` absent** (pre-papi#56 server or repo not yet upgraded) — the
   canonical nix URL is derived from the web `url` field as `git+<url>.git`
   (prefixing `git+` and appending `.git` to the HTTPS web URL). This is the
   original git-fetcher form and remains the safe fallback.

The domain is configured via `--papi-domain <domain>` (or the `PAPI_DOMAIN`
environment variable). When neither is set, or when the `papi` call fails (e.g.
offline, tailnet not connected), the check degrades gracefully: zero findings,
a stderr note, and a zero exit from the check itself. This matches the
offline-degradation contract of every other papi-dependent path in the fleet.

#### Dual-homed repos and the canonical marker (papi#53)

The `/papi/repos` endpoint deliberately lists a dual-homed repo once per forge
(papi#50) — so a repo mirrored to both the self-hosted forge and GitHub appears
twice in the JSON array under the same `name`. The naive last-entry-wins map
build lets server enumeration order decide which URL "wins", and this
mis-flipped ~34 fleet inputs on 2026-07-14 (issue #18).

Resolution (coordinating with papi#53, which amends RFC-0001): each entry
carries a boolean `canonical` field. `papiRepoURLs` applies the following rules
when constructing the name→URL map from the decoded JSON:

- **Single entry for a name** — accepted unchanged; `canonical` is ignored.
- **Multiple entries, exactly one marked `canonical:true`** — the marked
  entry's URL is used; the others are discarded.
- **Multiple entries, no entry marked** (pre-amendment server) — the repo is
  treated as **ambiguous** and *skipped* with a stderr note. This is the
  load-bearing behavior change: it stops mis-flips even before the server
  publishes markers, because the check will simply not rewrite an input it
  cannot resolve unambiguously.
- **Multiple entries, more than one marked** (server nonconformance) — also
  ambiguous, also skipped with a stderr note. The client never makes a
  tiebreak for server-side protocol violations.

### Detection

For each top-level root input that resolves to an actual lock node (not a
follows-resolved alias):

1. Look up the input name in the PAPI repo map.
2. If found, read the input's current `.url` binding from flake.nix via
   nixedit (the same byte-preserving shallow PEG as every other check).
3. If the current URL differs from the PAPI canonical URL, report a finding.

Inputs whose URL binding is not a plain quoted string (interpolation, `let…in`,
etc.) are skipped — the safe conservative outcome, matching nixedit's existing
behaviour for unparseable values. Inputs not present in the PAPI map (e.g.
nixpkgs, flake-utils, or any repo outside the operator's domain) are skipped
unconditionally.

### Repair

`--fix` rewrites each non-canonical input URL in place via
`nixedit.SetInputURL`, preserving all surrounding whitespace and structure
byte-for-byte. The repair edits `flake.nix` only and does **not** re-lock.
Re-locking is the caller's responsibility (the cascade's `nix flake update`
runs immediately after the repair lane, materializing the new forge URL into
`flake.lock`). `flake.nix` is staged with `git add` after the edit, same as
every other repair.

### Opt-in placement

The check is **not** in the default selection (the three always-safe offline
checks). It requires a network call (papi) and a `--papi-domain` parameter, so
enabling it fleet-wide is a deliberate per-repo or per-cascade decision. It is
added to `AllChecks` and selectable via `--checks canonical-inputs` or
`--checks all`.

### Ordering note

Fleet rollout must proceed upstream-first: a repo can only flip an input whose
target repo is already live on the forge (so the forge URL is resolvable and
the cascade's `nix flake update` can fetch it). eng's own ~28 `github:` inputs
flip last, after every downstream dependency has migrated.

## Alternatives considered

**Hardcoded owner→forge table.** Rejected — violates the no-fleet-policy
requirement from the issue. A future operator with a different domain would
need to fork the table.

**Use `papi repos --url` (SSH clone URLs).** The `--url` flag emits SSH clone
URLs (`ssh://git@...`), which are suitable for `git clone` but not for nix
flake inputs (nix expects `git+https://` for HTTPS fetches). The JSON
(`papi repos <domain>`, no `--url`) exposes the canonical HTTPS web URL
directly, from which the nix form is trivially derived.

**Re-lock in the repair.** Would force a second lock rebuild immediately before
the cascade's own `nix flake update`, duplicating work and potentially
introducing a race against the forge. The nixpkgs-master repair (FDR-0005)
establishes the precedent: edit flake.nix, leave locking to the caller.
