# Build, format, test, self-lint
default: build test lint

# Build via nix only — there is no `build-go` recipe. The amarbel-llc/nixpkgs
# fork's buildGoApplication overlay burns the version + commit into the
# binary via -ldflags, which a raw `go build` would not. Always use this.
build: build-nix

build-nix:
  nix build --show-trace

# Format + go test
test: fmt test-go

# Run go test via the flake's checks output so the suite executes in a
# sandboxed nix build. The check derivation is defined in flake.nix as
# checks.<system>.go-test.
test-go:
  nix flake check --show-trace

# Fast per-package `go test` for the agent TDD dev-loop. Runs in the devshell
# (seconds, sees untracked files) rather than the nix sandbox, so it is the
# loop to iterate against; the authoritative sandboxed run remains `test-go`
# (nix flake check), which the merge pre-merge hook (`just`) runs.
# Usage: just test-go-pkg internal/alfa/lint
[group('debug')]
test-go-pkg PKG:
  go test ./{{PKG}}/...

# Self-consume: run `doppelgang lint` against this repo's own flake.lock.
# Surfaces follows opportunities and multi-version inputs in our own
# inputs. Recipe (and `just`) fails when lint reports findings.
lint: lint-nix

lint-nix: build-nix
  ./result/bin/doppelgang lint

# Run `doppelgang lint --flake <DIR>` against an arbitrary flake
# directory. Used for ad-hoc validation of lint output against external
# fixtures (e.g. /tmp/claude-madder-* snapshots during the FDR-0002
# promotion checks). Exits 1 on findings.
[group('explore')]
explore-lint-flake DIR: build-nix
  ./result/bin/doppelgang lint --flake {{DIR}}

# Run `doppelgang lint --online --flake <DIR>` (text output): read-only
# transitive dead-override detection, which fetches upstream flake.nix files
# over the network. DIR defaults to this repo. Used to validate transitive
# detection against real closures (e.g. issue #11's conformist/moxy/posh).
[group('explore')]
explore-lint-online DIR=".": build-nix
  ./result/bin/doppelgang lint --online --format text --flake {{DIR}}

# Run `doppelgang lint --fix --flake <DIR>` against an arbitrary flake
# directory: apply the follows-opportunity edits to its flake.nix and
# re-lock. Used to validate the --fix repair path end-to-end against a
# fixture flake with a known duplicate.
[group('explore')]
explore-lint-fix DIR: build-nix
  ./result/bin/doppelgang lint --fix --flake {{DIR}}

# Format the tree via treefmt (config: treefmt.nix). Forwards args, e.g.
# `just fmt -- --ci` to fail if anything would change.
fmt *ARGS:
  nix fmt -- {{ARGS}}

# Regenerate gomod2nix.toml after go.mod / go.sum changes
gomod2nix:
  gomod2nix

# Tag a doppelgang release. The "v" prefix is added for you, so pass the
# semver without it. Usage: just tag 0.1.0 "feat: initial release"
tag version message:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{version}}"
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    if [[ -n "$prev" ]]; then
      gum log --level info "Previous: $prev"
      git log --oneline "$prev"..HEAD
    fi
    git tag -s -m "{{message}}" "$tag"
    gum log --level info "Created tag: $tag"
    git push origin "$tag"
    gum log --level info "Pushed $tag"
    git tag -v "$tag"

# Sed-rewrite doppelgangVersion in flake.nix to the given semver. The
# version is burnt into the binary at build time via -ldflags (auto-injected
# by the amarbel-llc fork's buildGoApplication overlay), so flake.nix is the
# single source of truth. No-op if already at the target version. Usage:
# just bump-version 0.0.2
bump-version new_version:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(grep 'doppelgangVersion = ' flake.nix | sed 's/.*"\(.*\)".*/\1/')
    if [[ "$current" == "{{new_version}}" ]]; then
      gum log --level info "already at {{new_version}}"
      exit 0
    fi
    sed -i.bak 's/doppelgangVersion = "'"$current"'"/doppelgangVersion = "{{new_version}}"/' flake.nix && rm flake.nix.bak
    gum log --level info "bumped doppelgangVersion: $current → {{new_version}}"

# Cut a release: must be run on master. Bumps doppelgangVersion in flake.nix,
# commits the bump with a changelog-style message built from commits since
# the last v* tag, pushes master, then signs and pushes the v{{version}}
# tag. The "v" prefix is added for you, so pass the semver without it.
# Usage: just release 0.0.2
#
# Use `just tag <version> <message>` directly if you want to control the
# commit message yourself without bumping.
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    current_branch=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$current_branch" != "master" ]]; then
      gum log --level error "just release must be run on master (currently on $current_branch)"
      exit 1
    fi
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    header="release v{{version}}"
    if [[ -n "$prev" ]]; then
      summary=$(git log --format='- %s' "$prev"..HEAD)
      if [[ -n "$summary" ]]; then
        msg="$header"$'\n\n'"$summary"
      else
        msg="$header"
      fi
    else
      msg="$header"
    fi
    just bump-version "{{version}}"
    if ! git diff --quiet flake.nix; then
      git add flake.nix
      git commit -m "chore: release v{{version}}"
      git push origin master
      gum log --level info "pushed flake.nix bump to master"
    fi
    just tag "{{version}}" "$msg"

clean:
  rm -rf result result-*
