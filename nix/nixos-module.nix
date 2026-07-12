# NixOS module: install nebulous (+ nebulous-cg) on PATH and run `nebulous
# fetch` on a periodic systemd timer, so a host keeps its local NewsBlur
# cache warm without a manual `nebulous fetch` invocation.
#
# `nebulous fetch` is a batch/sync concern, unrelated to MCP serving —
# circus's "no systemd unit of its own" rule
# (docs/circus-host-integration.7.scd § MCP ON A HOST) is scoped to the
# moxy-stdio-child role specifically ("under Path 1... it defines no
# systemd unit of its own"), not a blanket rule. nix-cache's and madder's
# own producer modules already own non-MCP daemon units the same way; this
# module follows that precedent. `nebulous serve mcp` itself is NOT wired
# here — under Path 1 that stays a moxy stdio child, hand-wired host-side
# (circus owns the moxyfile child entry + threads environmentFile into
# moxy's own unit env, same as it does for cutting-garden today).
#
# Imported via the flake's nixosModules.default, which passes `self`.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.nebulous;
  inherit (lib)
    mkIf
    mkOption
    mkEnableOption
    types
    ;
in
{
  options.services.nebulous = {
    enable = mkEnableOption "nebulous's periodic NewsBlur sync (nebulous fetch) on this host";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "nebulous.packages.\${system}.default";
      description = "The nebulous package to install (provides nebulous + nebulous-cg).";
    };

    environmentFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/run/nebulous/secrets.env";
      description = ''
        Path to a file of `VAR=value` lines providing `NEWSBLUR_TOKEN` (the
        NewsBlur session cookie `nebulous fetch` needs). Provisioned
        out-of-band (e.g. from piggy) and hand-placed root-only, following
        circus's established secret pattern (nix-cache `secretKeyFile`,
        cutting-garden `environmentFile`) — circus does not use
        sops/agenix. The plaintext token never enters the Nix store.

        Threaded into this module's own `nebulous-fetch` service below. A
        consumer wiring nebulous's MCP as a moxy stdio child threads the
        SAME file into moxy's unit environment separately (this module
        does not do that itself — see the module-level doc comment).
      '';
    };

    stateDir = mkOption {
      type = types.str;
      default = "/var/lib/nebulous";
      description = ''
        HOME for the fetch service, so nebulous's XDG-resolved local cache
        (the madder blob store + response manifest) lands under it. Also a
        natural bind-mount target for a durable volume on hosts that want
        the cache to survive a server recreate — mirrors the
        /var/lib/forgejo <- durable-volume pattern circus already uses
        elsewhere. Deliberately created via systemd.tmpfiles (below)
        rather than a per-unit `StateDirectory=`, so a later bind-mount
        onto this same path doesn't fight systemd's own directory
        management (circus has hit this exact ordering gotcha before).
      '';
    };

    fetchInterval = mkOption {
      type = types.str;
      default = "1h";
      example = "30min";
      description = ''
        systemd `OnUnitActiveSec` value for the periodic `nebulous fetch`
        timer.
      '';
    };
  };

  config = mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ];

    users.users.nebulous = {
      isSystemUser = true;
      group = "nebulous";
      home = cfg.stateDir;
    };
    users.groups.nebulous = { };

    systemd.tmpfiles.rules = [ "d ${cfg.stateDir} 0750 nebulous nebulous - -" ];

    systemd.timers.nebulous-fetch = {
      description = "Periodic NewsBlur sync timer for nebulous fetch";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "2min";
        OnUnitActiveSec = cfg.fetchInterval;
      };
    };

    systemd.services.nebulous-fetch = {
      description = "nebulous fetch — sync feeds/starred stories/original text from NewsBlur";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      unitConfig.RequiresMountsFor = [ cfg.stateDir ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = "${cfg.package}/bin/nebulous fetch";
        # HOME roots nebulous's own XDG resolution (internal/0/madder's
        # env_dir.MakeDefault + the response-cache manifest) under
        # stateDir, so both land under the ReadWritePaths grant below.
        Environment = [ "HOME=${cfg.stateDir}" ];
        # Optional ('-'): the unit still starts (and fails fast with a
        # clear auth error) when the secret hasn't been provisioned yet,
        # matching mcp-origin.nix's caldavEnvFile convention.
        EnvironmentFile = mkIf (cfg.environmentFile != null) [ "-${cfg.environmentFile}" ];
        User = "nebulous";
        Group = "nebulous";
        # A oneshot timer-triggered job: a failed run is retried by the
        # NEXT timer tick, not by systemd's own Restart= machinery.
        Restart = "no";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ReadWritePaths = [ cfg.stateDir ];
        ProtectHome = true;
      };
    };
  };
}
