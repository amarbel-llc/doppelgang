{
  description = "doppelgang: find duplicate packages in a Nix closure, sorted by wasted bytes";

  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs.inputs.nixpkgs-master.follows = "nixpkgs-master";
    nixpkgs.inputs.treefmt-nix.follows = "treefmt-nix";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    utils.inputs.systems.follows = "nixpkgs/systems";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    treefmt-nix.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
      treefmt-nix,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        pkgs-master = import nixpkgs-master { inherit system; };

        go = pkgs.go_1_26;

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
            && !(pkgs.lib.hasSuffix "/treefmt.nix" path)
            && !(pkgs.lib.hasInfix "/build/" path)
            && !(pkgs.lib.hasInfix "/.tmp/" path);
        };

        # `nix fmt` entry point. Config lives in ./treefmt.nix.
        treefmtEval = treefmt-nix.lib.evalModule pkgs ./treefmt.nix;

        # Single source of truth for the version. `just bump-version` rewrites
        # this literal; `just release` commits the bump and tags v$VERSION.
        # The amarbel-llc/nixpkgs fork's buildGoApplication overlay reads
        # `version` and `commit` and auto-injects them as -X main.version /
        # -X main.commit ldflags, so `doppelgang version` reports both at
        # runtime.
        doppelgangVersion = "0.0.6";
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

        # `go test ./...` exposed as a flake check so `nix flake check`
        # (and `just test-go`) run the suite in a sandboxed nix build.
        # subPackages is unset so every package is built and tested, not
        # just cmd/doppelgang. Stubbing buildPhase breaks goCheckHook
        # (which depends on goBuildHook's /build/buildFlagsArray), so we
        # let the standard phases run and just enable doCheck.
        doppelgangGoTest = doppelgang.overrideAttrs (_old: {
          pname = "doppelgang-go-test";
          subPackages = null;
          doCheck = true;
        });
      in
      {
        packages = {
          inherit doppelgang;
          default = doppelgang;
        };

        checks = {
          go-test = doppelgangGoTest;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            goEnv
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs.just
            pkgs.nix
          ];
        };

        formatter = treefmtEval.config.build.wrapper;
      }
    );
}
