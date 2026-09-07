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
            && !(pkgs.lib.hasInfix "/doc/" path)
            && !(pkgs.lib.hasInfix "/.tmp/" path);
        };

        doppelgangBin = pkgs.buildGoApplication {
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

        # Man pages, compiled from the scdoc sources in ./doc. Kept a separate
        # derivation from the Go build (rather than a postInstall on it) so
        # editing a man page does not bust the Go derivation's hash — the same
        # reason goSrc filters ./doc out. Per eng-manpages(7), man pages are
        # built by Nix, never by a justfile recipe or CI.
        doppelgangDoc = pkgs.stdenvNoCC.mkDerivation {
          pname = "doppelgang-doc";
          version = doppelgangVersion;
          src = ./doc;
          nativeBuildInputs = [ pkgs.scdoc ];
          dontUnpack = true;
          dontBuild = true;
          installPhase = ''
            mkdir -p $out/share/man/man1
            for f in $src/*.1.scd; do
              [ -e "$f" ] || continue
              scdoc < "$f" > "$out/share/man/man1/$(basename "$f" .scd)"
            done
          '';
        };

        # What `nix build` and `nix run` resolve to: the binary plus its man
        # pages under one prefix, so `result/share/man` sits alongside
        # `result/bin`.
        doppelgang = pkgs.symlinkJoin {
          name = "doppelgang-${doppelgangVersion}";
          paths = [
            doppelgangBin
            doppelgangDoc
          ];
          meta.mainProgram = "doppelgang";
        };

        goEnv = pkgs.mkGoEnv {
          pwd = ./.;
          inherit go;
        };

        # `go test ./...` exposed as a flake check so `nix flake check`
        # (and `just test-go`) run the suite in a sandboxed nix build.
        # Overrides the raw Go derivation, not the symlinkJoin.
        doppelgangGoTest = doppelgangBin.overrideAttrs (_old: {
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
          # The man pages alone, for consumers that want the docs without the
          # binary's closure.
          doppelgang-doc = doppelgangDoc;
          conformist-impure-config = conformistImpureEval.config.build.configFile;
          # The raw conformist binary, so `just lint-worktree` can
          # `nix run .#conformist -- check ...` instead of resolving
          # `conformist` from PATH — where eng's cwd-aware wrapper
          # (conformistCwd) would shadow it and refuse, since it gates on a
          # checked-in conformist.toml that no eng repo carries.
          conformist = conformistPkg;
          conformist-pre-commit = conformistEval.config.build.preCommit;
          conformist-repair = conformistEval.config.build.repair;
        };

        checks = {
          formatting = conformistEval.config.build.check self;
          go-test = doppelgangGoTest;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            # Puts doppelgang(1) on the devShell's MANPATH, so `man doppelgang`
            # works in-tree without a `nix build` first.
            doppelgangDoc
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
