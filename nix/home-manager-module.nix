# home-manager module: install nebulous into a user environment and, on
# request, wire it into the user's local MCP stack so a session loads nebulous
# from here instead of through a remote connector.
#
#   moxy.enable          render a moxyfile holding the `nebulous serve mcp` child
#                        (include it from your moxyfile, see moxy.moxyfile)
#   cuttingGarden.enable add the newsblur:// wire plugin to programs.cutting-garden
#                        (cutting-garden's own home-manager module must be imported)
#   fetch.enable         a systemd user timer running `nebulous fetch`, since
#                        `serve mcp` and the plugin read the store fetch populates
#
# No credential is wired anywhere: the package's bin/nebulous reads the NewsBlur
# token itself with the `piggy` the package carries (NEWSBLUR_TOKEN, when set,
# still wins). The periodic-fetch timer for headless hosts lives in
# nix/nixos-module.nix. Mirrors cutting-garden's home-manager module shape.
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
    mkMerge
    mkOption
    mkEnableOption
    types
    ;

  nebulousBin = "${cfg.package}/bin/nebulous";

  moxyfile = pkgs.writeText "nebulous.moxyfile" ''
    [[servers]]
    name = "nebulous"
    command = ["${nebulousBin}", "serve", "mcp"]
  '';

  # A `programs.cutting-garden.plugins` entry: `command` is the base invocation,
  # the host appends `traversal-serve` itself (cutting-garden's config.nix).
  cuttingGardenStanza = {
    name = "nebulous";
    command = [ nebulousBin ];
    schemes = [ "newsblur" ];
    protocols = [ "traversal" ];
  };
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

    moxy = {
      enable = mkEnableOption "a moxyfile with the `nebulous serve mcp` child, at ~/.config/moxy/nebulous.moxyfile";

      moxyfile = mkOption {
        type = types.str;
        readOnly = true;
        default = "${config.xdg.configHome}/moxy/nebulous.moxyfile";
        defaultText = lib.literalExpression ''"''${config.xdg.configHome}/moxy/nebulous.moxyfile"'';
        description = ''
          Where the rendered moxyfile lives once `moxy.enable` is set. Add it to
          your own moxyfile's `include` list (moxyfile(5)) to get the child.
        '';
      };
    };

    cuttingGarden.enable = mkEnableOption ''
      the newsblur:// wire plugin in `programs.cutting-garden.plugins`
      (requires cutting-garden's home-manager module)
    '';

    fetch = {
      enable = mkEnableOption "a systemd user timer running `nebulous fetch`";

      interval = mkOption {
        type = types.str;
        default = "1h";
        description = "systemd `OnUnitActiveSec` for the fetch timer.";
      };

      extraArgs = mkOption {
        type = types.listOf types.str;
        default = [ "-no-capture" ];
        description = ''
          Arguments appended to `nebulous fetch`. The capture phase drives
          cutting-garden and chrest by PATH, which a user unit does not carry,
          so it is skipped by default.
        '';
      };
    };
  };

  config = mkIf cfg.enable (mkMerge [
    { home.packages = [ cfg.package ]; }

    (mkIf cfg.moxy.enable {
      xdg.configFile."moxy/nebulous.moxyfile".source = moxyfile;
    })

    (mkIf cfg.cuttingGarden.enable {
      programs.cutting-garden.plugins = [ cuttingGardenStanza ];
    })

    (mkIf cfg.fetch.enable {
      systemd.user.services.nebulous-fetch = {
        Unit.Description = "nebulous fetch: sync feeds, starred stories and original text from NewsBlur";
        Service = {
          Type = "oneshot";
          ExecStart = lib.escapeShellArgs (
            [
              nebulousBin
              "fetch"
            ]
            ++ cfg.fetch.extraArgs
          );
        };
      };
      systemd.user.timers.nebulous-fetch = {
        Unit.Description = "Periodic nebulous fetch";
        Timer = {
          OnBootSec = "2min";
          OnUnitActiveSec = cfg.fetch.interval;
        };
        Install.WantedBy = [ "timers.target" ];
      };
    })
  ]);
}
