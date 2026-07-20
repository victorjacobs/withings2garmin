self: {
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.services.garmin-import;
  inherit (lib) mkEnableOption mkIf mkOption types;
  package =
    if cfg.package == null
    then self.packages.${pkgs.system}.default
    else cfg.package;
in {
  options.services.garmin-import = {
    enable = mkEnableOption "Withings to Garmin weight synchronization";
    package = mkOption {
      type = types.nullOr types.package;
      default = null;
    };
    user = mkOption {
      type = types.str;
      default = "garmin-import";
    };
    group = mkOption {
      type = types.str;
      default = "garmin-import";
    };
    withings = {
      clientId = mkOption {
        type = types.str;
        default = "";
      };
      clientSecretFile = mkOption {
        type = types.str;
        default = "";
      };
      redirectUri = mkOption {
        type = types.str;
        default = "";
      };
    };
    schedule = mkOption {
      type = types.str;
      default = "*-*-* 0/3:00:00";
    };
    randomizedDelaySec = mkOption {
      type = types.str;
      default = "5m";
    };
    initialLookback = mkOption {
      type = types.str;
      default = "720h";
    };
    includeAmbiguous = mkOption {
      type = types.bool;
      default = false;
    };
    logLevel = mkOption {
      type = types.enum ["debug" "info" "warn" "error"];
      default = "info";
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.withings.clientId != "";
        message = "services.garmin-import.withings.clientId is required";
      }
      {
        assertion = cfg.withings.redirectUri != "";
        message = "services.garmin-import.withings.redirectUri is required";
      }
      {
        assertion = cfg.withings.clientSecretFile != "";
        message = "services.garmin-import.withings.clientSecretFile is required";
      }
      {
        assertion = !(lib.hasPrefix "/nix/store/" cfg.withings.clientSecretFile);
        message = "Withings secret must not be in /nix/store";
      }
    ];

    users.groups.${cfg.group} = {};
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
    };

    systemd.tmpfiles.rules = ["d /var/lib/garmin-import 0700 ${cfg.user} ${cfg.group} -"];
    systemd.services.garmin-import = {
      description = "Withings to Garmin weight synchronization";
      wants = ["network-online.target"];
      after = ["network-online.target"];
      serviceConfig = {
        Type = "oneshot";
        User = cfg.user;
        Group = cfg.group;
        StateDirectory = "garmin-import";
        StateDirectoryMode = "0700";
        UMask = "0077";
        LoadCredential = ["withings-client-secret:${cfg.withings.clientSecretFile}"];
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictAddressFamilies = ["AF_UNIX" "AF_INET" "AF_INET6"];
      };
      script = ''
        exec ${package}/bin/garmin-import --state-dir /var/lib/garmin-import --log-level ${cfg.logLevel} sync \
          --client-id '${cfg.withings.clientId}' \
          --client-secret-file "$CREDENTIALS_DIRECTORY/withings-client-secret" \
          --initial-lookback '${cfg.initialLookback}' \
          ${lib.optionalString cfg.includeAmbiguous "--include-ambiguous"}
      '';
    };
    systemd.timers.garmin-import = {
      wantedBy = ["timers.target"];
      timerConfig = {
        OnCalendar = cfg.schedule;
        Persistent = true;
        RandomizedDelaySec = cfg.randomizedDelaySec;
        Unit = "garmin-import.service";
      };
    };
  };
}
