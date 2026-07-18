# withings2garmin

`withings2garmin` will be a small Go bridge that copies weight measurements from a Withings smart scale to Garmin Connect.

The repository currently contains the implementation specification, not the implementation. Start with [IMPLEMENTATION_PLAN.md](./IMPLEMENTATION_PLAN.md). Agents working on it must also follow [AGENTS.md](./AGENTS.md).

## Intended shape

- One Go binary; no Python runtime or runtime package manager.
- Go and all development tools supplied by a pinned Nix flake and direnv.
- Weight only. No activity, sleep, blood pressure, TrainerRoad, or body-composition scope creep.
- Official Withings OAuth 2.0 and measurement API.
- Current reverse-engineered Garmin mobile SSO and DI OAuth bearer-token flow. The deprecated `garth` implementation is explicitly out of scope.
- Direct Garmin weight API uploads; no home-grown FIT encoder for the weight-only use case.
- A flake package and NixOS module with a hardened oneshot service and timer.
- Secrets read from files/systemd credentials. Refresh tokens and sync state live outside the Nix store.

## Planned commands

The exact CLI is specified in the implementation plan. The expected operator flow is:

```text
withings2garmin auth withings ...
withings2garmin auth garmin ...
withings2garmin sync ...
withings2garmin status ...
```

Regular syncs use persisted OAuth refresh tokens. Garmin account credentials are only needed during explicit interactive bootstrap or reauthentication.

## Deployment target

The primary target is a NixOS server. The planned flake will expose:

```text
packages.<system>.default
devShells.<system>.default
nixosModules.default
checks.<system>.*
```

Do not put a Withings client secret, Garmin password, access token, or refresh token in a Nix expression: the Nix store is not a secret store.
