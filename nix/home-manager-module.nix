# home-manager module: install nebulous (+ nebulous-cg) into a user
# environment. `nebulous fetch`/`nebulous-cg`/`nebulous serve mcp` are run
# interactively or launched by an MCP client (claude/clown, or a user moxy
# child) on a personal workstation, so this module manages the package
# only — there is no long-running or periodic unit to define here (the
# periodic-fetch timer is a host concern; see nix/nixos-module.nix). This
# is the workstation counterpart of the headless NixOS module, mirroring
# cutting-garden's own home-manager module shape.
#
# Imported via the flake's homeManagerModules.default, which passes `self`
# so `package` can self-default to the flake's own nebulous build.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.nebulous;
  inherit (lib)
    mkIf
    mkOption
    mkEnableOption
    types
    ;
in
{
  options.programs.nebulous = {
    enable = mkEnableOption "nebulous's binaries for this user";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "nebulous.packages.\${system}.default";
      description = "The nebulous package to install into the user environment.";
    };
  };

  config = mkIf cfg.enable {
    home.packages = [ cfg.package ];
  };
}
