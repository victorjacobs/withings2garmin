# garmin-import

Weight-only synchronization from a Withings scale to Garmin Connect. It is one Go binary with a Nix-first development environment; Garmin Connect endpoints are unofficial and may change.

## Development

```sh
direnv allow
nix develop -c go test ./...
nix develop -c go build ./...
nix flake check
```

The flake supplies Go and all development tools. No host Go installation is required.

## Bootstrap

Register a personal Withings Public API application with the `user.metrics` scope, then authenticate as the account which owns the state directory:

```sh
sudo -u garmin-import garmin-import --state-dir /var/lib/garmin-import auth withings \
  --client-id YOUR_CLIENT_ID \
  --client-secret-file /run/secrets/withings-client-secret \
  --redirect-uri http://127.0.0.1:8080/callback

sudo -u garmin-import garmin-import --state-dir /var/lib/garmin-import auth garmin \
  --email-file /run/secrets/garmin-email \
  --password-file /run/secrets/garmin-password

sudo -u garmin-import garmin-import --state-dir /var/lib/garmin-import status --check
sudo -u garmin-import garmin-import --state-dir /var/lib/garmin-import sync --dry-run \
  --client-id YOUR_CLIENT_ID --client-secret-file /run/secrets/withings-client-secret
```

Run one non-dry sync before enabling the timer. Garmin credentials are used only by `auth garmin`; recurring sync uses the saved DI refresh token.

Use secret-manager paths appropriate to the service user. Do not put secrets in Nix expressions, command lines, the Nix store, or this repository.

## NixOS

```nix
{
  imports = [ inputs.garmin-import.nixosModules.default ];
  services.garmin-import = {
    enable = true;
    withings = {
      clientId = "your-public-client-id";
      clientSecretFile = "/run/secrets/withings-client-secret";
      redirectUri = "http://127.0.0.1:8080/callback";
    };
  };
}
```

The module creates `/var/lib/garmin-import` with restrictive ownership, injects the Withings secret through a systemd credential, and schedules a hardened oneshot service every three hours. Stop the timer during bootstrap if required.

## Recovery

- Withings reauthentication: rerun `auth withings`; keep the sync state.
- Garmin refresh rejected: rerun `auth garmin`; do not delete the ledger.
- Garmin 429: wait for the next scheduled run; do not repeatedly log in.
- Corrupt token/state files: move them aside manually and restore from backup or reauthenticate. The program never silently resets state.
- A recorded conflict means a source group changed or Garmin has an ambiguous matching record. Inspect it manually; version 1 does not overwrite Garmin data.
