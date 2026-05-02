# doppelgang

Find duplicate packages in a Nix closure, sorted by wasted bytes, and trace
each copy back to the top-level inputs that pulled it in.

## Subcommands

```
doppelgang dupes [--installable .#default] [--scope runtime|build]
                 [--top N] [--by-owner] [--json]
doppelgang why <regex> [--installable .#default]
```

`dupes` lists "true duplicates" — store paths sharing the same `<name>-<version>`
but with different content hashes (typically because two flake inputs each
carry their own pinned nixpkgs). Multi-output derivations (`jq`, `jq-bin`,
`jq-dev`, `jq-man`) are not duplicates and won't appear.

By default each duplicate copy is printed with its immediate parents in the
closure (which library uses it). Pass `--by-owner` to switch to the set of
top-level installables (direct references of the root) that reach each copy
— useful for attributing waste back to specific flake inputs.

`why` is a thin wrapper over `nix why-depends --derivation` that finds every
closure path matching the supplied regex and prints the dependency chain for
each. Pattern is matched against the name (after stripping the `/nix/store/<hash>-`
prefix and any trailing `.drv`).

## Runtime requirements

`doppelgang` shells out to `nix-store`, `nix path-info`, and `nix why-depends`,
so those must be on `PATH`. Designed for use inside a Nix devshell or any
environment that already has the Nix CLI available.

## Build

```
just build      # go build + nix build
just test       # gofumpt + go test ./...
just gomod2nix  # regenerate gomod2nix.toml after changing go.mod
```
