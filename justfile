# Build, test, format
default: build test

# Build go binary + nix package
build: build-go build-nix

build-go:
  go build -o build/doppelgang ./cmd/doppelgang

build-nix:
  nix build --show-trace

# Format + go test
test: fmt test-go

test-go *args:
  go test ./... {{args}}

# Format Go sources in place
fmt:
  gofumpt -w .
  goimports -w .

# Regenerate gomod2nix.toml after go.mod / go.sum changes
gomod2nix:
  gomod2nix

# Bump the version literal in flake.nix; commit + signed tag.
# Usage: just bump-version 0.0.2
bump-version new_version:
  #!/usr/bin/env bash
  set -euo pipefail
  sed -i -E 's/version = "[^"]+";/version = "{{new_version}}";/' flake.nix
  if git diff --quiet flake.nix; then
    echo "version already at {{new_version}}" >&2
    exit 0
  fi
  git commit flake.nix -m "bump version to {{new_version}}"

clean:
  rm -rf build result result-*
