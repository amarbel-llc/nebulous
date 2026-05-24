{
  description = "NewsBlur MCP server";

  inputs = {
    # amarbel-llc/nixpkgs carries the gomod2nix build helpers natively
    # (pkgs.buildGoApplication, pkgs.mkGoEnv, pkgs.gomod2nix CLI). See
    # `man 7 gomod2nix` inside the devshell for the migration guide.
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    madder = {
      url = "github:amarbel-llc/madder";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    chrest = {
      url = "github:amarbel-llc/chrest";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    bob = {
      url = "github:amarbel-llc/bob";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    purse-first = {
      url = "github:amarbel-llc/purse-first";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      nixpkgs-master,
      madder,
      chrest,
      bob,
      purse-first,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        pkgs-master = import nixpkgs-master { inherit system; };

        # Single source of truth for the Go toolchain — threaded into
        # both buildGoApplication and mkGoEnv so the build-time and
        # devshell versions stay in lockstep.
        go = pkgs-master.go_1_26;

        version = "0.1.0";

        madderPkg = madder.packages.${system}.default;
        chrestPkg = chrest.packages.${system}.default;

        gomod = import ./gomod.nix {
          inherit pkgs;
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

          subPackages = [ "cmd/nebulous" ];

          ldflags = [
            "-X github.com/friedenberg/nebulous/internal/0/madder.Bin=${madderPkg}/bin/madder"
            "-X github.com/friedenberg/nebulous/internal/alfa/capturer.Bin=${chrestPkg}/bin/chrest"
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
          chrest = chrestPkg;
          madder = madderPkg;
          inherit (gomod.goPkgs) go-pkgs go-pkgs-test;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            # mkGoEnv propagates the pinned go toolchain, the
            # gomod2nix CLI, and the go-sync-wrap hook that auto-
            # regenerates gomod2nix.toml after `go get` / `go mod tidy`.
            (pkgs.mkGoEnv { pwd = ./.; inherit go; })
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
            chrestPkg
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
