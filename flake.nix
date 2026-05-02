{
  description = "NewsBlur MCP server";

  inputs = {
    # amarbel-llc/nixpkgs is an overlay flake on top of upstream
    # nixpkgs/master. Its `legacyPackages` exposes the gomod2nix build
    # helpers (`buildGoApplication`, `mkGoEnv`, `gomod2nix` CLI) alongside
    # the standard nixpkgs surface, with `allowUnfree = true`. See
    # `man 7 gomod2nix` inside the devshell for the migration guide.
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    madder = {
      url = "github:amarbel-llc/madder";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs/nixpkgs";
      inputs.utils.follows = "utils";
    };
    chrest = {
      url = "github:amarbel-llc/chrest";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs/nixpkgs";
      inputs.utils.follows = "utils";
    };
    bob = {
      url = "github:amarbel-llc/bob";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs/nixpkgs";
      inputs.utils.follows = "utils";
    };
    purse-first = {
      url = "github:amarbel-llc/purse-first";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs/nixpkgs";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      madder,
      chrest,
      bob,
      purse-first,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Single source of truth for the Go toolchain — threaded into
        # both buildGoApplication and mkGoEnv so the build-time and
        # devshell versions stay in lockstep.
        go = pkgs.go_1_26;

        version = "0.1.0";

        madderPkg = madder.packages.${system}.default;
        chrestPkg = chrest.packages.${system}.default;

        nebulous = pkgs.buildGoApplication {
          pname = "nebulous";
          inherit version go;
          src = ./.;
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
        };

        devShells.default = pkgs.mkShell {
          packages = [
            # mkGoEnv propagates the pinned go toolchain, the
            # gomod2nix CLI, and the go-sync-wrap hook that auto-
            # regenerates gomod2nix.toml after `go get` / `go mod tidy`.
            (pkgs.mkGoEnv { pwd = ./.; inherit go; })
            pkgs.delve
            pkgs.gofumpt
            pkgs.golangci-lint
            pkgs.golines
            pkgs.gopls
            pkgs.gotools
            pkgs.govulncheck
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
