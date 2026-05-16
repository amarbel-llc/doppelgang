# treefmt-nix configuration. Run via `nix fmt`.
{
  projectRootFile = "flake.nix";

  # Go: goimports → gofumpt chain. Lower priority runs first; goimports must
  # run before gofumpt so the import-grouped output is then re-canonicalized
  # by gofumpt.
  programs.goimports.enable = true;
  settings.formatter.goimports.priority = 1;
  programs.gofumpt.enable = true;
  settings.formatter.gofumpt.priority = 2;

  programs.nixfmt.enable = true;

  settings.global.excludes = [
    "flake.lock"
    "go.sum"
    "gomod2nix.toml"
    "LICENSE"
    "*.md"
  ];
}
