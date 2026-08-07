# Google OAuth — Monitoring Refresh-Token TTL

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file holds the **refresh-token TTL monitoring**
runbook: the two TTL regimes, the alert signals to wire up, the
verify-mode scripts, and the `APP_MODE` + clock-injection test
coverage that pins the 7-day Testing boundary.

Related documents:

- [Console setup walkthrough](oauth-google-setup.md)
- [Limits we have to plan around](oauth-google-limits.md)
- [200-Channel rollout workflow](oauth-google-rollout.md)

This is the part most operators skip — until Production mode silently
appears to work, then breaks six months later when an unused channel
gets garbage-collected.

## The two TTL regimes

| Mode               | Refresh-token behaviour                                                                                              |
| ---                | ---                                                                                                                  |
| **Testing**        | Expires **7 days** after consent for every non-test user. The dashboard must re-prompt weekly.                       |
| **Production**     | **Indefinite** — until (a) the user revokes the grant via [myaccount.google.com/permissions](https://myaccount.google.com/permissions), (b) the user changes their Google Account password (which may invalidate grants that touch Gmail scopes — InstaEdit does not request Gmail scopes today, so this is not currently a risk), or (c) the refresh token is unused for ~6 months (Google may garbage-collect; conservative number, not formally documented). |

## What to monitor

1. **`oauth_connections.reauth_required_at IS NOT NULL` (HIGH alert)**
   Any row where this column flips from NULL → a timestamp means
   the next `vault.Renew` for that connection failed. This is the
   primary "this channel needs re-authorization" signal. The
   dashboard surfaces it; ops needs a paging alert.
2. **`oauth_connections.last_validated_at` older than 14 days (MEDIUM alert)**
   Even when refresh tokens are indefinite, the **vault's lazy
   re-encrypt path** (Blocco #2.2) and the **channel-binding check
   in `youtube_oauth.go::Publish`** should each touch the connection
   at least once a fortnight. A 14-day-stale `last_validated_at`
   is a strong "this channel is dormant and may have been
   garbage-collected" signal.
3. **`oauth_connections.expires_at IS NULL` for Production connections (INFO)**
   In Testing mode, `expires_at` is set to `now + 7 days` when the
   grant happens. In Production mode, the column stays NULL because
   the token has no fixed expiry. Spot-check this column via the
   `oauth_health` admin dashboard widget: any row with
   `app_mode = 'production'` (set by the publish verifier) but a
   non-NULL `expires_at` is a leftover from a Testing-mode grant and
   should be flagged for rotation.
4. **HTTP 400 `invalid_grant` from `videos.insert` (HIGH alert)**
   This is the **terminal** failure mode — the refresh token is
   already invalid. The vault must (a) flip `reauth_required_at`
   to NOW(), (b) emit the `youtube_publish_channel_mismatch_total`
   counter (P0#2, sibling of the channel-mismatch metric), and
   (c) surface a banner in the operator dashboard with a
   "Reconnect this channel" CTA. The fix is operator-driven: they
   click the CTA, get redirected through the OAuth flow, and the
   new refresh token overwrites the dead one.

## Silent refresh-token eviction diagnostic

Google silently invalidates the oldest refresh token of a
`(Google Account, OAuth client)` pair once the pair reaches ~50-100
active tokens (see [oauth-google-limits.md](oauth-google-limits.md)).
There is no push notification; the operator notices only when a
refresh comes back `invalid_grant`. The repository ships a read-only
diagnostic that surfaces both the risk (tokens per subject+client
pair vs the 50/90/100 bands) and the observable symptoms
(`invalid_grant`, `reauth_required_at`, non-active connections,
orphaned token rows):

```bash
# Static privacy/read-only test (no DB needed)
make refresh-token-eviction-diagnostic-test

# Run against the real database (password-free URL + protected
# PGPASSFILE; never a password-bearing DSN in process args)
PGPASSFILE="$HOME/.pgpass-instaedit" \
  psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
  -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/refresh-token-eviction-diagnostic.sql
```

Sections and thresholds:

- `subject_client` — token rows per `(provider_subject_id,
  oauth_client_key)`; flags at 40 (near recommended 50), 50 (at/over
  the recommended soft cap), 90 (pool critical, new grants blocked)
  and 100 (Google's hard cap). Any pair ≥ 50 is an eviction-risk pair
  that must be drained by rebalancing across clients/accounts.
- `eviction_signals` — connections with `reauth_required_at` set,
  `last_refresh_error` matching `invalid_grant`/`quota`, or a
  non-active status: the observable eviction symptom.
- `orphan_tokens` — token rows whose `oauth_connection_id` no longer
  resolves (grant deleted without the token rows).
- `channel_grant_consistency` — grant fan-out + channel status per
  connection.
- `summary` — `PASS`/`CHECK` aggregates; run it as the 24h
  post-rollout check after adding a large channel fleet.

The script never selects token/ciphertext columns — it projects
identifiers, statuses, timestamps, error codes and counts only. The
`test-refresh-token-eviction-diagnostic.sh` gate re-verifies that
contract in CI (see the `refresh-token-eviction-diagnostic-test`
Makefile target).

## How to verify the current mode quickly

The `scripts/verify-google-oauth-mode.sh` (YouTube grant) and
`scripts/verify-drive-oauth-mode.sh` (Drive grant) helpers call
`GET https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=...`
and print:

* `aud` — the OAuth client_id the token was issued to. If this
  matches `YOUTUBE_CLIENT_ID` or `GOOGLE_DRIVE_CLIENT_ID` in
  `.env.production`, the token is signed by the production client.
* `expires_in` — the access-token's remaining TTL in seconds.
  Roughly 3,600 (1 hour) at issuance, decreasing. This does **not**
  reflect the refresh token's TTL (which is held server-side by
  Google), but a working `tokeninfo` response confirms the token
  has not yet expired and the client is in Google's good graces.
* `scope` — the space-delimited list of scopes the token was
  granted. Cross-check against
  [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set).
* `azp` — the authorized party (the client that requested the
  token). For web-server-flow InstaEdit tokens, `azp == aud`. A
  mismatch is suspicious and worth investigating.

Use them as quick "is the published app actually serving tokens?"
checks after every consent-screen republish.

```bash
./scripts/verify-google-oauth-mode.sh "$YOUTUBE_OAUTH_ACCESS_TOKEN"
./scripts/verify-drive-oauth-mode.sh "$DRIVE_OAUTH_ACCESS_TOKEN"
```

## AppMode flag + real clock injection for TTL coverage

The InstaEdit backend plumbs through an `APP_MODE` env var that
pins the deployment to Google's OAuth-consent-screen publishing
status:

```bash
APP_MODE=production   # default; durable refresh tokens
APP_MODE=testing      # mirrors Google's 7-day Testing-mode TTL
```

The flag lives in `internal/config/config.go` as
`Config.AppMode string`. In production wiring it is read by the
`TokenRefresher` closure built in the canonical bootstrap wiring; production
forwards calls to Google's real `oauth2/v3/token` endpoint and
returns the response verbatim.

In CI the flag is paired with a **real clock injection** on the
vault itself so TTL math is fully deterministic without pinging
real Google. The injection wires through vault.go:

* A `clock func() time.Time` field on `*CredentialVault`,
  initialised to `time.Now` in the production constructor.
* A `(*CredentialVault).SetClock(clock func() time.Time)` setter,
  reserved for tests (production callers should leave the default).
* All four `time.Now()` / `time.Until()` sites in vault.go (token
  persistence, `Get`, and the `Renew` fast + slow paths) read
  `v.clock()` instead. A regression that re-introduces `time.Now()`
  inside vault.go would be detected by failing the
  `ExpiresAt.Equal(want)` assertion below.

`internal/credentials/vault_ttl_test.go` injects a
`fakeClock{ t time.Time }` with a `Set(t time.Time)` driver +
duplicates the production wiring exactly:

* `TestVault_Renew_ProductionMode_T8d_RefreshSucceeds` --
  fakeClock set to T0; oauth_connection + expired access token
  seeded at T0; fakeClock.Set(T0 + 8 * 24h) advances the
  simulated time; production-mode closure emits a fresh token
  pair; `vault.Renew` must succeed AND the persisted `ExpiresAt`
  must equal `fc.t.Add(3600s)` (proving the clock injection
  flows through `saveForOAuthConnection`). A regression that
  reverted vault.go to `time.Now()` would fail this assert
  sharply without waiting 8 real-world days.
* `TestVault_Renew_ProductionMode_T7d_StillSucceeds` --
  belt-and-braces boundary case: fakeClock.Set(T0 + 7 * 24h)
  under AppMode=production. Production must STILL succeed at the
  exact day-7 boundary (the durable refresh-token invariant).
* `TestVault_Renew_TestingMode_T7d_FailsInvalidGrant` --
  fakeClock.Set(T0 + 7 * 24h) under AppMode=testing. The
  `ttlAwareClosure` (driven by `fc.Now() - baseTime`) emits
  Google's documented `invalid_grant` envelope. `vault.Renew` must
  propagate the error envelope containing BOTH `invalid_grant` AND
  `(status 400)` so `internal/services/youtube_oauth.go::isHardRejection4xxStatus`
  routes to the reauth branch (not the transient-retry branch).
* `TestVault_Renew_TestingMode_T7dMinus1h_StillWorks` --
  T+7d-1h regression guard: inside the grace window under testing,
  refresh must still succeed. Catches closure mis-cutoffs that
  would flip the boundary one hour early.
* `TestConfig_AppModeDefaultIsProduction` --
  `config.Load()` defaults `AppMode` to `"production"` so operators
  who forget to set the env var inherit the durable refresh-token
  bucket. `config.go::getEnv` uses `os.LookupEnv` so a literally
  empty `APP_MODE=""` env-var would still register as "value
  present"; the test therefore uses `os.Unsetenv("APP_MODE")`
  (not `t.Setenv("APP_MODE", "")`) to exercise the default path.

The two invariants the suite pins:

1. **I-1 — clock source is injectable.** vault.Renew reads the
   "now" through `v.clock()`, not `time.Now()`. Future tickets
   that add new TTL math (e.g. a 24h refresh-token inactivity
   garbage-collection check) are required to read `v.clock()` too;
   the existing assertions catch a regression with sub-second
   resolution.
2. **I-2 — the 7-day Testing-mode boundary lives ONLY in
   AppMode=testing.** AppMode=production is durable past day 7
   (and day 8, etc). The boundary cannot leak across modes; the
   `T+7dMinus1h` guard catches a regression that flipped the
   closure cutoff to <7d.

Trade-off vs. hitting real Google: the closed-form closure
deterministically emits the production-vs-testing branching
based on `fc.Now() - baseTime`. CI does not need to wait 7-8 real
days. The test pins OUR policy semantics (AppMode + clock
injection) but does NOT validate Google's actual
`oauth2/v3/token` response shape at T+7d. Operators verify the
latter on a per-channel cadence via `private_canary_ok` /
`canary_channel_match_ok` from `/admin/health.csv` (see the
[operational checklist](OAUTH-PRODUCTION.md#operational-checklist)).
