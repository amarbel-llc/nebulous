{
  description = "NewsBlur MCP server";

  inputs = {
    # amarbel-llc/nixpkgs carries the gomod2nix build helpers natively
    # (pkgs.buildGoApplication, pkgs.mkGoEnv, pkgs.gomod2nix CLI). See
    # `man 7 gomod2nix` inside the devshell for the migration guide.
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs/567a49d1913ce81ac6e9582e3553dd90a955875f";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    madder = {
      url = "github:amarbel-llc/madder";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    bob = {
      url = "github:amarbel-llc/bob";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    purse-first = {
      url = "github:amarbel-llc/purse-first";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    tap = {
      url = "github:amarbel-llc/tap";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    bob.inputs.tap.inputs.bats.follows = "bob/bats";
    bob.inputs.gomod2nix.inputs.nixpkgs-master.follows = "bob/bats/nixpkgs-master";
    bob.inputs.purse-first.inputs.nixpkgs-master.follows = "bob/bats/nixpkgs-master";
    bob.inputs.tap.inputs.nixpkgs-master.follows = "bob/bats/nixpkgs-master";
    bob.inputs.purse-first.inputs.gomod2nix.follows = "bob/gomod2nix";
    bob.inputs.tap.inputs.gomod2nix.follows = "bob/gomod2nix";
    bob.inputs.tap.inputs.purse-first.follows = "bob/purse-first";
    bob.inputs.tap.inputs.rust-overlay.follows = "bob/rust-overlay";
    utils.inputs.systems.follows = "igloo/systems";
    tap.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    bob.inputs.bats.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    bob.inputs.tap.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    madder.inputs.bats.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    tap.inputs.bats.follows = "madder/bats";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    madder.inputs.purse-first.follows = "purse-first";
    tap.inputs.purse-first.follows = "purse-first";
    tap.inputs.gomod2nix.follows = "purse-first/gomod2nix";
    madder.inputs.tap.follows = "tap";
    bob.inputs.bats.inputs.utils.follows = "utils";
    bob.inputs.gomod2nix.inputs.flake-utils.follows = "utils";
    bob.inputs.purse-first.inputs.utils.follows = "utils";
    bob.inputs.tap.inputs.utils.follows = "utils";
  };

  outputs =
    {
      self,
      igloo,
      utils,
      nixpkgs-master,
      madder,
      bob,
      purse-first,
      tap,
    }:
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

        # Single source of truth for the Go toolchain — threaded into
        # both buildGoApplication and mkGoEnv so the build-time and
        # devshell versions stay in lockstep.
        go = pkgs-master.go_1_26;

        version = "0.1.0";

        madderPkg = madder.packages.${system}.default;

        gomod = import ./gomod.nix {
          inherit
            pkgs
            system
            tap
            purse-first
            ;
          src = self;
        };

        # Self-consumption SHOULD (RFC 0001 § Producer interface):
        # point our own buildGoApplication src/pwd at the published
        # go-pkgs-test so checkPhase becomes the contract test for
        # the producer outputs. Drift between the worktree and the
        # filtered tree fails the build instead of slipping through
        # to downstream consumers.
        nebulous = pkgs.buildGoApplication {
          pname = "nebulous";
          inherit version go;
          src = gomod.goPkgs.go-pkgs-test;
          pwd = gomod.goPkgs.go-pkgs-test;
          modules = ./gomod2nix.toml;
          inherit (gomod) goFlakeInputs;

          subPackages = [
            "cmd/nebulous"
            "cmd/nebulous-cg"
          ];

          postInstall = ''
            $out/bin/nebulous generate-plugin $out
          '';

          meta = with pkgs.lib; {
            description = "NewsBlur MCP server";
            homepage = "https://github.com/friedenberg/nebulous";
            license = licenses.mit;
          };
        };
      in
      {
        packages = {
          default = nebulous;
          inherit nebulous;
          madder = madderPkg;
          inherit (gomod.goPkgs) go-pkgs go-pkgs-test;
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
            in
            pkgs.runCommand "nebulous-modules-eval"
              {
                inherit execStart onUnitActiveSec;
                installsPackage = if installsPackage then "1" else "";
              }
              ''
                echo "--- nebulous-fetch.serviceConfig.ExecStart ---"
                echo "$execStart"
                echo "$execStart" | grep -q '/bin/nebulous fetch'

                echo "--- nebulous-fetch timer OnUnitActiveSec ---"
                echo "$onUnitActiveSec"
                [ "$onUnitActiveSec" = "42min" ]

                [ -n "$installsPackage" ] || {
                  echo "nebulous package missing from environment.systemPackages" >&2
                  exit 1
                }

                touch "$out"
              '';

        devShells.default = pkgs-master.mkShell {
          packages = [
            # mkGoEnv propagates the pinned go toolchain, the
            # gomod2nix CLI, and the go-sync-wrap hook that auto-
            # regenerates gomod2nix.toml after `go get` / `go mod tidy`.
            (pkgs.mkGoEnv {
              pwd = ./.;
              inherit go;
              inherit (gomod) goFlakeInputs;
            })
            pkgs-master.delve
            pkgs-master.gofumpt
            pkgs-master.golangci-lint
            pkgs-master.golines
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.govulncheck
            pkgs.just
            pkgs.bats
            pkgs.shellcheck
            pkgs.shfmt
            madderPkg
            purse-first.packages.${system}.dagnabit
            bob.packages.${system}.batman
          ];

          shellHook = ''
            export BATS_LIB_PATH=${bob.packages.${system}.batman}/share/bats
          '';
        };
      }
    );
}
