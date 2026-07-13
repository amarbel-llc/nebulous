# NixOS module: install nebulous (+ nebulous-cg) on PATH and run `nebulous
# fetch` on a periodic systemd timer, so a host keeps its local NewsBlur
# cache warm without a manual `nebulous fetch` invocation. Optionally also
# runs `nebulous capture` on a second timer (FDR 0001 Stage 3) — the
# multi-format capture-via-chrest gap-filling scan — when the host supplies
# chrestPackage + cuttingGardenPackage.
#
# `nebulous fetch`/`nebulous capture` are batch/sync concerns, unrelated to
# MCP serving — circus's "no systemd unit of its own" rule
# (docs/circus-host-integration.7.scd § MCP ON A HOST) is scoped to the
# moxy-stdio-child role specifically ("under Path 1... it defines no
# systemd unit of its own"), not a blanket rule. nix-cache's and madder's
# own producer modules already own non-MCP daemon units the same way; this
# module follows that precedent. `nebulous serve mcp` itself is NOT wired
# here — under Path 1 that stays a moxy stdio child, hand-wired host-side
# (circus owns the moxyfile child entry + threads environmentFile into
# moxy's own unit env, same as it does for cutting-garden today).
#
# chrest/cutting-garden are supplied as OPTION VALUES (chrestPackage /
# cuttingGardenPackage), not constructor args — unlike `self` (nebulous's
# own package, resolved from nebulous's own flake), chrest and
# cutting-garden are external packages nebulous's flake does not depend on
# (cutting-garden is only a Go/gomod2nix dependency here, not a flake
# input), so the consumer (circus, which DOES carry those flake inputs)
# passes them in the standard NixOS way: as option values on
# services.nebulous, exactly like nix-cache's `package` option takes a
# package value rather than nix-cache's flake threading its own producer
# in as a curried arg. The capture timer is created only when both are
# supplied — a host that only wants NewsBlur sync (no captures) can leave
# them unset.
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

  # Hardening + state/secret wiring shared by every nebulous oneshot
  # timer-triggered service (fetch, capture, ...) — only ExecStart and
  # the unit-specific env/after vary per caller.
  mkOneshotService =
    {
      description,
      execStart,
      extraEnv ? [ ],
      extraAfter ? [ ],
    }:
    {
      inherit description;
      after = [ "network-online.target" ] ++ extraAfter;
      wants = [ "network-online.target" ];
      unitConfig.RequiresMountsFor = [ cfg.stateDir ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = execStart;
        # nebulous#41: internal/0/madder's env_dir.MakeDefault walks up
        # from cwd looking for an ancestor `.madder` override directory
        # before falling back to standard $HOME-relative XDG paths.
        # Systemd's own default WorkingDirectory ("/") makes this
        # cwd-dependent and, in the field, crashed the whole process
        # when that walk-up found an unrelated `.madder` entry outside
        # this unit's control. Pinning cwd at stateDir removes the
        # ambiguity entirely — stateDir is purpose-built and never
        # accidentally carries a stray `.madder`.
        WorkingDirectory = cfg.stateDir;
        # HOME roots nebulous's own XDG resolution (internal/0/madder's
        # env_dir.MakeDefault + the response-cache manifest) under
        # stateDir, so both land under the ReadWritePaths grant below.
        Environment = [ "HOME=${cfg.stateDir}" ] ++ extraEnv;
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
        # ProtectSystem=strict leaves /tmp and /var/tmp read-only unless
        # this is set — nebulous-capture's chrest subprocess needs a
        # writable temp dir for its browser profile. Harmless for
        # nebulous-fetch, which doesn't touch /tmp either way.
        PrivateTmp = true;
      };
    };

  mkOneshotTimer =
    {
      description,
      onBootSec,
      onActiveSec,
    }:
    {
      inherit description;
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = onBootSec;
        OnUnitActiveSec = onActiveSec;
      };
    };
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

    chrestPackage = mkOption {
      type = types.nullOr types.package;
      default = null;
      description = ''
        The chrest package (web-page capture backend `nebulous capture`
        drives, via cutting-garden, RFC 0002/0003). Supplying this AND
        cuttingGardenPackage enables the `nebulous-capture` timer below;
        leaving either unset means this host only runs the fetch timer.
        Not a constructor arg like `self` — chrest isn't a flake input of
        nebulous's own flake, so the consumer (circus) supplies it as an
        option value from ITS OWN chrest flake input.
      '';
    };

    cuttingGardenPackage = mkOption {
      type = types.nullOr types.package;
      default = null;
      description = ''
        The cutting-garden package `nebulous capture` shells out to. See
        chrestPackage — both are required to enable the capture timer.
      '';
    };

    captureFormats = mkOption {
      type = types.listOf types.str;
      default = [ "markdown-reader" ];
      description = ''
        Capture formats for the `nebulous-capture` timer's `--formats`
        flag — passed straight through to `nebulous capture`. Extensible:
        add a format string here to capture it going forward (mirrors
        tools.DefaultCaptureFormats' Go-side default; kept in sync by
        hand since Nix can't import a Go const).
      '';
    };

    captureStoreId = mkOption {
      type = types.str;
      default = "nebulous";
      description = ''
        The madder blob-store id `nebulous capture`'s `--store` flag
        targets. Defaults to the same named store internal/0/madder
        resolves for nebulous's own response cache (Stage 1) — one store
        to back up/migrate rather than two.
      '';
    };

    captureInterval = mkOption {
      type = types.str;
      default = "6h";
      example = "1h";
      description = ''
        systemd `OnUnitActiveSec` value for the periodic `nebulous
        capture` timer. Longer than fetchInterval's default by design —
        a capture launches a real headless-browser render per eligible
        (story, format) pair and is considerably heavier than a NewsBlur
        API sync.
      '';
    };
  };

  config = lib.mkMerge [
    (mkIf cfg.enable {
      environment.systemPackages = [ cfg.package ];

      users.users.nebulous = {
        isSystemUser = true;
        group = "nebulous";
        home = cfg.stateDir;
      };
      users.groups.nebulous = { };

      systemd.tmpfiles.rules = [ "d ${cfg.stateDir} 0750 nebulous nebulous - -" ];

      systemd.timers.nebulous-fetch = mkOneshotTimer {
        description = "Periodic NewsBlur sync timer for nebulous fetch";
        onBootSec = "2min";
        onActiveSec = cfg.fetchInterval;
      };

      systemd.services.nebulous-fetch = mkOneshotService {
        description = "nebulous fetch — sync feeds/starred stories/original text from NewsBlur";
        execStart = "${cfg.package}/bin/nebulous fetch";
      };
    })
    (mkIf (cfg.enable && cfg.chrestPackage != null && cfg.cuttingGardenPackage != null) {
      # internal/alfa/capture resolves both `cutting-garden` and (inside
      # THAT subprocess) `chrest` via a bare PATH lookup, not an absolute
      # store path — unlike mcp-origin.nix's own child commands, which
      # invoke madder/cutting-garden by absolute `${pkg}/bin/...` path and
      # never touch PATH at all. Matching that PATH-free convention would
      # mean threading the resolved binary paths into nebulous's own Go
      # code (a Bin-var override, mirroring internal/0/madder's historical
      # pattern before Stage 1's in-process rewrite removed it) — deferred;
      # for now the simpler fix is a unit-scoped PATH carrying exactly the
      # two packages this timer needs, nothing else.
      systemd.timers.nebulous-capture = mkOneshotTimer {
        description = "Periodic capture-via-chrest timer for nebulous capture";
        onBootSec = "10min"; # after nebulous-fetch's own 2min anchor, so a fresh host syncs before it captures
        onActiveSec = cfg.captureInterval;
      };

      systemd.services.nebulous-capture = mkOneshotService {
        description = "nebulous capture — multi-format capture-via-chrest gap-filling scan (FDR 0001 Stage 3)";
        extraAfter = [ "nebulous-fetch.service" ];
        execStart = lib.escapeShellArgs [
          "${cfg.package}/bin/nebulous"
          "capture"
          "--formats"
          (lib.concatStringsSep "," cfg.captureFormats)
          "--store"
          cfg.captureStoreId
        ];
        extraEnv = [
          "PATH=${cfg.cuttingGardenPackage}/bin:${cfg.chrestPackage}/bin:/run/current-system/sw/bin"
        ];
      };
    })
  ];
}
