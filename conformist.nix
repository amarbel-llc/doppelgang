# doppelgang's conformist overlay, merged with conformist.lib.presets.{eng,eng-go}
# in flake.nix (conformist.lib.evalModule). presets.eng enables the
# eng-convention linters (eng-versioning, flake-outputs/lock, the justfile-*
# roster); presets.eng-go carries the canonical goimports -> gofumpt chain.
# Here live nixfmt, the eng-versioning key, and repo-specific excludes.
{ ... }:
{
  programs.nixfmt.enable = true;

  linters.eng-versioning.key = "DOPPELGANG_VERSION";

  settings.excludes = [
    "*.md"
    "flake.lock"
    "go.sum"
    "gomod2nix.toml"
    "LICENSE"
    "result"
    "result-*"
    ".tmp/**"
  ];
}
