{
  description = "doppelgang: find duplicate packages in a Nix closure, sorted by wasted bytes";

  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/e2dde111aea2c0699531dc616112a96cd55ab8b5";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        pkgs-master = import nixpkgs-master { inherit system; };

        go = pkgs-master.go_1_26;

        # Exclude non-Go-source paths so edits to docs, justfile, etc. don't
        # bust the derivation hash.
        goSrc = pkgs.lib.cleanSourceWith {
          src = ./.;
          filter =
            path: _type:
            !(pkgs.lib.hasSuffix "/justfile" path)
            && !(pkgs.lib.hasSuffix "/sweatfile" path)
            && !(pkgs.lib.hasSuffix "/README.md" path)
            && !(pkgs.lib.hasSuffix "/LICENSE" path)
            && !(pkgs.lib.hasInfix "/build/" path)
            && !(pkgs.lib.hasInfix "/.tmp/" path);
        };

        # Single source of truth for the version. `just bump-version` rewrites
        # this literal; `just release` commits the bump and tags v$VERSION.
        # The amarbel-llc/nixpkgs fork's buildGoApplication overlay reads
        # `version` and `commit` and auto-injects them as -X main.version /
        # -X main.commit ldflags, so `doppelgang version` reports both at
        # runtime.
        doppelgangVersion = "0.0.1";
        # shortRev for clean builds, dirtyShortRev for dirty trees so devshell
        # builds visibly read `dirty-abcdef` instead of impersonating a release.
        doppelgangCommit = self.shortRev or self.dirtyShortRev or "unknown";

        doppelgang = pkgs.buildGoApplication {
          pname = "doppelgang";
          version = doppelgangVersion;
          commit = doppelgangCommit;
          inherit go;
          src = goSrc;
          modules = ./gomod2nix.toml;
          subPackages = [ "cmd/doppelgang" ];
          GOTOOLCHAIN = "local";
          CGO_ENABLED = "0";
        };

        goEnv = pkgs.mkGoEnv {
          pwd = ./.;
          inherit go;
        };
      in
      {
        packages = {
          inherit doppelgang;
          default = doppelgang;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            goEnv
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs-master.gofumpt
            pkgs.just
            pkgs.nix
          ];
        };
      }
    );
}
