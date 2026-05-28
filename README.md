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

`lint` reads `<flake>/flake.lock` and surfaces reducible input duplication:

- **follows opportunities** — nodes that pin a byte-identical source (same
  `narHash`/`rev`) more than once. For each, `lint` prints the concrete
  `inputs.X.follows = "Y"` line(s) to add to collapse them onto one node.
- **multi-version inputs** — a single `owner/repo` pinned at more than one
  revision. These are highlighted but never auto-collapsed, since choosing a
  revision changes behavior.

The analysis is entirely offline (no `nix` invocation); it reads only
`<flake>/flake.lock`. A missing `flake.lock` is a hard error. See
`docs/features/0002-lint-follows-and-multiversion.md`.

`--format` (default `auto`) selects the output: `text` is the bordered
human-readable view; `ndjson` is the amarbel-llc/tap test-result NDJSON schema
(`tap-ndjson(7)`) — one JSON record per line: a leading `plan` record, the two
checks as top-level test points each with their findings as nested subtests,
and a trailing `summary` record; `json` is a single indented JSON document.
`auto` emits `text` when stdout is a TTY and `ndjson` otherwise, so piping or
redirecting `lint` yields machine-readable output without a flag.

The leading `{"type":"plan","count":2}` record is the schema's normative plan
record: lint knows its fixed two checks up front, so it announces them as the
first record, and the summary's `plan_count` matches that count.

`lint` exits `1` when any follows or multi-version finding is reported, so it
can run in CI as a gate against new input duplication.

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
