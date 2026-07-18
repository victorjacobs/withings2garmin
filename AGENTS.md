# Repository instructions

## Current state

This repository is plan-only until the first implementation pass. Read `IMPLEMENTATION_PLAN.md` completely before writing code. Implement it in the listed phase order and keep the plan accurate if verified external behavior forces a change.

## Scope

- Build a Withings smart-scale weight bridge to Garmin Connect.
- Weight only.
- Use Go.
- Use Nix for Go and every development tool.
- Produce a flake package and a NixOS module/service timer.
- Read secrets from files or systemd credentials.
- Use Garmin's current reverse-engineered mobile SSO to DI OAuth bearer-token flow.
- Do not inspect, import, copy, or depend on `garth`; it is deliberately excluded.
- Do not add FIT generation unless current evidence proves the direct Garmin weight endpoint cannot satisfy the weight-only scope. Flag that change before implementing it.

## Development environment

The first implementation change must add `flake.nix` and `.envrc`. Do not use a host-installed Go toolchain. Run Go commands through the flake, for example:

```sh
direnv allow
nix develop -c go version
nix develop -c gofmt -w .
nix develop -c go test ./...
nix develop -c go build ./...
nix flake check
```

Do not document `go install`, Homebrew, apt, pip, Poetry, Docker, or another ad hoc development setup as the normal path.

## Go rules

- Follow existing package patterns once they exist. Do not introduce a second pattern casually.
- Keep the dependency set small. The TLS-fingerprinting dependency is isolated to Garmin interactive authentication; normal API and Withings traffic should use `net/http`.
- Use `context.Context` for network and storage boundaries.
- Set explicit HTTP timeouts and bounded response-body reads.
- Handle every error explicitly. Do not discard errors unless the surrounding pattern documents why the error is safely irrelevant.
- Put blank lines between setup, validation/error handling, the main action, and result construction.
- Add compile-time interface assertions with struct literals, for example `var _ TokenStore = &fileTokenStore{}`.
- Write comments for non-obvious reasons, not for restating code.
- Use `log/slog`. Never log passwords, authorization codes, cookies, access tokens, refresh tokens, raw credential files, or unredacted HTTP bodies/headers. Weight is health data; do not log its value at normal levels.
- Use exact integer/fixed-point weight representation internally. Do not use binary floating point as an identity or deduplication key.
- Centralize volatile Garmin endpoints, client IDs, app-identification headers, and TLS profiles in one package with source references and a researched date.

## Persistence and safety

- State belongs under the configured state directory, not the repository, home-directory dotfiles chosen ad hoc, `/tmp`, or the Nix store.
- Directories containing tokens must be mode `0700`; token/state files must be `0600`.
- Token rotation and cursor updates must use atomic replace in the same directory. A failed write must leave the last valid file intact.
- Hold the process lock for any command that can refresh tokens or mutate sync state.
- Persist a newly rotated Withings refresh token before using its new access token.
- Do not let the recurring timer fall back to a full Garmin username/password login. It must fail with an actionable reauthentication error.
- Never automatically delete or overwrite a Garmin weigh-in unless it can be proven to be the exact record created by this bridge. Version 1 records conflicts instead of guessing.

## External API behavior

- Withings success is the JSON envelope's `status == 0`, not merely HTTP 200. Status `100` is a successful no-data result. Follow pagination until `more == 0`.
- Use Withings `meastype=1`, `category=1`, and bearer authorization. Use `startdate`/`enddate` for initial or explicit backfills and `lastupdate` for incremental sync.
- Use the response `updatetime` as the next cursor only after the complete fetched set reaches a terminal state.
- Garmin API calls use DI bearer tokens and current native Garmin app headers.
- Garmin upload is `POST /weight-service/user-weight` with both local and GMT timestamps. Reconcile ambiguous POST outcomes with a read; do not blindly retry a possibly successful write.
- Treat Garmin endpoints as unofficial and unstable. Contract tests must make request drift obvious.

## Verification

Before handing off implementation changes, run at minimum:

```sh
nix develop -c gofmt -w .
nix develop -c go test ./...
nix develop -c go build ./...
nix flake check
```

Also run `go test -race ./...`, `golangci-lint run`, and `govulncheck ./...` through `nix develop` when those tools have been added to the flake. Live Garmin write tests must be opt-in and must never run in normal CI.

## Git

- Do not commit or push unless explicitly asked.
- Never amend commits.
- Preserve unrelated user changes.
