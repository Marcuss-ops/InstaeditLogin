# Error Classification — Contract & delivery_class Runbook

## The contract (one paragraph)

**Producers wrap typed sentinels; classifiers use `errors.Is` / `errors.As`; message text is never consulted.** A provider-controlled string merely *mentioning* `"expired"` or `"401"` must never be able to flip behavior, and codes derived from prose break the moment a message is reworded. The CI guard `scripts/verify-error-classification.sh` (wired into `make verify` as `verify-error-classification`) ratchets this invariant: any **new** `strings.Contains(err.Error(), …)` classifier in runtime Go fails the build. Tests are exempt — they pin legacy string behavior to prove it is gone.

## The three classifiers and their typed sources

| Classifier | Location | Reads (typed only) |
|---|---|---|
| `isTokenExpired` (account → `expired` status) | `pkg/api/accounts_validate.go` | `errors.Is(err, credentials.ErrTokenExpired)` |
| `isExpiryError` (vault legacy fallback) | `internal/credentials/vault_legacy_fallback.go` | `errors.Is(err, ErrTokenExpired)` |
| `deliveryErrorCode` (worker `delivery_class`) | `internal/worker/publish_worker_delivery.go` | `errors.Is` on `ERR_DRIVE_*` sentinels + `errors.As` on `services.DeliveryError` carriers |

`pkg/metrics.ErrorKind` also consults an optional `ErrorKindCarrier` interface (`ErrorKindName() ErrorKind`) implemented by `services.YouTubeAPIError` and `credentials.OAuthTokenError` **before** falling back to its documented legacy heuristics — typed errors bucket themselves, legacy untyped errors keep their buckets.

### Adding a new classification

1. Declare a sentinel (`var ErrFoo = errors.New("ERR_FOO")`) or a typed carrier struct with an `Unwrap() error` method (see `internal/services/delivery_error.go` for the pattern).
2. **Wrap it at the producer** — the wrap site owns the knowledge, once.
3. Classify with `errors.Is` / `errors.As` at the consumer.
4. Never `strings.Contains(err.Error(), …)` in runtime code — the guard will reject it.

## delivery_class runbook

Emitted by `dispatchPostCompletion` on delivery-dispatch failure, logged as `delivery_class` and **persisted to `post_targets.last_error_code`** (via `PostRepository.MarkDeliveryDispatchFailed`), so failures are queryable:

```sql
SELECT status, last_error_code, COUNT(*)
FROM post_targets
WHERE last_error_code IS NOT NULL AND updated_at > NOW() - INTERVAL '1 day'
GROUP BY 1, 2 ORDER BY 3 DESC;
```

| Code | Meaning | Operator action |
|---|---|---|
| `ERR_DRIVE_CONFIG` | Unparseable/empty destination config (`drive_account_id`, `folder_id`, `filename_template`). | Fix the destination configuration; not transient. |
| `ERR_DRIVE_NO_REFRESH_TOKEN` | Drive grant has no refresh token (first-generation consent). | Reconnect the Drive account (re-consent). |
| `ERR_DRIVE_INVALID_ACCOUNT_ID` | Token provider received a non-positive platform account id. | Data bug — check `platform_accounts.id` wiring. |
| `ERR_DRIVE_SESSION_EXPIRED` | Resumable session exceeded Google's 7-day TTL (404/410). | Transient — the destination resets the row and re-initiates; recurring values mean the source URL or folder is sick. |
| `ERR_DRIVE_IDEMPOTENCY_CONFLICT` | appProperties lookup found a *different* Drive file for the same key. | Runbook-required: inspect the Drive folder for a colliding `instaedit_delivery_id`. |
| `ERR_DRIVE_REINITIATE_BUDGET_EXHAUSTED` | A session key burned 5 consecutive dead sessions; row stamped `failed` + `delivery_drive_reinitiate_loops_total` incremented. | Permanent upload failure — check source URL reachability and destination folder permissions; repair then `RetryTarget`. |
| `HTTP_<n>` | Upstream HTTP status from a delivery stage (e.g. `HTTP_503`). | 4xx: config/permissions; 5xx: provider-side, usually transient; 429: quota. |
| `SESSIONSTORE.CREATE`, `SESSIONSTORE.MARKCOMPLETED`, `POSTINITIATESESSION`, `ENCRYPTOR.ENCRYPT`, `DECRYPT SESSION URI`, `SOURCE RANGE GET`, `FINDBYIDEMPOTENCYKEY`, `LOOKUPBYAPPPROPERTY`, `GETACCESSTOKEN`, `STREAMCHUNKS`, `INITIATE POST` | Pipeline stage codes (legacy byte-compat: dots preserved, spaces → underscores). | The stage names the failing step; pair with the redacted log line for detail. |
| `DELIVERY_ERROR` | Untyped delivery failure (no sentinel, no carrier). | Expected to shrink to zero as producers stamp carriers; investigate if a new producer never classifies. |

### Related counters

- `delivery_drive_reinitiate_loops_total{deliverable_type}` — churn guard trips (see table above).
- `publish_drive_required_violations_total{platform}` — platform publish completed while a required Drive upload terminally failed (target flips to `drive_required_failed`, `last_error_code='DRIVE_REQUIRED'`).
- `vault_refresh_flights_total{token_type}` / `vault_refresh_flight_shared_total{token_type}` — slow-path refresh volume / shared-grant queueing.
- `vault_refresh_slow_path_duration_seconds{outcome,token_type}` — renewal latency; `outcome ∈ {success, error, cancelled}`. Sustained `error` growth = provider degradation; `shared` growth = hot shared grants queueing.
