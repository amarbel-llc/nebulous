{
  description = "NewsBlur MCP server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/6d41bc27aaf7b6a3ba6b169db3bd5d6159cfaa47";
    nixpkgs-master.url = "github:NixOS/nixpkgs/5b7e21f22978c4b740b3907f3251b470f466a9a2";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    go = {
      url = "github:amarbel-llc/purse-first?dir=devenvs/go";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    shell = {
      url = "github:amarbel-llc/purse-first?dir=devenvs/shell";
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
      go,
      shell,
      nixpkgs-master,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            go.overlays.default
          ];
        };

        version = "0.1.0";

        nebulous = pkgs.buildGoModule {
          pname = "nebulous";
          inherit version;
          src = ./.;
          vendorHash = "sha256-vPk1SsLO4F2/Fy4jgOVzzR5TQgjl+/WHAV5Pf1Jw1+Q=";

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

        devShells.default = pkgs.mkShell {
          inputsFrom = [
            go.devShells.${system}.default
            shell.devShell.${system}
          ];
        };
      }
    );
}
