default: lint build test

lint: lint-fmt lint-worktree lint-flake lint-man

build: build-gomod2nix build-nix

test: test-go

codemod: codemod-fmt

clean: clean-build

# Read-only formatting + the eng presets' file-based linters, via the sandboxed
# checks.formatting derivation. Does NOT modify files — the modifying
# counterpart is `codemod-fmt`.
#
# check formatting and the eng file-based linters read-only
[group('lint')]
lint-fmt:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build ".#checks.${system}.formatting" --no-link --print-build-logs

# Impure eng checks (git remotes, sweatfile, agents-md) against the working
# tree. The binary comes from `.#conformist`, NOT from PATH: eng's
# home-profile wrapper (conformistCwd) also installs a `conformist`, and it
# gates on a checked-in conformist.toml/.conformist.toml/treelint.toml at the
# git root, exiting 2 before it ever reads --config-file. No eng repo carries
# one — the config is Nix-generated (.#conformist-impure-config) — so whenever
# the profile bin wins the PATH race (any run outside the devShell) a bare
# `conformist` fails this lane. `nix run` makes it PATH-order independent.
#
# run the impure eng checks against the working tree
[group('lint')]
lint-worktree:
  #!/usr/bin/env bash
  set -euo pipefail
  cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
  nix run '.#conformist' -- check --config-file "$cfg" --tree-root .

# Self-consume: run `doppelgang lint` against this repo's own flake.lock.
# Surfaces follows opportunities and multi-version inputs in our own
# inputs. Recipe (and `just`) fails when lint reports findings.
#
# run `doppelgang lint` against this repo's own flake.lock
[group('lint')]
lint-flake: build-nix
  ./result/bin/doppelgang lint

# Build the doppelgang binary via nix. The amarbel-llc/nixpkgs fork's
# buildGoApplication overlay burns the version + commit into the binary via
# -ldflags, which a raw `go build` would not. Always use this.
#
# build the doppelgang binary via nix
[group('build')]
build-nix:
  nix build --show-trace

# Run go test via the flake's checks output so the suite executes in a
# sandboxed nix build (checks.<system>.go-test in flake.nix).
#
# run the Go test suite via the flake's checks output
[group('test')]
test-go:
  nix flake check --show-trace

# Fast per-package `go test` for the agent TDD dev-loop. Runs in the devshell
# (seconds, sees untracked files) rather than the nix sandbox. The
# authoritative sandboxed run is `test-go` (`nix flake check`).
# Usage: just debug-go-test-pkg internal/alfa/lint
#
# run `go test` for a single package in the devshell
[group('debug')]
debug-go-test-pkg PKG:
  go test ./{{PKG}}/...

# Evaluate a flake's `outputs` and report the exit code. Used to prove a
# repaired flake still evaluates — in particular that its outputs signature
# accepts every declared input, whose failure mode is
# `error: function 'outputs' called with unexpected argument '<name>'`.
# Usage: just debug-flake-eval .tmp/troupe-verify
#
# evaluate a flake directory's outputs, reporting the exit code
[group('debug')]
debug-flake-eval DIR:
  cd {{ DIR }} && nix flake show --no-write-lock-file 2>&1; echo "exit=$?"

# Verbose single-test dev-loop: `debug-go-test-pkg` hard-codes `/...` and takes
# no test flags, so it cannot run one test with `-v` (needed to read t.Log
# output when probing parser/CST behaviour).
# Usage: just debug-go-test-run internal/0/nixedit TestApplyBlockForm
#
# run one `go test` by name, verbosely, in the devshell
[group('debug')]
debug-go-test-run PKG RUN:
  go test ./{{PKG}} -run '{{RUN}}' -v

# format the tree in place (repair mode) via `nix fmt`
[group('codemod')]
codemod-fmt:
  nix fmt

# regenerate gomod2nix.toml after go.mod / go.sum changes
[group('build')]
build-gomod2nix:
  gomod2nix

# remove build out-links
[group('clean')]
clean-build:
  rm -rf result result-*

# Run the outputs-participation check against a flake directory, optionally
# repairing it. Pair with `debug-flake-eval` to confirm a flake that nix
# rejected with `function 'outputs' called with unexpected argument '<name>'`
# evaluates after the repair.
# Usage: just explore-lint-outputs-participation /path/to/flake --fix
# (from a shell a leading flag needs a `--` separator: ... /path/to/flake -- --fix)
#
# trim_start_match drops a leading `--` that reached ARGS as a literal, which
# Go's flag package would otherwise stop at -- silently dropping every flag
# after it while still exiting 0. See explore-lint-checks for the full note.
#
# run the outputs-participation check against a flake directory
[group('explore')]
explore-lint-outputs-participation DIR *ARGS: build-nix
  ./result/bin/doppelgang lint --flake {{ DIR }} --checks outputs-participation --format text {{ trim_start_match(ARGS, "-- ") }}; echo "exit=$?"

# Run `doppelgang lint --flake <DIR>` against an arbitrary flake
# directory. Used for ad-hoc validation of lint output against external
# fixtures (e.g. /tmp/claude-madder-* snapshots during the FDR-0002
# promotion checks). Exits 1 on findings.
#
# run `doppelgang lint` against an arbitrary flake directory
[group('explore')]
explore-lint-flake DIR: build-nix
  ./result/bin/doppelgang lint --flake {{DIR}}

# Run `doppelgang lint --online --flake <DIR>` (text output): read-only
# transitive dead-override detection, which fetches upstream flake.nix files
# over the network. DIR defaults to this repo. Used to validate transitive
# detection against real closures (e.g. issue #11's conformist/moxy/posh).
#
# run `doppelgang lint --online` against a flake directory
[group('explore')]
explore-lint-online DIR=".": build-nix
  ./result/bin/doppelgang lint --online --format text --flake {{DIR}}

# Run `doppelgang lint --fix --flake <DIR>` against an arbitrary flake
# directory: apply the follows-opportunity edits to its flake.nix and
# re-lock. Used to validate the --fix repair path end-to-end against a
# fixture flake with a known duplicate.
#
# run `doppelgang lint --fix` against an arbitrary flake directory
[group('explore')]
explore-lint-fix DIR: build-nix
  ./result/bin/doppelgang lint --fix --flake {{DIR}}

# Run `doppelgang lint --checks <CHECKS> --flake <DIR>` and report the exit
# code, for black-box validation of the --checks selector (issue #14): which
# checks gate the non-zero exit, and (with `--format ndjson`) the plan count.
# CHECKS is the comma-separated subset; pass extra args after `--`, e.g.
# `just explore-lint-checks /tmp/flake follows,dead-overrides -- --format ndjson`.
#
# The `--` stops just from claiming `--format` as its own option, and just's
# parser then consumes that separator. But a caller that already supplies one
# (the MCP just runner) delivers a literal `--` as the first arg instead --
# and Go's flag package STOPS at `--`, silently dropping every flag after it
# while still exiting 0. That reads as a clean pass, so the false result is
# worse than an error. trim_start_match drops a leading separator so both
# invocation paths behave identically.
#
# run `doppelgang lint --checks` against a flake directory and report the exit code
[group('explore')]
explore-lint-checks DIR CHECKS *ARGS: build-nix
  ./result/bin/doppelgang lint --flake {{DIR}} --checks {{CHECKS}} {{ trim_start_match(ARGS, "-- ") }}; echo "exit=$?"

# Run the nixpkgs-master convention repair against a flake directory — the
# exact shape eng's update-nix cascade invokes (issue #16). Pins the
# nixpkgs-master input in <DIR>/flake.nix to <SHA> (splices it when missing,
# rewrites when floating or when pinned to a revision other than SHA),
# editing flake.nix only (no re-lock; the caller re-locks). Reports the exit
# code. SHA must be a 40-hex nixpkgs revision.
#
# To see the same staleness detection WITHOUT repairing, pass the sha to the
# check-mode recipe instead:
#   just explore-lint-checks <DIR> nixpkgs-master -- --nixpkgs-master-sha <SHA>
#
# run the nixpkgs-master convention repair against a flake directory
[group('explore')]
explore-lint-nixpkgs-master DIR SHA: build-nix
  ./result/bin/doppelgang lint --flake {{DIR}} --checks nixpkgs-master --fix --nixpkgs-master-sha {{SHA}} --format text; echo "exit=$?"

# Print the NAME section of a rendered page (gz or plain) so the roff shapes
# `lint-man` must parse can be eyeballed. Usage: just debug-man-name-section PATH
#
# print a rendered page's NAME section
[group('debug')]
debug-man-name-section PATH:
  #!/usr/bin/env bash
  set -euo pipefail
  if [[ "{{PATH}}" == *.gz ]]; then zcat "{{PATH}}"; else cat "{{PATH}}"; fi \
    | sed -n '/^\.S[Hh][ \t]*"\{0,1\}NAME/,/^\.S[Hh][ \t]*"\{0,1\}[A-Z]/p' | head -40

# Run lexgrog(1), the man-db reference NAME parser, over every page under a
# manpath root. `lint-man`'s parsable/unparsable verdicts must agree with it;
# this is the acceptance check for that. Usage: just debug-lexgrog-corpus ROOT
#
# run lexgrog over every page under a manpath root
[group('debug')]
debug-lexgrog-corpus ROOT:
  #!/usr/bin/env bash
  set -uo pipefail
  find -L {{ROOT}}/man* -type f | sort | while read -r f; do lexgrog "$f" 2>&1 || true; done

# Run `doppelgang lint-man` against a manpath root, page, or scdoc source and
# report the exit code. The fleet acceptance run is
#   just explore-lint-man ~/.local/share/first-party-manpages/linenisgreat/man
# whose findings must agree with `debug-lexgrog-corpus` on parsability.
# Extra flags go after `--` (see explore-lint-checks for the separator note).
#
# run `doppelgang lint-man` against a manpath root or scdoc source
[group('explore')]
explore-lint-man TARGET *ARGS: build-nix
  ./result/bin/doppelgang lint-man --format text {{ trim_start_match(ARGS, "-- ") }} {{ TARGET }}; echo "exit=$?"

# Self-consume: lint this repo's own scdoc NAME lines. Fails `just` (via
# `lint`) when a page's NAME line breaks the index-builder contract.
#
# run `doppelgang lint-man` against this repo's own doc/*.scd
[group('lint')]
lint-man: build-nix
  ./result/bin/doppelgang lint-man doc

# Render a man page for inspection after editing its scdoc source in doc/.
# The pages themselves are built by nix, never by a recipe (eng-manpages(7)):
# the default package is a symlinkJoin of the binary and the compiled pages,
# so `build-nix` already put them under result/share/man. This only opens
# what nix produced. PAGE is the page name without the .gz suffix.
#
# view a man page built by nix
[group('explore')]
explore-man PAGE="doppelgang.1": build-nix
  man -l "result/share/man/man1/{{PAGE}}.gz"

# Build packages.doppelgang-static (fails if any store path survives in the
# binary, via allowedReferences = []), show that it is static, and run
# `lint-man` from it against a known-good (doc/) and a known-bad scdoc page.
# Serves the release-asset dev-loop (conformist#114).
#
# build the static binary and prove it is static and runs lint-man
[group('explore')]
explore-static-evidence:
  #!/usr/bin/env bash
  set -uo pipefail
  out=$(nix build --no-link --print-out-paths --print-build-logs '.#doppelgang-static')
  bin="$out/bin/doppelgang"
  file -L "$bin" || true
  ldd "$bin" 2>&1 || true
  nix path-info -r "$bin"
  tmp=$(mktemp -d)
  mkdir "$tmp/good" "$tmp/bad"
  cp doc/*.1.scd "$tmp/good/"
  printf 'bad(1)\n\n# NAME\n\nbad - %s\n' "$(printf 'x%.0s' $(seq 1 100))" > "$tmp/bad/bad.1.scd"
  "$bin" lint-man --format text "$tmp/good"; echo "good exit=$?"
  "$bin" lint-man --format text "$tmp/bad"; echo "bad exit=$?"

# List every /nix/store path string embedded in the static binary, with
# counts, to locate where survivors come from (debug info, embedded
# toolchain paths). Serves the release-asset dev-loop (conformist#114).
#
# show /nix/store path strings that survive in the static binary
[group('debug')]
debug-static-store-strings:
  #!/usr/bin/env bash
  set -uo pipefail
  out=$(nix build --no-link --print-out-paths '.#doppelgang-static')
  bin="$out/bin/doppelgang"
  echo "== distinct store paths (count) =="
  rg -a -o '/nix/store/[a-z0-9]{32}-[A-Za-z0-9._+-]+' "$bin" | sort | uniq -c | sort -rn
  echo "== context (first 40) =="
  rg -a -o '.{0,60}/nix/store/[a-z0-9]{32}-.{0,60}' "$bin" | head -40

# Build packages.doppelgang-static-aarch64 and show it is a static ARM aarch64
# ELF with no store references. Serves the aarch64 release asset (conformist#114).
#
# build the aarch64 static binary and show its ELF identity
[group('explore')]
explore-static-aarch64:
  #!/usr/bin/env bash
  set -uo pipefail
  out=$(nix build --no-link --print-out-paths --print-build-logs '.#doppelgang-static-aarch64')
  ls -l "$out/bin"
  file -L "$out/bin/doppelgang" || true
  nix path-info -r "$out"

# Tag a doppelgang release. The "v" prefix is added for you, so pass the
# semver without it. Usage: just tag 0.1.0 "feat: initial release"
#
# tag a doppelgang release
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

# Sed-rewrite DOPPELGANG_VERSION in version.env to the given semver. The
# version is burnt into the binary at build time via -ldflags (auto-injected
# by the amarbel-llc fork's buildGoApplication overlay), so version.env is the
# single source of truth. No-op if already at the target version. Usage:
# just bump-version 0.0.2
#
# rewrite DOPPELGANG_VERSION in version.env to the given semver
bump-version new_version:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(grep 'DOPPELGANG_VERSION=' version.env | sed 's/.*=\(.*\)/\1/')
    if [[ "$current" == "{{new_version}}" ]]; then
      gum log --level info "already at {{new_version}}"
      exit 0
    fi
    sed -i.bak 's/DOPPELGANG_VERSION='"$current"'/DOPPELGANG_VERSION={{new_version}}/' version.env && rm version.env.bak
    gum log --level info "bumped DOPPELGANG_VERSION: $current → {{new_version}}"

# Bump version.env by one semver level (bugfix, minor or major) and commit it as
# "release vX.Y.Z" on the current branch, BEFORE merging. The `release`
# post-merge target (just deploy-release) then tags the merged commit, creates
# the forge release and attaches the static binary. `bump-version` itself stays
# the pure mutation.
#
# bump version.env by one semver level and commit the release bump
bump-version-level level:
    #!/usr/bin/env bash
    set -euo pipefail
    . version.env
    cur="${DOPPELGANG_VERSION:?missing DOPPELGANG_VERSION in version.env}"
    IFS=. read -r major minor patch <<<"$cur"
    case "{{ level }}" in
        bugfix) patch=$((patch + 1)) ;;
        minor) minor=$((minor + 1)); patch=0 ;;
        major) major=$((major + 1)); minor=0; patch=0 ;;
        *) echo "bump-version-level: level must be bugfix, minor or major (got '{{ level }}')" >&2; exit 1 ;;
    esac
    new="$major.$minor.$patch"
    just bump-version "$new"
    git add version.env
    git commit -m "release v$new"
    echo "bump-version-level: committed release v$new; merging runs the release post-merge target"

# Publish the release for the version a merge landed, run by the `release`
# post-merge target. Reads version.env from the MERGED commit (SHA, default
# $SPINCLASS_MERGED_SHA). If its tag already exists it only ensures the assets, so
# every merge is a safe no-op until a new version is committed. Otherwise:
# changelog since the previous tag (minus the bump commit), a signed annotated
# tag on the merged commit (needs the signing agent), the forge release via
# smith, then the asset.
#
# tag, publish and attach assets for the version a merge landed
deploy-release sha=env_var_or_default("SPINCLASS_MERGED_SHA", "HEAD"):
    #!/usr/bin/env bash
    set -euo pipefail
    git fetch --tags origin
    sha=$(git rev-parse "{{ sha }}^{commit}")
    ver=$(git show "$sha:version.env" | sed -n 's/^export DOPPELGANG_VERSION=//p')
    [ -n "$ver" ] || { echo "deploy-release: no DOPPELGANG_VERSION in version.env at $sha" >&2; exit 1; }
    tag="v$ver"
    if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
        echo "deploy-release: $tag already exists; ensuring its assets"
        just deploy-release-assets "$ver"
        exit 0
    fi

    header="release $tag"
    prev=$(git tag --sort=-v:refname -l 'v*' --merged "$sha" | sed -n 1p)
    if [ -n "$prev" ]; then
        # The bump commit is named "$header"; it MUST NOT appear in the changelog
        # it announces (eng-versioning(7)).
        summary=$(git log --format='- %s' "$prev..$sha" | grep -vxF -- "- $header" || true)
        msg="$header"$'\n\n'"$summary"
    else
        msg="$header"
    fi

    git tag -s -m "$msg" "$tag" "$sha"
    git push origin "refs/tags/$tag"
    git tag -v "$tag"

    smith -H forge.starbrandshoes.com release -r linenisgreat/doppelgang \
        create "$header" --tag "$tag" --body "$msg"
    just deploy-release-assets "$ver"

# Attach doppelgang's portable static binary (packages.doppelgang-static) to the
# forge release for version.env's version, as doppelgang-static-x86_64-linux
# and doppelgang-static-aarch64-linux. Idempotent: no release for the version is a clean
# no-op, and an asset already attached is skipped. Uses smith's ambient forge auth.
# Run by `deploy-release` (the `release` post-merge target).
#
# attach the static release binaries to the current version's forge release
deploy-release-assets version="":
    #!/usr/bin/env bash
    set -euo pipefail
    . version.env
    ver="{{ version }}"
    ver="${ver:-${DOPPELGANG_VERSION:?missing DOPPELGANG_VERSION in version.env}}"
    release="release v$ver"
    smith_release=(smith -H forge.starbrandshoes.com release -r linenisgreat/doppelgang)
    if ! view=$("${smith_release[@]}" view --by-tag "v$ver" 2>/dev/null); then
        echo "deploy-release-assets: no forge release for v$ver yet; nothing to attach"
        exit 0
    fi
    # Build from the release's TAG, never the checkout: an asset must be the
    # binary its tag's source produces. A tag older than doppelgang-static
    # cannot produce one, so it gets none.
    rev=$(git rev-parse "v$ver^{commit}")
    flake="git+file://$(git rev-parse --show-toplevel)?rev=$rev"
    system=$(nix eval --impure --raw --expr builtins.currentSystem)
    if [ "$(nix eval "$flake#packages.$system" --apply 'p: p ? doppelgang-static')" != true ]; then
        echo "deploy-release-assets: v$ver predates doppelgang-static; nothing to attach"
        exit 0
    fi
    # attr:asset-suffix. The aarch64 binary is Go-cross-compiled (CGO off), so
    # it builds on the x86_64-linux host; a tag that predates an attr skips it.
    for pair in "doppelgang-static:x86_64-linux" "doppelgang-static-aarch64:aarch64-linux"; do
        attr="${pair%%:*}"
        asset="doppelgang-static-${pair##*:}"
        if [ "$(nix eval "$flake#packages.$system" --apply "p: p ? $attr")" != true ]; then
            echo "deploy-release-assets: v$ver has no $attr; skipping $asset"
            continue
        fi
        if grep -qxF -- "• $asset" <<<"$view" || grep -qF -- " $asset" <<<"$view"; then
            echo "deploy-release-assets: $asset already attached to $release"
            continue
        fi
        out=$(nix build --no-link --print-out-paths "$flake#$attr")
        bin=$(find "$out/bin" -type f -name doppelgang -print -quit)
        [ -n "$bin" ] || { echo "deploy-release-assets: no doppelgang binary in $out/bin" >&2; exit 1; }
        "${smith_release[@]}" asset create "$release" "$bin" "$asset"
    done

# Cut a release: must be run on master. Bumps DOPPELGANG_VERSION in version.env,
# commits the bump with a changelog-style message built from commits since
# the last v* tag, pushes master, then signs and pushes the v{{version}}
# tag. The "v" prefix is added for you, so pass the semver without it.
# Usage: just release 0.0.2
#
# Use `just tag <version> <message>` directly if you want to control the
# commit message yourself without bumping.
#
# cut a release: bump version.env, commit, push master, then sign and push the tag
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
    if ! git diff --quiet version.env; then
      git add version.env
      git commit -m "chore: release v{{version}}"
      git push origin master
      gum log --level info "pushed version.env bump to master"
    fi
    just tag "{{version}}" "$msg"
