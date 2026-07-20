# garmin-import implementation plan

Research snapshot: 2026-07-18.

This is an implementation specification for a faster follow-up agent. It intentionally resolves the architectural choices up front so the implementation pass can mostly be mechanical.

## 1. Required outcome

Build a reliable, weight-only bridge with these properties:

1. Fetch weight measurements produced by a Withings smart scale.
2. Upload each measurement once to the correct Garmin Connect account with the original instant preserved.
3. Recover safely from restarts, token expiry, partial failure, network ambiguity, duplicate scheduler runs, and refresh-token rotation.
4. Run as one Go binary. Python is not part of the build or runtime closure.
5. Use a pinned Nix flake for development and packaging.
6. Expose a NixOS module containing a hardened oneshot service and persistent timer.
7. Read secrets from files/systemd credentials; never materialize secrets in the Nix store or command logs.
8. Use the current reverse-engineered Garmin mobile SSO -> DI OAuth bearer-token flow. Do not use or inspect `garth`.

The implementation is complete only when `nix build`, `nix flake check`, `go build ./...`, and `go test ./...` succeed through the Nix environment and the documented bootstrap/sync workflow is coherent.

## 2. Decisions already made

These are deliberate constraints, not questions for the implementation pass:

- Use the official Withings Public API with a user-owned OAuth application and the `user.metrics` scope.
- Use the Garmin Connect mobile SSO flow to obtain a CAS service ticket, exchange it for DI OAuth access/refresh tokens, and use bearer authentication against `connectapi.garmin.com`.
- Implement the current auth-strategy waterfall described in section 7. Do not resurrect the old OAuth1/preauthorized flow.
- Use Garmin's direct weight endpoint, `POST /weight-service/user-weight`, instead of generating a FIT file. Weight-only does not justify maintaining a binary FIT encoder.
- Upload measurements sequentially. There is no benefit in racing a private, rate-limited API.
- Persist an at-least-once Withings cursor plus an idempotency ledger. Confirm against Garmin before a write so a crash between remote success and local persistence does not create a duplicate.
- Do not automatically edit or delete Garmin records in version 1. If a Withings group changes after upload, record a conflict and require operator review. Deleting a user's unrelated Garmin measurement would be a fairly spectacular definition of “sync.”
- The recurring service never stores or uses the Garmin password. A failed Garmin refresh token requires an explicit `auth garmin` run.
- Use JSON state files with schema versions, atomic replacement, restrictive permissions, and a process lock. A database is unnecessary for one person's scale data.
- Default first-run history is 30 days. Explicit backfill flags can select a longer range.
- Default polling is every three hours with timer jitter. Withings says not to poll a user more often than every ten minutes; one scale measurement a day does not need a tiny polling interval.

### Non-goals for version 1

- Body composition, BMI, fat, hydration, muscle, bone, blood pressure, activity, sleep, TrainerRoad, or another destination.
- A long-running HTTP daemon or Withings webhook receiver. A NixOS timer is simpler for a personal scale bridge; the cursor protects missed intervals.
- Multi-user/account routing.
- Garmin `.cn` accounts.
- Bidirectional sync or importing Garmin data back into Withings.
- Automatically modifying/deleting Garmin records when Withings history changes.
- Docker images or a second non-Nix deployment stack.
- A general Garmin Connect SDK. Only auth, token refresh, reads required for reconciliation, and weight writes belong here.

## 3. Research findings

### 3.1 Existing Python project

`~/dev/withings-sync` was inspected as behavior reference only. Its relevant current behavior is:

- It combines CLI parsing, OAuth, API access, transformation, state, FIT encoding, and upload orchestration.
- It declares Python plus unconstrained-at-the-top `garminconnect`, `requests`, `lxml`, dotenv, and importlib dependencies, with no Nix development environment.
- Its current Garmin wrapper delegates auth and API behavior to another Python package. Its runtime therefore inherits that package's native TLS/browser-emulation dependency stack.
- It refreshes Withings tokens on every process start, even when the access token is still valid.
- It writes rotating tokens directly to JSON instead of atomically. Its refresh error path can continue and write missing token fields.
- It uses a shared application credential checked into that repository. This implementation must require the operator's own Withings application.
- It does not follow Withings pagination.
- It records local wall-clock time as `last_sync` instead of the server's `updatetime`, leaving a race window for missed data.
- It requests all measure types, fetches height, computes BMI/body composition, writes FIT manually, and uploads a batch even though this project's scope is weight only.
- It uses local-time conversions (`fromtimestamp`, `mktime`) in the FIT path, making results depend on the process timezone.
- It has no per-measurement idempotency record. Batch upload plus a single last-sync timestamp makes partial success difficult to recover safely.
- It has requests without explicit timeouts and debug logging that can include parsed CLI arguments containing credentials.

The useful ideas to retain are: Withings measure type `1`, category `1`, weight exponent decoding, preservation of measurement time, and persistent auth sessions. The structure should not be copied.

### 3.2 Withings contract

Use Withings' maintained [AI-agent reference](https://developer.withings.com/llms.md), [OpenAPI specification](https://developer.withings.com/openapi.yaml), [OAuth authorization documentation](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/get-access/oauth-authorization-url/), and [token lifetime/rotation documentation](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/get-access/access-and-refresh-tokens-no-recover/) as authoritative sources.

Important facts:

- Authorization endpoint: `GET https://account.withings.com/oauth2_user/authorize2`.
- Token endpoint: `POST https://wbsapi.withings.net/v2/oauth2` using form encoding and `action=requesttoken`.
- Authorization code lifetime: 30 seconds.
- Access token lifetime: 10,800 seconds (3 hours).
- Refresh token lifetime: one year.
- Refresh tokens rotate. The previous token expires eight hours after rotation or as soon as the new access token is used. The new token pair must be stored before the new access token is used.
- Data endpoint: `POST https://wbsapi.withings.net/measure` with bearer authorization and form encoding.
- Weight measure type is `1`; real measurement category is `1`.
- Decode a measurement as `value * 10^unit` kilograms.
- A measure group has stable `grpid`, measurement `date`, `created`, `modified`, `attrib`, device/model fields, and a list of measures.
- Pagination is mandatory: while `body.more == 1`, repeat the same request with `body.offset`.
- Initial/history reads should use measurement `startdate` and `enddate`. Incremental reads should use `lastupdate`.
- Store response `body.updatetime` as the next incremental cursor only after processing succeeds.
- Envelope `status == 0` is success. Status `100` is successful/no data. Notable errors are `343` for invalid/absent access token and `601` for rate limiting.

### 3.3 Current Garmin authentication

Garmin Connect does not publish this consumer API. The implementation must make its unofficial nature obvious and isolate volatile details.

The current non-deprecated flow was verified against active implementations at fixed revisions:

- [`python-garminconnect` native auth client, revision `2ae0eb5`](https://github.com/cyberjunky/python-garminconnect/blob/2ae0eb5d6e56a13bc0194e229992c3a7d855253c/garminconnect/client.py)
- [`ha-garmin` auth client, revision `67ba9c9`](https://github.com/cyberjunky/ha-garmin/blob/67ba9c9801929501147d606e309cb3f94c4ef3ff/src/ha_garmin/auth.py)
- [Current weight methods in `python-garminconnect`](https://github.com/cyberjunky/python-garminconnect/blob/2ae0eb5d6e56a13bc0194e229992c3a7d855253c/garminconnect/__init__.py#L1081-L1229)

Both current auth implementations use Garmin mobile/web SSO to get a CAS service ticket, then exchange it at `diauth.garmin.com` for DI OAuth access and refresh tokens. They use TLS browser impersonation and multiple SSO strategies because Cloudflare may return 403, 429, CAPTCHA, or non-JSON challenges to ordinary programmatic clients.

The Go implementation should use [`github.com/bogdanfinn/tls-client`](https://github.com/bogdanfinn/tls-client) only behind the Garmin interactive-auth transport adapter. It is a pure-Go, HTTP-client-like layer with Safari/iOS/Chrome profiles. Normal Withings and authenticated Garmin API calls should remain on `net/http`. Pin a released version in `go.mod`; do not track a branch. At research time the latest stable release was v1.14.0.

### 3.4 Garmin weight write

For weight-only uploads use:

```text
POST https://connectapi.garmin.com/weight-service/user-weight
Authorization: Bearer <DI access token>
Content-Type: application/json
```

Payload:

```json
{
  "dateTimestamp": "2026-07-18T08:12:34.000",
  "gmtTimestamp": "2026-07-18T06:12:34.000",
  "unitKey": "kg",
  "sourceType": "MANUAL",
  "value": 75.42
}
```

`dateTimestamp` is local wall time without an offset. `gmtTimestamp` is the same instant rendered in UTC without an offset. Garmin currently returns either JSON or 204 depending on endpoint behavior/account version; accept any documented 2xx shape and preserve a bounded sanitized copy only in test fixtures, not logs.

Use these read endpoints for reconciliation:

- `GET /weight-service/weight/dayview/YYYY-MM-DD?includeAll=true`
- `GET /weight-service/weight/dateRange?startDate=YYYY-MM-DD&endDate=YYYY-MM-DD`

Prefer day view when it exposes exact timestamps and `samplePk`. Treat response fields as tolerant/optional because the API is unofficial. Contract fixtures must cover the observed variants.

Direct upload means Garmin will label the source as manual. That is acceptable for version 1: preserving weight and time is the product requirement. Body-composition/FIT support is a later, separately justified feature.

## 4. Repository layout

Create this structure. Small adjustments are fine, but preserve the boundaries:

```text
.
├── .envrc
├── .gitignore
├── AGENTS.md
├── IMPLEMENTATION_PLAN.md
├── README.md
├── flake.lock
├── flake.nix
├── go.mod
├── go.sum
├── cmd/
│   └── garmin-import/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── app.go
│   │   └── commands.go
│   ├── config/
│   │   └── config.go
│   ├── garmin/
│   │   ├── api.go
│   │   ├── api_types.go
│   │   ├── auth.go
│   │   ├── auth_constants.go
│   │   ├── auth_transport.go
│   │   ├── errors.go
│   │   └── tokens.go
│   ├── secret/
│   │   └── file.go
│   ├── state/
│   │   ├── atomic.go
│   │   ├── ledger.go
│   │   ├── lock.go
│   │   └── tokens.go
│   ├── syncer/
│   │   ├── syncer.go
│   │   └── types.go
│   └── withings/
│       ├── api.go
│       ├── errors.go
│       ├── oauth.go
│       └── types.go
├── nix/
│   └── module.nix
└── testdata/
    ├── garmin/
    └── withings/
```

Tests should live beside their packages. Use `testdata` only for sanitized external contract fixtures and golden request bodies.

Do not create a reusable public library prematurely. Everything except `cmd` can remain under `internal`.

The owner has not selected a project license. Do not invent one during implementation; leave package license metadata unset until the owner chooses, or ask at the point a distributable release needs it.

## 5. Domain model and exact values

### 5.1 Weight

Do not use `float64` as identity or persisted truth. Define a measurement approximately as:

```text
WeightMeasurement
  WithingsGroupID int64
  MeasuredAt      time.Time        // parsed from Unix seconds; always an instant
  ModifiedAt      time.Time
  WeightGrams     int64
  Attribution     int
  DeviceID        string
  Model           string
  Timezone        string
```

Convert Withings weight with checked integer powers:

```text
kilograms = value * 10^unit
grams    = value * 10^(unit + 3)
```

Rules:

- Detect multiplication overflow.
- If `unit + 3 < 0`, divide with a documented deterministic rounding rule. A unit test must cover halves and negative exponents. Negative weight values are invalid.
- Reject zero and obviously impossible data. Use an internal hard safety range of 0.5 kg through 500 kg. Do not silently clamp.
- Render Garmin JSON kilograms from integer grams, not a binary float. `json.Number` or a small custom JSON marshaler can emit `75420` grams as `75.42`.
- Compare exact grams and exact UTC seconds for deduplication. If observed Garmin responses round to a coarser unit, normalize that response explicitly and test it; do not add arbitrary floating-point epsilon logic.

### 5.2 Timestamps

- Interpret Withings `date` as Unix seconds in UTC.
- Load the IANA timezone returned by Withings. Import `time/tzdata` so a static/minimal deployment does not depend on host zoneinfo availability.
- Format Garmin local and UTC timestamp strings with layout `2006-01-02T15:04:05.000`.
- Use the local date for Garmin day-view reconciliation.
- Test both sides of a Europe/Brussels DST change and a non-DST zone.

### 5.3 Attribution filter

The product is for smart-scale measurements, not manual Withings entries.

- Accept device-known attribution values `0` and `8` by default.
- Skip manual values `2` and `4`.
- Skip ambiguous attribution `1` by default and emit a count plus an actionable message. Support `--include-ambiguous` and a NixOS option for an operator who intentionally wants these.
- Do not hardcode a closed list of scale model IDs as the primary filter; Withings will add models. Weight type plus device attribution is more forward-compatible.
- Persist ignored group IDs/fingerprints so an unchanged ambiguous/manual result is not reported on every overlapping cursor read. A changed group is evaluated again.

## 6. CLI contract

Use the standard `flag` package with explicit subcommand `FlagSet`s. Cobra is unnecessary.

Global behavior:

```text
garmin-import [--state-dir PATH] [--log-level LEVEL] COMMAND
```

Default state directory:

- `$XDG_STATE_HOME/garmin-import` when set.
- Otherwise `~/.local/state/garmin-import`.
- The NixOS service always passes `/var/lib/garmin-import` through systemd's state-directory specifier.

Commands:

### `auth withings`

Required inputs:

- `--client-id STRING` (not a secret; may also come from `WITHINGS_CLIENT_ID`).
- `--client-secret-file PATH`.
- `--redirect-uri URI`.

Behavior:

1. Generate 32 random bytes with `crypto/rand`; encode base64url without padding as OAuth state.
2. Build the authorization URL with `url.Values`, `response_type=code`, `scope=user.metrics`, exact redirect URI, client ID, and state.
3. Print the URL to stdout and explain that the returned code lasts 30 seconds.
4. Accept a pasted full redirect URL on a TTY. Parse `code`, `state`, and `error`; require state equality. Do not accept an unvalidated raw code by default.
5. Optionally support `--callback-listen HOST:PORT` when the configured redirect URI points to the same loopback listener. Validate the host/port/path match before opening the listener. The callback handler must exchange immediately, display a minimal success/failure page, shut down, and never log the code.
6. Exchange the code and atomically persist the token set.
7. Print the Withings user ID, scope, and expiry time, not token values.

Do not ship someone else's Withings client ID or secret.

### `auth garmin`

Inputs:

- `--email-file PATH`.
- `--password-file PATH`.
- Optional `--mfa-code-file PATH`; otherwise prompt on a TTY if Garmin requests MFA.
- Optional `--force` to discard a currently valid token and perform full SSO again.

Behavior:

1. Hold the state lock.
2. If valid saved DI tokens exist and `--force` is absent, validate with social profile and return success.
3. Read credentials only when full login is necessary.
4. Execute the strategy waterfall in section 7.
5. Exchange ticket for DI tokens, validate the token against the API tier, atomically persist tokens, and discard credential bytes from reachable variables as soon as practical.
6. Never print a profile payload. A display name is acceptable in a final success message only if the user explicitly enabled verbose identity output; default to a generic account-valid message.

### `sync`

Inputs:

- Withings `--client-id` and `--client-secret-file` are needed for refresh.
- Optional `--from YYYY-MM-DD` and `--to YYYY-MM-DD` for an explicit backfill.
- Optional `--initial-lookback 30d` when no cursor exists.
- `--include-ambiguous`.
- `--dry-run`.
- Optional `--max-uploads N` as a backfill safety valve; default unlimited for incremental sync and 100 for an explicit first/backfill run unless overridden.

Behavior is specified in section 10.

### `status`

Without network access, report:

- Whether each token file exists and parses.
- Access-token expiry timestamps.
- Whether refresh tokens exist.
- Last successful Withings cursor time.
- Ledger counts: pending, uploaded, reconciled, ignored, conflict.
- Last run summary and error category.

`--check` may perform read-only Withings and Garmin auth validation. It must not upload or silently trigger full Garmin SSO. Token refresh is allowed because it is a normal auth-maintenance operation and must be persisted.

### Exit codes

Use stable documented codes:

- `0`: success, including no new measurements.
- `2`: CLI/configuration error.
- `3`: explicit reauthentication required.
- `4`: sync completed with recorded conflicts/partial terminal skips.
- `1`: other operational failure.

The systemd service treats any nonzero code as failure and exposes it in the journal.

## 7. Garmin authentication implementation

Put all volatile values in `internal/garmin/auth_constants.go`. Include a short comment with the source revisions and research date. Tests should assert request construction, not that the constants are eternal.

### 7.1 Current constants

For `garmin.com` accounts:

```text
SSO base                 https://sso.garmin.com
Connect API base         https://connectapi.garmin.com
DI token URL             https://diauth.garmin.com/di-oauth2-service/oauth/token
iOS SSO client ID        GCM_IOS_DARK
iOS service URL          https://mobile.integration.garmin.com/gcm/ios
Portal SSO client ID     GarminConnect
Portal service URL       https://connect.garmin.com/app
DI grant type            https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket
```

DI client IDs, in current preference order:

```text
GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2
GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4
GARMIN_CONNECT_MOBILE_ANDROID_DI
GARMIN_CONNECT_MOBILE_IOS_DI
```

Do not implement `.cn` support in the first pass unless it falls out cleanly and has tests. The initial deployment is a European `.com` account. Keep bases injectable so `.cn` can be added later without surgery.

### 7.2 Native Garmin headers

Use the current mobile-app identification headers for DI exchange, refresh, and authenticated API calls:

```text
User-Agent: GCM-Android-5.23
X-Garmin-User-Agent: com.garmin.android.apps.connectmobile/5.23; ; Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0
X-Garmin-Paired-App-Version: 10861
X-Garmin-Client-Platform: Android
X-App-Ver: 10861
X-Lang: en
X-GCExperience: GC5
Accept-Language: en-US,en;q=0.9
```

Keep header construction in one function. API calls add `Authorization: Bearer ...` and `Accept: application/json`.

### 7.3 Strategy waterfall

Implement a strategy interface returning either a CAS service ticket plus the exact service URL used or a typed error. Each strategy owns a cookie jar for its attempt.

For `.com`, try in this order:

1. Mobile iOS JSON login through TLS-browser clients, rotating equivalent released profiles: Safari iOS 18.5, desktop Safari, Chrome 120/144.
2. Mobile iOS JSON login through plain `net/http`.
3. SSO embed-widget HTML flow through a Chrome TLS profile.
4. Portal JSON login through TLS-browser profiles: Safari, Safari iOS, Chrome, Edge-equivalent where available.
5. Portal JSON login through plain `net/http`.

Typed error policy:

- Invalid username/password stops the waterfall immediately.
- MFA requirement stops the waterfall and is completed using the same cookie/session context.
- 403, CAPTCHA, unexpected HTML, transport failure, and a single-strategy 429 fall through.
- If every attempted strategy is rate-limited, return a rate-limit error.
- A strategy does not win until its DI token passes a read-only API validation request. If token exchange succeeds but `connectapi` returns 401/403, clear it and try the next strategy.

Do not add rapid loops within a profile. Each credential submission spends rate-limit budget.

### 7.4 Mobile iOS JSON request

Request:

```text
POST /mobile/api/login
  ?clientId=GCM_IOS_DARK
  &locale=en-US
  &service=https://mobile.integration.garmin.com/gcm/ios
```

Headers:

```text
User-Agent: Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148
Accept: application/json, text/plain, */*
Content-Type: application/json
Origin: https://sso.garmin.com
```

JSON:

```json
{
  "username": "...",
  "password": "...",
  "rememberMe": true,
  "captchaToken": ""
}
```

Classify `responseStatus.type`:

- `SUCCESSFUL`: read `serviceTicketId`.
- `MFA_REQUIRED`: save `customerMfaInfo.mfaLastMethodUsed`, defaulting to `email`.
- `INVALID_USERNAME_PASSWORD`: permanent auth error.
- `CAPTCHA_REQUIRED`: strategy challenge; fall through.

Also classify HTTP 429, HTTP 403, non-JSON responses, and JSON `error.status-code == "429"`.

### 7.5 MFA

For the mobile attempt, POST the same query params/cookies to `/mobile/api/mfa/verifyCode` with:

```json
{
  "mfaMethod": "email",
  "mfaVerificationCode": "123456",
  "rememberMyBrowser": true,
  "reconsentList": [],
  "mfaSetup": false
}
```

If the matching endpoint is rate-limited or returns a challenge, current clients attempt `/portal/api/mfa/verifyCode` with portal parameters using the same session. Implement that single fallback. Success returns another `serviceTicketId`.

The widget flow uses `/sso/verifyMFA/loginEnterMfaCode` form submission with the page CSRF token. Parse HTML with `golang.org/x/net/html`; do not use greedy regexes for security-sensitive fields.

### 7.6 Widget and portal fallbacks

Widget flow:

1. GET `/sso/embed?id=gauth-widget&embedWidget=true&gauthHost=https://sso.garmin.com/sso`.
2. GET `/sso/signin` with the current embed query set and referer.
3. Parse `_csrf` from the form.
4. Wait a cryptographically unimportant random 3-8 seconds. Make the sleeper injectable in tests.
5. POST form username, password, `embed=true`, and `_csrf`.
6. Detect bad credentials, child-account restrictions, server/challenge titles, MFA pages, and `<title>Success</title>`.
7. Extract the `ST-...` ticket from the success page and return the exact embed service URL used.

Portal flow:

1. GET `/portal/sso/en-US/sign-in?clientId=GarminConnect&service=https://connect.garmin.com/app` to establish cookies.
2. Wait 10-20 seconds before credential POST. Current active clients use this delay to avoid Cloudflare's rapid GET->POST detection.
3. POST JSON to `/portal/api/login` with portal client/service params, browser headers, referer, and the same credential shape as mobile.
4. Handle response/MFA exactly as the mobile JSON flow, but preserve portal service URL.

### 7.7 DI token exchange

For each DI client ID, POST form data to the DI token URL:

```text
client_id=<candidate>
service_ticket=<ST ticket>
grant_type=https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket
service_url=<exact service URL used to obtain the ticket>
```

Headers include native headers plus:

```text
Authorization: Basic base64("<client_id>:")
Accept: application/json,text/html;q=0.9,*/*;q=0.8
Content-Type: application/x-www-form-urlencoded
Cache-Control: no-cache
```

Try the next DI client ID on a non-429 rejected exchange. Stop on 429. On success require `access_token`; preserve `refresh_token`; parse the unverified JWT payload only to obtain `client_id` and `exp`. Never treat unverified JWT metadata as authorization—the token came over authenticated TLS and the server validates it.

Validate with:

```text
GET https://connectapi.garmin.com/userprofile-service/socialProfile
```

Persist tokens only after validation succeeds.

### 7.8 Refresh

Refresh 15 minutes before JWT expiry and once on an authenticated API 401:

```text
POST https://diauth.garmin.com/di-oauth2-service/oauth/token
Authorization: Basic base64("<stored_client_id>:")

grant_type=refresh_token
client_id=<stored_client_id>
refresh_token=<stored_refresh_token>
```

On success atomically store the returned access token and returned/previous refresh token before retrying the API request. Only one refresh and one API retry are allowed per request. If refresh is invalid, return exit code 3 with `run 'garmin-import auth garmin' interactively`; never fall back to stored account credentials.

## 8. Withings implementation

### 8.1 OAuth token model

Persist:

```text
schema_version
user_id
access_token
refresh_token
scope
token_type
obtained_at
expires_at
```

Token exchange and refresh use form bodies, not query strings. Require JSON envelope status zero and required fields. Preserve the previous valid file on every error.

Proactively refresh five minutes before `expires_at`. On data envelope status 343, refresh once and retry the read once. Never retry status 342/304 as though they were transient.

Critical refresh order:

1. Receive new token pair.
2. Validate required fields in memory.
3. Atomically persist the complete new pair.
4. Only then make an API call with the new access token.

If step 3 fails, return an error and leave the prior state untouched. This preserves Withings' grace behavior for the old refresh token.

### 8.2 Measurements request

Use `Authorization: Bearer ...`, form content type, and:

```text
action=getmeas
meastype=1
category=1
```

For initial/backfill:

```text
startdate=<inclusive Unix second>
enddate=<inclusive Unix second>
```

For incremental:

```text
lastupdate=<stored cursor minus one second>
```

The one-second overlap is deliberate; ledger deduplication makes it cheap and it protects timestamp-boundary behavior.

Pagination:

1. Issue the base request.
2. Decode with `json.Decoder.UseNumber()` and reject trailing JSON.
3. Treat status 100 as an empty successful page.
4. Append groups.
5. Record the first page's `updatetime` as the candidate cursor. If later pages disagree, use the minimum nonzero value and log a warning without sensitive data.
6. If `more == 1`, require a changed/nonzero offset and repeat with `offset` while preserving every other parameter.
7. Detect repeated offsets to avoid infinite loops.
8. Enforce a generous maximum page count (for example 10,000) and bounded body size.

Do not advance state during pagination.

### 8.3 HTTP behavior

- One reusable `http.Client` with transport pooling.
- Per-request timeout of 30 seconds; caller context may be shorter.
- Bounded response read, e.g. 4 MiB for token responses and 16 MiB per measurement page.
- Retry idempotent reads for network errors and 5xx up to three attempts with exponential backoff and jitter.
- Respect `Retry-After` when present, but do not sleep beyond the command context/deadline.
- Do not automatically retry 4xx, envelope errors other than the single 343 refresh path, or 601.

## 9. State and secret handling

### 9.1 Layout

Inside the state directory:

```text
withings-tokens.json     mode 0600
garmin-tokens.json       mode 0600
sync-state.json          mode 0600
garmin-import.lock     mode 0600
```

The directory is mode 0700. Refuse to operate if a state path is a symlink. Warn and tighten overly broad modes when owned by the current user; fail if ownership is unexpected.

### 9.2 Atomic file replacement

One helper should:

1. Marshal/validate the complete new document before opening files.
2. Create a unique temp file in the destination directory with mode 0600 and exclusive creation.
3. Write all bytes and check the write error.
4. `Sync` the file.
5. Close and check close error.
6. Rename over the destination atomically.
7. Open/sync the parent directory on Unix.
8. Clean up only the known temp path after a failure.

Never truncate the active file in place. Keep a schema version in every document and fail clearly on a future unknown version.

### 9.3 Lock

Use a maintained cross-platform file-lock module such as `github.com/gofrs/flock`, pinned in `go.mod`. Acquire the lock for all commands that can refresh tokens or modify state (`auth`, `sync`, `status --check`). Fail after a short context-aware timeout instead of running two syncs concurrently.

### 9.4 Secret files

The secret reader should:

- Open an explicit path only.
- Read a bounded amount (for example 64 KiB).
- Remove exactly one trailing LF and an optional preceding CR. Do not `TrimSpace`; passwords may contain spaces.
- Reject NUL and empty values.
- Return errors that name the path/purpose but never the content.
- Avoid retaining duplicate string copies where practical. Go cannot promise zeroization, so do not make false security claims.

## 10. Sync algorithm

Implement this order exactly unless a test proves a required correction.

### 10.1 Acquire and prepare

1. Parse and validate all configuration before network calls.
2. Ensure the state directory and permissions.
3. Acquire the process lock.
4. Load token stores and sync state. A corrupt file is an error; do not silently start fresh and duplicate remote data.
5. Refresh Withings and Garmin access tokens when near expiry, persisting rotations.
6. Validate that the Garmin token can make a lightweight API read. Do not full-login.

### 10.2 Select the Withings query

- Explicit `--from`/`--to`: perform a backfill date-range query. Do not alter the normal incremental cursor.
- No cursor: query measurement timestamps from `now - initialLookback` through `now`. After full success, initialize the cursor from Withings `updatetime`.
- Existing cursor: use `lastupdate=cursor-1`.

Calculate date boundaries in UTC for API seconds; `--from` begins at local midnight in the configured/operator timezone and `--to` ends at the final second of that local day. Document this in CLI help and test DST days.

### 10.3 Normalize/filter

1. Fetch every page before uploading anything. A pagination failure makes no remote Garmin changes.
2. For every group, find exactly one type-1 measurement. Missing weight is ignored. Multiple type-1 values are a malformed conflict.
3. Apply category and attribution filters.
4. Decode exact grams and validate the range.
5. Convert the Unix instant and Withings timezone.
6. Compute a fingerprint over stable canonical fields: group ID, modified timestamp, measured UTC second, grams, attribution, and device ID. Use SHA-256 and store hex.
7. Stable-sort by measurement instant, then group ID.

### 10.4 Ledger check

Ledger entry shape:

```text
group_id
observed_fingerprint
observed_measured_at
observed_weight_grams
synced_fingerprint  optional; exact source version represented in Garmin
synced_measured_at  optional
synced_weight_grams optional
state              pending | uploaded | reconciled | ignored | conflict
garmin_sample_pk   optional
first_seen_at
last_seen_at
reason             bounded enum/string, no secret data
```

- Same group ID and observed fingerprint in a terminal state: skip.
- Same group ID with a different observed fingerprint after `uploaded`/`reconciled`: preserve the `synced_*` fields and Garmin key, record the new `observed_*` fields as `conflict`, and do not write Garmin.
- Ignored entries are reevaluated when their fingerprint changes.
- A prior conflict with the same fingerprint remains terminal and visible in `status`.
- `pending` is a write-ahead marker, not terminal. Reconcile it before considering any new POST. If an exact remote result cannot be proven after bounded reads, leave it pending and fail safely; do not guess whether a prior attempt reached Garmin.

### 10.5 Garmin reconciliation and upload

Maintain a per-run cache of Garmin day views keyed by local date.

For each candidate:

1. Read/cache the day view with `includeAll=true`.
2. Normalize every Garmin result to UTC instant and integer grams when the fields allow it.
3. If exactly one result matches both UTC second and grams, record `reconciled` and its `samplePk`; do not POST.
4. If more than one exact match exists, record conflict.
5. If the same exact timestamp exists with another value, record conflict rather than overwriting.
6. If this source entry was already `pending` and no exact match is visible after bounded reconciliation reads, leave it pending and return failure. Do not continue to the POST path.
7. If there is no match and `--dry-run` is set, count `would_upload` without changing ledger/cursor.
8. Atomically persist a `pending` ledger entry before constructing/sending the POST. This write-ahead marker closes the process-killed-after-remote-write window.
9. POST the direct weight payload.
10. On definite 2xx, invalidate/refetch that day's cache and locate the created record. Store `samplePk` when unambiguous, then atomically change the ledger entry to `uploaded` immediately.
11. On timeout, reset, or another ambiguous transport failure after the request may have been sent, refetch the day view with bounded short delays for eventual visibility. If the exact record now exists, record `reconciled`; otherwise leave `pending` and return failure. Do not blindly POST again in the same or a later run.
12. On 401, refresh once, reconcile, then retry once only when reliable reconciliation proves the first request was not applied. Persist a new `pending` attempt timestamp before that retry.
13. On 429, stop further Garmin writes, preserve completed and pending ledger entries, do not advance cursor, and return failure.

Pace explicit backfills (for example 500 ms between writes with jitter) and stop at `--max-uploads`. Incremental sync normally contains zero or one item and needs no artificial delay.

### 10.6 Commit cursor and result

- Save each pending marker before its write and each terminal ledger entry immediately after its decision.
- Advance the Withings cursor only when every fetched eligible group reached a terminal state and no transient failure occurred.
- `ignored` and recorded `conflict` are terminal for cursor purposes; transient API/storage errors are not.
- If a transient failure occurs, leave the cursor unchanged. The next run refetches the overlap and the ledger skips already-completed groups.
- Explicit backfill never changes the incremental cursor.
- No-data status is success and may advance an incremental cursor to returned `updatetime`.
- Save a sanitized run summary: start/end times, query mode, counts, new cursor, and typed error category. Do not persist raw API bodies.

This creates at-least-once reads and effectively-once writes under the observable Garmin API.

## 11. Error model and logging

Define typed/sentinel error categories rather than parsing message text:

```text
configuration
authentication_required
invalid_credentials
mfa_required
rate_limited
remote_client_error
remote_server_error
transport
protocol
state_corrupt
state_write
conflict
```

Wrap with operation context, e.g. `refresh withings token: ...`, while preserving `errors.Is/As` classification.

Use `log/slog`:

- Default text logs for interactive commands and JSON-friendly attributes under systemd.
- Info: command phase and counts only.
- Debug: endpoint name, HTTP method, status, attempt, duration, group count. Do not log query/body fields that contain credentials or codes.
- Warn: skipped ambiguous measurements, token nearing invalid state, rate limit, protocol drift.
- Error: one final contextual error per failed command. Avoid duplicate stack-shaped log spam.
- Never log `Authorization`, `Cookie`, `Set-Cookie`, Basic auth, passwords, codes, token JSON, credential file content, or raw request/response bodies.
- Weight values are health data. Keep them out of info logs and the system journal. `status` reports counts, not weights.

## 12. Nix development environment and package

### 12.1 First implementation files

The first change must add:

`.envrc`:

```text
use flake
```

`flake.nix` and its generated `flake.lock`, pinning one nixpkgs revision. Do not use an unpinned channel at runtime.

### 12.2 Flake outputs

Support at least `x86_64-linux`, `aarch64-linux`, and `aarch64-darwin`; add `x86_64-darwin` if all dependencies build.

Use `lib.genAttrs`, not another flake dependency solely for iteration.

Development shell packages:

- `go` from the pinned nixpkgs (`pkgs.go`; research environment currently resolves Go 1.26.x).
- `gopls`.
- `gotools`/`go-tools` providing `goimports` as named by the pinned nixpkgs.
- `golangci-lint`.
- `govulncheck`.
- `delve` if supported on the platform.
- `alejandra` for Nix formatting.

Set no secret environment variables in the flake.

Package with `buildGoModule`:

- `pname = "garmin-import"`.
- Version injected from a source version or `0.1.0` initially.
- `src` filtered to exclude `.git`, local state, and result symlinks.
- Fixed `vendorHash` generated after `go.mod/go.sum` settle.
- `CGO_ENABLED=0` using the current nixpkgs-supported `env.CGO_ENABLED = "0"` form.
- `ldflags` inject version, revision, and build date variables without making the derivation depend on wall-clock time. Use a reproducible source date.
- `doCheck = true`; normal unit tests run in the package build.
- `meta.mainProgram = "garmin-import"`.

Checks should include the package, unit tests, formatting/lint where practical, and evaluation of a minimal NixOS configuration importing `nixosModules.default`.

## 13. NixOS module

Expose `nixosModules.default` from `nix/module.nix`.

### 13.1 Options

Use this interface:

```text
services.garmin-import.enable                   bool
services.garmin-import.package                  package
services.garmin-import.user                     string, default "garmin-import"
services.garmin-import.group                    string, default "garmin-import"
services.garmin-import.withings.clientId        string
services.garmin-import.withings.clientSecretFile absolute path string
services.garmin-import.withings.redirectUri     string
services.garmin-import.schedule                 string, default "*-*-* 0/3:00:00"
services.garmin-import.randomizedDelaySec       string, default "5m"
services.garmin-import.initialLookback          string, default "720h"
services.garmin-import.includeAmbiguous         bool, default false
services.garmin-import.logLevel                 enum debug|info|warn|error, default info
```

Assertions:

- client ID, redirect URI, and client-secret file are required when enabled.
- schedule cannot imply a sub-ten-minute default without an explicit override option and warning.
- secret path must not syntactically point into `/nix/store`. Evaluation cannot prove ownership/permissions, but it can reject the obvious foot-gun.

Declare the secret-file option as an absolute-path **string**, not `types.path`: a Nix path value can copy the referenced secret into the world-readable Nix store during evaluation.

Do not add Garmin username/password options to the recurring service. They are bootstrap-only CLI inputs.

### 13.2 User/state

Create a fixed system user/group rather than `DynamicUser`. A fixed user makes interactive SSH bootstrap into the same state directory tractable.

The service uses:

```text
User=garmin-import
Group=garmin-import
StateDirectory=garmin-import
StateDirectoryMode=0700
UMask=0077
```

Also add a tmpfiles rule that creates `/var/lib/garmin-import` as `0700` with the configured user/group during activation. This makes the directory available for interactive authentication before the timer's first service invocation; `StateDirectory` remains the service-side ownership/hardening declaration. The administrator should persist `/var/lib/garmin-import` on impermanent systems.

### 13.3 Credential injection

Use systemd `LoadCredential` for the Withings client secret. systemd exposes service credentials through `$CREDENTIALS_DIRECTORY` and the `%d` specifier; see [`systemd.exec(5)` credentials](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#Credentials).

Conceptually:

```text
LoadCredential=withings-client-secret:/run/secrets/withings-client-secret
ExecStart=... sync --client-secret-file %d/withings-client-secret ...
```

The source file can be managed by sops-nix, agenix, or another operator choice. Do not make this module depend on one secret manager.

### 13.4 Service hardening

Oneshot service with network-online ordering and at least:

```text
Type=oneshot
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
```

Allow writes only through `StateDirectory`. Verify that the pure-Go TLS client works with the chosen syscall/hardening set before adding a strict `SystemCallFilter`; omit that filter rather than shipping an untested one.

### 13.5 Timer

Define a timer:

```text
OnCalendar=<schedule>
Persistent=true
RandomizedDelaySec=<configured>
Unit=garmin-import.service
WantedBy=timers.target
```

`Persistent=true` lets a missed run fire after reboot. The process lock and ledger make overlapping/manual invocations safe.

### 13.6 Bootstrap documentation

Document a two-stage deployment:

1. Import the module/package, create the fixed user/state directory, and temporarily leave the timer disabled or stop it.
2. Register a personal Withings Public API app with the configured redirect URI.
3. Run `auth withings` interactively as the service user, passing the client secret file.
4. Run `auth garmin` interactively as the service user, passing email/password files and MFA when requested.
5. Run `status --check` and one `sync --dry-run`.
6. Run one real `sync`.
7. Enable/start the timer.

Provide concrete `sudo -u garmin-import` examples, but do not assume secrets are readable by arbitrary users. Note that the exact command depends on the operator's secret manager.

## 14. Testing plan

No normal test should need internet access or real credentials.

### 14.1 Test infrastructure

- Inject every base URL, clock, sleeper, random source used for jitter, HTTP doer, and token store.
- Use `httptest.Server` for protocol tests.
- Use sanitized JSON fixtures for observed Garmin variants and official Withings examples.
- Add compile-time interface assertions for production and fake implementations.
- Never record live VCR-style cassettes containing auth headers/cookies and hope a sanitizer caught everything.

### 14.2 Withings tests

- Authorization URL is correctly encoded and state is random/validated.
- Full redirect URL parsing rejects missing/wrong state and OAuth errors.
- Authorization-code exchange sends exact form fields and handles the 30-second flow without storing the code.
- Refresh sends exact form fields.
- New refresh token is persisted before a new access token is exposed to the caller; simulate persistence failure.
- Envelope status 0, 100, 343, 601, malformed JSON, missing fields, oversized response.
- One 343 causes one refresh and retry.
- Pagination gathers all pages, preserves filters, detects repeated offset, and chooses a conservative cursor.
- Weight exponent table including `65750 * 10^-3 kg`, overflow, fractional gram, zero, and range rejection.
- Attribution filtering and changed ignored groups.

### 14.3 Garmin auth tests

- Mobile iOS request path, params, headers, JSON, and cookies.
- `SUCCESSFUL`, invalid credentials, MFA, CAPTCHA, 403, top-level 429, JSON 429, non-JSON challenge.
- Wrong credentials stop later strategies.
- Transient/challenge failures fall through in correct order.
- MFA uses the original session and dual endpoint fallback.
- Widget CSRF/title/ticket parsing uses representative HTML and rejects missing/duplicate fields.
- Portal delay uses injected sleeper and stays inside configured bounds.
- DI exchange tries client IDs in order, uses exact Basic auth/form, stops on 429, and binds exact service URL.
- JWT base64url parsing handles absent padding, invalid JSON, missing client ID/exp.
- Token is validated against social profile before persistence.
- Rejected token falls through to another SSO strategy.
- Refresh rotates and persists tokens; 401 triggers at most one refresh/retry.
- Token store permissions and redaction.
- TLS profile adapter construction has a small test, but do not couple most auth logic to the third-party client type.

### 14.4 Garmin weight API tests

- Exact local/GMT timestamps across DST.
- Exact JSON number rendering from grams.
- Required native headers and bearer token.
- Accept observed 200/201/204 success variants.
- Parse day-view/date-range fields tolerantly and normalize grams/time.
- Read retry only on transport/5xx.
- POST ambiguous failure invokes reconciliation instead of blind retry.
- 429 stops writes; `Retry-After` parsing is bounded.

### 14.5 Sync tests

- Empty first run advances cursor and exits zero.
- Initial range vs incremental `lastupdate` vs explicit backfill.
- Stable chronological order.
- Same ledger fingerprint is skipped.
- State-loss scenario: remote exact match becomes `reconciled`, not duplicate POST.
- Crash-window scenario: POST succeeds, local save fails, next run reconciles.
- Kill-before-POST scenario: a write-ahead `pending` entry is not blindly retried; exact reconciliation or operator review resolves it.
- Same timestamp/different weight becomes conflict.
- Changed already-uploaded Withings group becomes conflict, not duplicate/overwrite.
- Multiple legitimate same-day measurements with distinct timestamps both upload.
- Ambiguous/manual groups are ignored per configuration.
- Failure on item N persists items 1..N-1 and leaves cursor unchanged.
- Next run skips persisted entries and resumes safely.
- Dry run performs reads but no POST/ledger/cursor changes.
- Upload limit and backfill pacing.
- Two concurrent sync attempts: one holds lock, the other times out cleanly.

### 14.6 State tests

- New files/directories get 0600/0700.
- Atomic replacement preserves old file on marshal/write/sync/rename failure using injected filesystem hooks where necessary.
- Unknown schema version fails.
- Corrupt JSON does not reset state.
- Symlink target is rejected.
- Secret reader preserves meaningful spaces and removes only one newline.

### 14.7 Nix tests/checks

- `nix develop -c go test ./...`.
- `nix develop -c go test -race ./...` on supported systems.
- `nix develop -c go build ./...`.
- `nix develop -c golangci-lint run`.
- `nix develop -c govulncheck ./...`; assess any TLS-stack advisory before release rather than suppressing it blindly.
- `nix build` produces a runnable binary with `CGO_ENABLED=0`.
- `nix flake check` evaluates all outputs.
- A NixOS module evaluation test verifies service, timer, user, state directory, credential mapping, and assertions.

### 14.8 Optional live tests

Use build tags and two explicit gates:

- Read-only live auth/API test: `WITHINGS2GARMIN_LIVE_TEST=1`.
- Garmin write test: a second `WITHINGS2GARMIN_LIVE_WRITE_TEST=1` plus an explicit test weight/time, followed by deletion only if the exact created `samplePk` was captured.

Never run live tests in ordinary CI. Never make automatic cleanup delete by date or approximate weight.

## 15. Implementation phases

Each phase should finish green before starting the next.

### Phase 1: Nix and skeleton

Deliver:

- `flake.nix`, `.envrc`, `flake.lock`, `.gitignore`.
- `go.mod`, main command skeleton, build metadata/version command.
- Dev shell and `buildGoModule` package.
- Basic CI/check commands documented.

Acceptance:

- `nix develop -c go version` works without host Go.
- `nix develop -c go test ./...`, `go build ./...`, and `nix build` pass.

### Phase 2: Secure state foundation

Deliver:

- State-dir resolution, permission checks, secret reader, atomic JSON store, schema versions, file lock.
- Unit tests including corruption and rotation-write failures.

Acceptance:

- Concurrent mutation is impossible.
- A failed state write demonstrably preserves the previous valid file.

### Phase 3: Withings OAuth and measurements

Deliver:

- `auth withings` including state validation and optional loopback callback.
- Token refresh rotation ordering.
- Measurement API, pagination, filters, exact conversion.
- Mock-server contract tests.

Acceptance:

- Official demo-mode OAuth can be tested manually if a developer app is available.
- All normal tests remain offline.

### Phase 4: Garmin DI auth

Deliver:

- Isolated TLS auth transport.
- Five-strategy SSO waterfall, MFA, DI exchange, validation, persistence, refresh.
- `auth garmin` and `status --check` read-only validation.
- Full offline tests for request contracts/error policy.

Acceptance:

- A real interactive `.com` login persists DI tokens mode 0600.
- A second process validates/refreshes using token files without username/password or SSO.

### Phase 5: Garmin weight API and syncer

Deliver:

- Weight/day-view clients.
- Ledger and reconciliation.
- Full sync algorithm, dry run, backfill, cursor semantics, summaries.
- Failure-injection tests.

Acceptance:

- One opt-in live measurement reaches Garmin with correct weight/time.
- Rerunning the same sync produces no second record.
- Simulated crash after remote write reconciles without duplication.

### Phase 6: NixOS module and operations docs

Deliver:

- `nixosModules.default`, fixed user, credential loading, hardening, timer.
- Module evaluation test.
- README changed from plan-only to actual installation/bootstrap/operations/troubleshooting docs.
- Example NixOS configuration using a generic secret path and notes for sops-nix/agenix without depending on them.

Acceptance:

- NixOS service runs `sync` with no Garmin password available.
- Secret values do not appear in the Nix store, unit `ExecStart`, process list, or journal.
- State survives package/system upgrades.

### Phase 7: Final hardening

Deliver:

- Race, lint, vulnerability, and flake checks.
- Audit redaction and response-size limits.
- Verify every external request has timeout/context.
- Verify interface assertions and Go formatting.
- Document recovery for revoked Withings consent, expired Garmin refresh, corrupt state, 429, and API drift.

Acceptance:

- All commands in section 14.7 pass.
- No TODO remains on an auth, persistence, idempotency, or secret-handling path.

## 16. Operator recovery behavior

Document exact remedies:

- Withings 343 after refresh failure: rerun `auth withings`; do not delete sync state/ledger.
- Garmin DI refresh invalid: rerun `auth garmin`; do not delete ledger.
- Garmin 429: wait; the next timer run resumes. Do not hammer full SSO.
- Withings 601: respect backoff/schedule; do not advance cursor.
- Corrupt token file: move it aside manually and reauthenticate. The program must not delete it automatically.
- Corrupt sync state: restore from backup or run an explicit operator-approved rebuild/reconcile command added later. Never silently start with an empty ledger.
- Recorded conflict: inspect both services and resolve manually. Version 1 should offer enough IDs/timestamps in `status --verbose` for the owner, while normal logs stay health-data-minimal.
- Garmin protocol drift: contract failure should identify endpoint/status/field category without leaking response bodies. Update centralized constants/types and add the new sanitized fixture.

## 17. Definition of done

The next agent should not call the project done merely because one happy-path upload works. Done means:

- Nix-first setup is the only supported development path.
- The binary builds without CGo and runs without Python.
- Withings OAuth state, short-lived code, rotating refresh tokens, bearer auth, pagination, and server cursor are implemented correctly.
- Garmin current SSO/DI OAuth, MFA, TLS challenge fallback, token validation, persistence, and refresh are implemented without `garth`.
- Weight conversion and timestamps are exact and covered across DST.
- Duplicate/crash/partial-failure tests prove the cursor-plus-ledger design.
- The recurring NixOS service has no Garmin password and reads the Withings secret through a credential file.
- Secrets/tokens do not enter the Nix store or logs.
- `go build ./...`, `go test ./...`, lint, vulnerability scan, `nix build`, and `nix flake check` pass through the flake.
- README contains tested bootstrap, deployment, scheduling, status, and recovery commands.

At that point it will be a bridge rather than an ecosystem of packaging grievances wearing a cron job as a hat.
