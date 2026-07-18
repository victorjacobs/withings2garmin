self: { config, lib, pkgs, ... }:
let
  cfg = config.services.withings2garmin;
  inherit (lib) mkEnableOption mkIf mkOption types;
  package = if cfg.package == null then self.packages.${pkgs.system}.default else cfg.package;
in {
  options.services.withings2garmin = {
    enable = mkEnableOption "Withings to Garmin weight synchronization";
    package = mkOption { type = types.nullOr types.package; default = null; };
    user = mkOption { type = types.str; default = "withings2garmin"; };
    group = mkOption { type = types.str; default = "withings2garmin"; };
    withings = {
      clientId = mkOption { type = types.str; default = ""; };
      clientSecretFile = mkOption { type = types.str; default = ""; };
      redirectUri = mkOption { type = types.str; default = ""; };
    };
    schedule = mkOption { type = types.str; default = "hourly"; };
    randomizedDelaySec = mkOption { type = types.str; default = "5m"; };
    initialLookback = mkOption { type = types.str; default = "720h"; };
    includeAmbiguous = mkOption { type = types.bool; default = false; };
    logLevel = mkOption { type = types.enum [ "debug" "info" "warn" "error" ]; default = "info"; };
  };

  config = mkIf cfg.enable {
    assertions = [
      { assertion = cfg.withings.clientId != ""; message = "services.withings2garmin.withings.clientId is required"; }
      { assertion = cfg.withings.redirectUri != ""; message = "services.withings2garmin.withings.redirectUri is required"; }
      { assertion = cfg.withings.clientSecretFile != ""; message = "services.withings2garmin.withings.clientSecretFile is required"; }
      { assertion = !(lib.hasPrefix "/nix/store/" cfg.withings.clientSecretFile); message = "Withings secret must not be in /nix/store"; }
    ];

    users.groups.${cfg.group} = {};
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = "/var/lib/withings2garmin";
      createHome = true;
    };

    systemd.tmpfiles.rules = [ "d /var/lib/withings2garmin 0700 ${cfg.user} ${cfg.group} -" ];
    systemd.services.withings2garmin = {
      description = "Withings to Garmin weight synchronization";
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        User = cfg.user;
        Group = cfg.group;
        StateDirectory = "withings2garmin";
        StateDirectoryMode = "0700";
        UMask = "0077";
        LoadCredential = [ "withings-client-secret:${cfg.withings.clientSecretFile}" ];
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
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" ];
      };
      script = ''
        exec ${package}/bin/withings2garmin --state-dir /var/lib/withings2garmin --log-level ${cfg.logLevel} sync \\
          --client-id '${cfg.withings.clientId}' \\
          --client-secret-file "$CREDENTIALS_DIRECTORY/withings-client-secret" \\
          --initial-lookback '${cfg.initialLookback}' \\
          ${lib.optionalString cfg.includeAmbiguous "--include-ambiguous"}
      '';
    };
    systemd.timers.withings2garmin = {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = cfg.schedule;
        Persistent = true;
        RandomizedDelaySec = cfg.randomizedDelaySec;
        Unit = "withings2garmin.service";
      };
    };
  };
}
