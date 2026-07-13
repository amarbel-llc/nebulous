# NixOS module: install nebulous (+ nebulous-cg) on PATH and run `nebulous
# fetch` on a single periodic systemd timer, so a host keeps its local
# NewsBlur cache warm without a manual `nebulous fetch` invocation.
# `nebulous fetch` itself now folds in the multi-format capture-via-chrest
# gap-filling scan (originally FDR 0001 Stage 3's own separate timer,
# reversed once the watermark + per-(hash,format) completion-record
# idempotency built for that scan made a no-op pass cheap enough to run
# from inside the same command) — supplying chrestPackage +
# cuttingGardenPackage enables it by threading -formats/-store flags and a
# PATH prepend onto this same nebulous-fetch service; leaving either unset
# means fetch runs sync-only (the Go binary's own PATH lookup soft-skips
# the capture phase when cutting-garden isn't found).
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
# chrest/cutting-garden are supplied as OPTION VALUES (chrestPackage /
# cuttingGardenPackage), not constructor args — unlike `self` (nebulous's
# own package, resolved from nebulous's own flake), chrest and
# cutting-garden are external packages nebulous's flake does not depend on
# (cutting-garden is only a Go/gomod2nix dependency here, not a flake
# input), so the consumer (circus, which DOES carry those flake inputs)
# passes them in the standard NixOS way: as option values on
# services.nebulous, exactly like nix-cache's `package` option takes a
# package value rather than nix-cache's flake threading its own producer
# in as a curried arg. The capture phase only runs when both are
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

  # The capture phase (folded into nebulous-fetch, no separate unit) is
  # only enabled when both packages are supplied — bound once so the
  # ExecStart flags and the PATH/env wiring below can't drift apart from
  # checking the same condition twice.
  captureEnabled = cfg.chrestPackage != null && cfg.cuttingGardenPackage != null;

  # Hardening + state/secret wiring for nebulous-fetch's oneshot
  # timer-triggered service.
  mkOneshotService =
    {
      description,
      execStart,
      extraEnv ? [ ],
    }:
    {
      inherit description;
      after = [ "network-online.target" ];
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
        # this is set — the folded-in capture phase's chrest subprocess
        # needs a writable temp dir for its browser profile. Harmless
        # when the capture phase is disabled (chrestPackage/
        # cuttingGardenPackage unset), since fetch doesn't touch /tmp
        # either way.
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
        The chrest package (web-page capture backend the capture phase
        drives, via cutting-garden, RFC 0002/0003). Supplying this AND
        cuttingGardenPackage enables the capture phase folded into
        nebulous-fetch below; leaving either unset means this host only
        runs the NewsBlur sync (the Go binary's own PATH lookup
        soft-skips the capture phase when cutting-garden isn't found).
        Not a constructor arg like `self` — chrest isn't a flake input of
        nebulous's own flake, so the consumer (circus) supplies it as an
        option value from ITS OWN chrest flake input.
      '';
    };

    cuttingGardenPackage = mkOption {
      type = types.nullOr types.package;
      default = null;
      description = ''
        The cutting-garden package the capture phase shells out to. See
        chrestPackage — both are required to enable it.
      '';
    };

    captureFormats = mkOption {
      type = types.listOf types.str;
      default = [ "markdown-reader" ];
      description = ''
        Capture formats for `nebulous fetch`'s `-formats` flag, passed
        through to the folded-in capture phase. Extensible: add a format
        string here to capture it going forward (mirrors
        tools.DefaultCaptureFormats' Go-side default; kept in sync by
        hand since Nix can't import a Go const).
      '';
    };

    captureStoreId = mkOption {
      type = types.str;
      default = "nebulous";
      description = ''
        The madder blob-store id `nebulous fetch`'s `-store` flag
        targets for the folded-in capture phase. Defaults to the same
        named store internal/0/madder resolves for nebulous's own
        response cache (Stage 1) — one store to back up/migrate rather
        than two.
      '';
    };

    captureInterval = mkOption {
      type = types.str;
      default = "6h";
      example = "1h";
      description = ''
        NEBULOUS_CAPTURE_INTERVAL: how often the capture phase folded
        into `nebulous fetch` actually runs, gated at the application
        level rather than by a separate systemd timer. Longer than
        fetchInterval's default by design — the corpus scan behind the
        capture loop is real cost tied to the manifest's mtime (which
        every fetch tick bumps), so running it on the same cadence as
        the NewsBlur sync itself would multiply that cost for no
        benefit.
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

    systemd.timers.nebulous-fetch = mkOneshotTimer {
      description = "Periodic NewsBlur sync + gap-filling capture timer for nebulous fetch";
      onBootSec = "2min";
      onActiveSec = cfg.fetchInterval;
    };

    systemd.services.nebulous-fetch = mkOneshotService {
      description = "nebulous fetch — sync feeds/starred stories/original text from NewsBlur, plus a gated gap-filling capture-via-chrest pass";
      execStart = lib.escapeShellArgs (
        [
          "${cfg.package}/bin/nebulous"
          "fetch"
        ]
        ++ lib.optionals captureEnabled [
          "-formats"
          (lib.concatStringsSep "," cfg.captureFormats)
          "-store"
          cfg.captureStoreId
        ]
      );
      extraEnv = [
        "NEBULOUS_CAPTURE_INTERVAL=${cfg.captureInterval}"
      ]
      ++
        # internal/alfa/capture resolves both `cutting-garden` and (inside
        # THAT subprocess) `chrest` via a bare PATH lookup, not an absolute
        # store path — unlike mcp-origin.nix's own child commands, which
        # invoke madder/cutting-garden by absolute `${pkg}/bin/...` path
        # and never touch PATH at all. Matching that PATH-free convention
        # would mean threading the resolved binary paths into nebulous's
        # own Go code (a Bin-var override, mirroring internal/0/madder's
        # historical pattern before Stage 1's in-process rewrite removed
        # it) — deferred; for now the simpler fix is a unit-scoped PATH
        # carrying exactly the two packages the capture phase needs,
        # nothing else.
        lib.optionals captureEnabled [
          "PATH=${cfg.cuttingGardenPackage}/bin:${cfg.chrestPackage}/bin:/run/current-system/sw/bin"
        ];
    };
  };
}
