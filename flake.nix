{
  description = "NewsBlur MCP server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/4590696c8693fea477850fe379a01544293ca4e2";
    nixpkgs-master.url = "github:NixOS/nixpkgs/e2dde111aea2c0699531dc616112a96cd55ab8b5";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      gomod2nix,
      nixpkgs-master,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            gomod2nix.overlays.default
          ];
        };

        pkgs-master = import nixpkgs-master { inherit system; };

        version = "0.1.0";

        nebulous = pkgs.buildGoApplication {
          pname = "nebulous";
          inherit version;
          src = ./.;
          modules = ./gomod2nix.toml;
          go = pkgs-master.go_1_26;

          subPackages = [ "cmd/nebulous" ];

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
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            pkgs-master.go_1_26
            pkgs-master.delve
            pkgs-master.gofumpt
            pkgs-master.golangci-lint
            pkgs-master.golines
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.govulncheck
            gomod2nix.packages.${system}.default
            pkgs.just
            pkgs.bats
            pkgs.shellcheck
            pkgs.shfmt
          ];
        };
      }
    );
}
