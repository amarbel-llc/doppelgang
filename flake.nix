{
  description = "doppelgang: find duplicate packages in a Nix closure, sorted by wasted bytes";

  inputs = {
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    nixpkgs-master.url = "github:NixOS/nixpkgs/567a49d1913ce81ac6e9582e3553dd90a955875f";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    utils.inputs.systems.follows = "igloo/systems";

    # conformist provides the linter/formatter multiplexer, its Nix module
    # library (conformist.lib), and the eng-convention presets. Consumed from
    # the forge (linenisgreat/conformist); no github: deferral applies.
    conformist = {
      url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
      conformist,
    }:
    let
      # version.env at repo root is the single source of truth for the release
      # version. Burnt into the binary via the fork's auto-injected -ldflags
      # (-X main.version / -X main.commit).
      doppelgangVersion = builtins.head (
        builtins.match ".*DOPPELGANG_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
      );
      # shortRev for clean builds, dirtyShortRev for dirty trees so devshell
      # builds visibly read `dirty-abcdef` instead of impersonating a release.
      doppelgangCommit = self.shortRev or self.dirtyShortRev or "unknown";
    in
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import igloo { inherit system; };
        pkgs-master = import nixpkgs-master { inherit system; };

        go = pkgs.go_1_26;

        conformistPkg = conformist.packages.${system}.default;

        # Pure lane: the eng presets (+ the canonical goimports->gofumpt chain)
        # and this repo's overlay (./conformist.nix). Drives `nix fmt` and the
        # sandboxed `checks.formatting`.
        conformistEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            conformist.lib.presets.eng-go
            ./conformist.nix
          ];
          package = conformistPkg;
        };

        # Impure lane: the git-state checks (git-remotes, sweatfile, agents-md,
        # gomod2nix) run against the working tree via `just lint-worktree`.
        conformistImpureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };

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
        doppelgangGoTest = doppelgang.overrideAttrs (_old: {
          pname = "doppelgang-go-test";
          subPackages = null;
          doCheck = true;
        });
      in
      {
        formatter = conformistEval.config.build.wrapper;

        packages = {
          inherit doppelgang;
          default = doppelgang;
          conformist-impure-config = conformistImpureEval.config.build.configFile;
          conformist-pre-commit = conformistEval.config.build.preCommit;
          conformist-repair = conformistEval.config.build.repair;
        };

        checks = {
          formatting = conformistEval.config.build.check self;
          go-test = doppelgangGoTest;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            conformistPkg
            conformistEval.config.build.preCommit
            conformistEval.config.build.repair
            goEnv
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs.just
            pkgs.nix
          ];
        };
      }
    );
}
