# doppelgang

Find duplicate packages in a Nix closure, sorted by wasted bytes, and trace
each copy back to the top-level inputs that pulled it in.

## Subcommands

```
doppelgang dupes [--installable .#default] [--scope runtime|build]
                 [--top N] [--by-owner] [--json]
doppelgang why <regex|/nix/store/...> [--installable .#default]
                                      [--scope runtime|build]
doppelgang lint [--flake .] [--format auto|text|json|ndjson]
                [--checks follows,multi-version,dead-overrides,nixpkgs-master]
                [--online] [--fix] [--nixpkgs-master-sha <40-hex>]
doppelgang version
```

`dupes` lists "true duplicates" — store paths sharing the same `<name>-<version>`
but with different content hashes (typically because two flake inputs each
carry their own pinned nixpkgs). Multi-output derivations (`jq`, `jq-bin`,
`jq-dev`, `jq-man`) are not duplicates and won't appear.

By default each duplicate copy is printed with its immediate parents in the
closure (which library uses it). Pass `--by-owner` to switch to the set of
top-level installables (direct references of the root) that reach each copy
— useful for attributing waste back to specific flake inputs.

`why` is a thin wrapper over `nix why-depends`. Polymorphic in its argument:
- A `/nix/store/...` path is traced directly (handy when you already have an
  exact path from a `dupes` run).
- Any other argument is treated as a regex; every closure path whose name
  matches has its dependency chain printed.

In `--scope build` (the default), `--derivation` is passed to `why-depends`
so build-time-only paths like setup hooks (`install-shell-files`,
`goBuildHook`) are reachable. `--scope runtime` traces output paths only.

`lint` reads `<flake>/flake.lock` (and `<flake>/flake.nix`) and surfaces
classes of reducible input duplication and rot, plus (opt-in) one convention
check:

- **follows opportunities** — nodes that pin a byte-identical source (same
  `narHash`/`rev`) more than once. For each, `lint` prints the concrete
  `inputs.X.follows = "Y"` line(s) to add to collapse them onto one node.
- **multi-version inputs** — a single `owner/repo` pinned at more than one
  revision. These are highlighted but never auto-collapsed, since choosing a
  revision changes behavior.
- **dead follows overrides** — `inputs.X.follows` overrides that point at an
  input the dependency no longer declares (the condition Nix warns on as "has
  an override for a non-existent input"). A *direct* dead override lives in the
  linted `flake.nix` and is fixable here; a *transitive* one lives in an
  upstream flake's `flake.nix` and is report-only (the fix lands upstream).
- **nixpkgs-master convention** (opt-in; not a default check) — verifies
  `<flake>/flake.nix` declares a top-level `nixpkgs-master` input pinned to
  `github:NixOS/nixpkgs/<40-hex sha>`, the shape eng's update-nix cascade
  requires. It fails on a missing input, a floating ref (no rev, or a
  branch/tag name), or a non-github shape. `--fix` pins it (see below). This
  encodes an amarbel-llc-fleet policy rather than a universal finding, so it
  is excluded from the default checks and only runs when selected via
  `--checks nixpkgs-master` (or the `all` alias). Detection reads `flake.nix`
  alone — no `flake.lock` needed — so it works on a freshly-cloned repo that
  is not yet locked. See `docs/features/0005-lint-nixpkgs-master-convention.md`.
- **canonical-form** (opt-in; not a default check; per-flake opt-in) — flags
  inputs whose bindings (`url`, `follows`/overrides, nested sub-attrset) are
  not contiguous under the top-level `inputs` attrset — i.e. some other
  input's binding is interleaved between two of theirs. Only runs on a flake
  that carries a `# doppelgang: canonical` directive comment on the line
  immediately above its `inputs` binding (the deprecated `# canonical-form`
  spelling still opts in too, and `--fix` upgrades it to the structured form);
  a flake with neither is never flagged or reshaped. `--fix` relocates a
  scattered input's follows/override bindings adjacent to its remaining
  bindings, and migrates a deprecated sentinel to the structured directive
  (see below). Selected via `--checks canonical-form` (or `all`). Detection
  reads `flake.nix` alone. See
  `docs/features/0007-canonical-inputs-block.md`.

The follows / multi-version analyses are entirely offline. Dead-override
detection reads `<flake>/flake.nix` too (direct overrides are not recorded in
the lock — Nix drops them), but stays offline; a missing or unparseable
`flake.nix` simply skips dead-override detection. A missing `flake.lock` is a
hard error. See `docs/features/0002-lint-follows-and-multiversion.md` and
`docs/features/0003-lint-prune-dead-follows-overrides.md`.

`--fix` promotes the auto-fixable findings from "print" to "apply": it edits
`<flake>/flake.nix` to add the follows line(s) `lint` computed *and* to prune
direct dead overrides, re-locks via `nix flake lock`, and `git add`s the
touched files (self-staging, so it composes with a `nix fmt` / pre-commit
`--staged` repair flow). The edit is real Nix-expression surgery — `flake.nix`
is parsed with an embedded PEG (amarbel-llc/langlang); follows bindings are
spliced into, and dead overrides excised from, the top-level `inputs` attrset
by byte offset, preserving the rest of the file. `--fix` is idempotent and
needs `nix` on `PATH`, so unlike plain `lint` it is not offline. Detecting
*transitive* dead overrides (declared in an upstream `flake.nix`) requires
fetching those files, so it is opt-in: `--online` does it read-only, and
`--fix` implies it. The fetch is best-effort (github raw HTTP, falling back to
`nix`); any failure is a silent no-op. **Multi-version inputs and
transitive dead overrides stay report-only** — collapsing or relocating them is
not a local mechanical edit — so `--fix` still exits non-zero if any such
finding (or any residual auto-fixable one) remains afterward. If `flake.nix`
can't be parsed or has no editable `inputs` attrset, `--fix` prints the changes
to make by hand and exits non-zero rather than risk corrupting the file.

When the `nixpkgs-master` check is selected, `--fix` pins the input to
`--nixpkgs-master-sha <40-hex>` (required in that case; `--fix` without it
exits `2`): the `nixpkgs-master.url = "github:NixOS/nixpkgs/<sha>";` binding is
spliced into the `inputs` attrset when the input is missing, or its url is
rewritten in place when it floats — same byte-preserving PEG surgery as the
follows/dead-override edits. Unlike those, the nixpkgs-master pin edits
`flake.nix` only and does **not** re-lock: materializing the new/updated input
into `flake.lock` is left to the caller (eng's cascade runs `nix flake update`
immediately after). `flake.nix` is still staged.

`--checks` restricts the run to a comma-separated subset of `follows`,
`multi-version`, `dead-overrides`, and `nixpkgs-master` (default: the first
three; `all` selects every check including the opt-in `nixpkgs-master`; an
unknown name exits `2`). The selection gates **everything**: only the chosen
checks are rendered (in every `--format`), counted toward the non-zero exit, and
auto-fixed by `--fix`. This lets a caller gate on a chosen subset — e.g. a flake
that intentionally pins inputs at multiple revisions can run
`--checks follows,dead-overrides` to exclude the report-only `multi-version`
check from its CI gate. The expensive dead-override pass (which parses
`flake.nix` and may fetch upstream files) is skipped entirely when
`dead-overrides` is deselected.

`--format` (default `auto`) selects the output: `text` is the bordered
human-readable view; `ndjson` is the amarbel-llc/tap test-result NDJSON schema
(`tap-ndjson(7)`) — one JSON record per line: a leading `plan` record, the
selected checks as top-level test points each with their findings as nested
subtests, and a trailing `summary` record; `json` is a single indented JSON
document (a deselected check's key is omitted, distinguishing "not checked"
from "checked, clean"). `auto` emits `text` when stdout is a TTY and `ndjson`
otherwise, so piping or redirecting `lint` yields machine-readable output
without a flag.

The leading `{"type":"plan","count":N}` record is the schema's normative plan
record: lint knows its plan up front — `N` is the number of *selected* checks
(three by default; fewer under a `--checks` subset, or four when `nixpkgs-master`
is added) — so it announces them as the first record, and the summary's
`plan_count` matches that count.

`lint` exits `1` when any *selected* check reports a finding, so it can run in
CI as a gate against new input duplication and rot (over the chosen subset).

`version` prints the burnt-in `<version> (<commit>)` injected at build time
by the amarbel-llc/nixpkgs `buildGoApplication` overlay.

## Runtime requirements

`doppelgang` shells out to `nix-store`, `nix path-info`, and `nix why-depends`,
so those must be on `PATH`. Designed for use inside a Nix devshell or any
environment that already has the Nix CLI available.

## Build

`doppelgang` is built only via Nix — there is no `go build` recipe. The
amarbel-llc/nixpkgs `buildGoApplication` overlay injects `-X main.version`
and `-X main.commit` ldflags from `flake.nix`, which a raw `go build` would
not. The `version` subcommand reads those.

```
just build       # nix build (the only build path)
just test        # gofumpt + go test ./...
just gomod2nix   # regenerate gomod2nix.toml after changing go.mod
just bump-version 0.0.2
just release 0.0.2   # bump + commit + sign + push v0.0.2 tag
```
