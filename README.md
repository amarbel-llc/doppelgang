# doppelgang

Find duplicate packages in a Nix closure, sorted by wasted bytes, and trace
each copy back to the top-level inputs that pulled it in.

## Subcommands

```
doppelgang dupes [--installable .#default] [--scope runtime|build]
                 [--top N] [--by-owner] [--json]
doppelgang why <regex|/nix/store/...> [--installable .#default]
                                      [--scope runtime|build]
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
