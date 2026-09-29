{
  description = "NewsBlur MCP server";

  inputs = {
    # igloo carries the Go build helpers: godyn (buildGoAuto, the go.nix
    # manifest, godyn-go / godyn-test) and mkGoPkgs. See godyn(7).
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    nixpkgs-master.url = "github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    madder = {
      url = "https://code.linenisgreat.com/madder/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    bats = {
      url = "https://code.linenisgreat.com/bats/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    purse-first = {
      url = "https://code.linenisgreat.com/purse-first/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    tap = {
      url = "https://code.linenisgreat.com/tap/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    utils.inputs.systems.follows = "igloo/systems";
    tap.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    tap.inputs.bats.follows = "madder/bats";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    madder.inputs.purse-first.follows = "purse-first";
    tap.inputs.purse-first.follows = "purse-first";
    tap.inputs.gomod2nix.follows = "purse-first/gomod2nix";
    madder.inputs.tap.follows = "tap";
    conformist = {
      url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    madder.inputs.conformist.follows = "conformist";
    madder.inputs.bats.follows = "bats";
    purse-first.inputs.conformist.follows = "conformist";
    bats.inputs.conformist.follows = "conformist";
    # Source of the code.linenisgreat.com/cutting-garden Go module, bridged via
    # go.nix flakeInputs (its go-pkgs, RFC 0001). The transitive fleet modules
    # it shares with madder follow madder's nodes so one rev of each resolves.
    cutting-garden = {
      url = "https://code.linenisgreat.com/cutting-garden/archive/master.tar.gz";
      inputs = {
        igloo.follows = "igloo";
        nixpkgs-master.follows = "nixpkgs-master";
        flake-utils.follows = "utils";
        madder.follows = "madder";
        tap.follows = "tap";
        purse-first.follows = "purse-first";
        bats.follows = "bats";
        conformist.follows = "conformist";
        crap.follows = "madder/crap";
        hyphence.follows = "madder/hyphence";
        piggy.follows = "madder/piggy";
        tommy.follows = "madder/tommy";
        langlang.follows = "madder/langlang";
      };
    };
  };

  outputs =
    inputs@{
      conformist,
      self,
      igloo,
      utils,
      nixpkgs-master,
      madder,
      bats,
      tap,
      ...
    }:
    let
      nebulousVersion = builtins.head (
        builtins.match ".*NEBULOUS_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
      );
      nebulousCommit = self.shortRev or self.dirtyShortRev or "unknown";
    in
    {
      # System-independent module outputs (circus-host-integration(7)'s
      # producer-exports-modules convention). `self` is threaded in so
      # each module's `package` option self-defaults to this flake's own
      # nebulous build; circus adds this flake as an input and flips
      # services.nebulous.enable.
      nixosModules.default = import ./nix/nixos-module.nix self;
      homeManagerModules.default = import ./nix/home-manager-module.nix self;
    }
    // utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import igloo { inherit system; };

        pkgs-master = import nixpkgs-master { inherit system; };

        # The buildGoApplication backend's toolchain; the godyn backend
        # compiles with igloo's own pkgs.go.
        go = pkgs-master.go_1_26;

        madderPkg = madder.packages.${system}.default;

        # bats no longer reaches nebulous through the bob flake input
        # (nebulous#52 -- bob was a thin wrapper: its own `batman` package
        # was literally `bats.lib.${system}.mkBats { tap-dancer-go = ...;
        # }.default`). Calling mkBats directly here, with the same
        # tap-dancer-go nebulous's own `tap` input already builds, drops
        # the bob input and its dedup lines while keeping tap-dancer-go
        # pinned to the rest of this flake's `tap` rather than bats's own
        # vendored FOD.
        batmanPkgs = bats.lib.${system}.mkBats {
          tap-dancer-go = tap.packages.${system}.tap-dancer-go;
        };

        # RFC 0001 producer from go.nix (igloo FDR 0008): go-pkgs carry a
        # rendered go.mod and gomod2nix.toml, and go.nix's flakeInputs become
        # passthru.goFlakeInputs for consumers' inherited bridges.
        goPkgs = pkgs.mkGoPkgs {
          src = self;
          manifest = ./go.nix;
          inherit inputs;
        };

        # Self-consumption (RFC 0001 § Producer interface): build from the
        # published go-pkgs-test so the producer outputs are what the
        # binaries and tests are built from.
        buildNebulousGo =
          args:
          pkgs.buildGoAuto (
            {
              pname = "nebulous";
              src = goPkgs.go-pkgs-test;
              manifest = ./go.nix;
              inherit inputs;
              version = nebulousVersion;
              nativeArgs = {
                commit = nebulousCommit;
                # github.com/DataDog/zstd (via madder/go) is cgo-only.
                inherit (pkgs.stdenv) cc;
              };
            }
            // args
          );

        nebulous = buildNebulousGo {
          subPackages = [ "cmd/nebulous" ];
          tests = true;
          # internal/0/madder's store tests create a madder env under $HOME.
          testPreRun = ''
            export HOME="$TMPDIR"
          '';

          postInstall = ''
            $out/bin/nebulous generate-plugin $out
          '';

          bgaArgs = {
            inherit go;
            commit = nebulousCommit;
            meta = with pkgs.lib; {
              description = "NewsBlur MCP server";
              homepage = "https://code.linenisgreat.com/nebulous";
              license = licenses.mit;
            };
          };
        };

        # One-shot legacy cache migration tool; not part of the shipped
        # package, built for the bats suite and `just codemod-migrate-cache`.
        migrate-cache = buildNebulousGo {
          pname = "migrate-cache";
          subPackages = [ "cmd/migrate-cache" ];
          bgaArgs = {
            inherit go;
            commit = nebulousCommit;
          };
        };

        conformistPkg = conformist.packages.${system}.default;

        conformistEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            conformist.lib.presets.eng-go
            ./conformist.nix
          ];
          package = conformistPkg;
        };

        conformistImpureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        packages = {
          default = nebulous;
          inherit nebulous migrate-cache;
          madder = madderPkg;
          inherit (goPkgs) go-pkgs go-pkgs-test;
          conformist-impure-config = conformistImpureEval.config.build.configFile;
          conformist-pre-commit = conformistEval.config.build.preCommit;
          conformist-repair = conformistEval.config.build.repair;
        };

        # Eval-check for the exported NixOS/home-manager modules
        # (docs/features/0001-krone-live-service.md Stage 2): instantiate
        # the exported NixOS module through a minimal host and assert the
        # timer/service it renders carry the configured values through
        # correctly — a logic error in the module's option-merge or
        # config block fails this gate. Mirrors cutting-garden's own
        # checks.modules-eval (nix/nixos-module.nix + flake.nix) shape.
        # Network-free, no real binary invocation needed (unlike
        # cutting-garden's config.toml-loading test) since nebulous's
        # secret seam is a plain systemd EnvironmentFile, not a rendered
        # config file this module parses itself.
        checks.modules-eval =
          # igloo.lib.nixosSystem assumes Linux; skip on darwin rather than
          # force an evaluator that can't run there.
          if !pkgs.stdenv.hostPlatform.isLinux then
            pkgs.runCommand "nebulous-modules-eval-skipped" { } "touch \"$out\""
          else
            let
              hostConfig =
                (igloo.lib.nixosSystem {
                  inherit system;
                  modules = [
                    self.nixosModules.default
                    {
                      system.stateVersion = "25.11";
                      services.nebulous = {
                        enable = true;
                        fetchInterval = "42min";
                      };
                    }
                  ];
                }).config;
              execStart = hostConfig.systemd.services.nebulous-fetch.serviceConfig.ExecStart;
              onUnitActiveSec = hostConfig.systemd.timers.nebulous-fetch.timerConfig.OnUnitActiveSec;
              installsPackage = builtins.elem nebulous hostConfig.environment.systemPackages;

              # A second host config supplies chrestPackage/cuttingGardenPackage
              # (stand-ins — this flake carries neither as a real input) to
              # exercise the capture phase folded into nebulous-fetch itself
              # (no separate unit anymore — Change 2 reversed FDR 0001 Stage
              # 3's own timer), which the module only enables (extra
              # -formats/-store flags + PATH prepend) when both are non-null.
              captureHostConfig =
                (igloo.lib.nixosSystem {
                  inherit system;
                  modules = [
                    self.nixosModules.default
                    {
                      system.stateVersion = "25.11";
                      services.nebulous = {
                        enable = true;
                        chrestPackage = pkgs.hello;
                        cuttingGardenPackage = pkgs.hello;
                        captureFormats = [
                          "markdown-reader"
                          "pdf"
                        ];
                        captureStoreId = "nebulous";
                        captureInterval = "3h";
                      };
                    }
                  ];
                }).config;
              captureExecStart = captureHostConfig.systemd.services.nebulous-fetch.serviceConfig.ExecStart;
              captureEnvJoined = pkgs.lib.concatStringsSep "\n" captureHostConfig.systemd.services.nebulous-fetch.serviceConfig.Environment;
            in
            pkgs.runCommand "nebulous-modules-eval"
              {
                inherit
                  execStart
                  onUnitActiveSec
                  captureExecStart
                  captureEnvJoined
                  ;
                installsPackage = if installsPackage then "1" else "";
              }
              ''
                echo "--- nebulous-fetch.serviceConfig.ExecStart (sync-only host) ---"
                echo "$execStart"
                echo "$execStart" | grep -q '/bin/nebulous fetch'
                if echo "$execStart" | grep -q -- '-formats'; then
                  echo "sync-only ExecStart unexpectedly carries -formats (chrestPackage/cuttingGardenPackage unset)" >&2
                  exit 1
                fi

                echo "--- nebulous-fetch timer OnUnitActiveSec ---"
                echo "$onUnitActiveSec"
                [ "$onUnitActiveSec" = "42min" ]

                [ -n "$installsPackage" ] || {
                  echo "nebulous package missing from environment.systemPackages" >&2
                  exit 1
                }

                echo "--- nebulous-fetch.serviceConfig.ExecStart (capture-enabled host) ---"
                echo "$captureExecStart"
                echo "$captureExecStart" | grep -q '/bin/nebulous fetch'
                echo "$captureExecStart" | grep -q -- '-formats'
                echo "$captureExecStart" | grep -q 'markdown-reader,pdf'
                echo "$captureExecStart" | grep -q -- '-store'
                echo "$captureExecStart" | grep -q 'nebulous$'

                echo "--- nebulous-fetch.serviceConfig.Environment (capture-enabled host) ---"
                echo "$captureEnvJoined"
                echo "$captureEnvJoined" | grep -q 'NEBULOUS_CAPTURE_INTERVAL=3h'
                echo "$captureEnvJoined" | grep -q '^PATH='
                echo "$captureEnvJoined" | grep -q '/bin:'

                touch "$out"
              '';

        # godyn lanes from go.nix, on systems where buildGoAuto chose the
        # godyn backend: per-package tests, vet, and godyn-lint (vet passes +
        # staticcheck defaults, //nolint honored). `just test-go` and
        # `just lint-go` build these.
        checks.nebulous-tests = nebulous.passthru.checkAll or nebulous;
        checks.vet = nebulous.passthru.vetAll or nebulous;
        checks.lint = nebulous.passthru.lintAll or nebulous;

        devShells.default = pkgs-master.mkShell {
          packages = [
            # No ambient go: dependencies live in go.nix (igloo FDR 0008); go
            # commands run through godyn-go (`just codemod-go`), tests through
            # godyn-test.
            pkgs.godyn-go
            pkgs.godyn-test
            pkgs.just
            pkgs.bats
            pkgs.shellcheck
            pkgs.shfmt
            madderPkg
            inputs.purse-first.packages.${system}.dagnabit
            batmanPkgs.default
            conformistPkg
            conformistEval.config.build.preCommit
            conformistEval.config.build.repair
          ];

          shellHook = ''
            export BATS_LIB_PATH=${batmanPkgs.default}/share/bats
          '';
        };

        formatter = conformistEval.config.build.wrapper;
        checks.formatting = conformistEval.config.build.check self;
      }
    );
}
