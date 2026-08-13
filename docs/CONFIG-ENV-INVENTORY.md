# Configuration environment inventory

This document is the operational inventory for `internal/config`. The loader
reads environment variables in `config_load.go`; `config_validation.go` applies
boot-time policy after loading. Values are not secrets-safe to print: the
inventory names keys and behavior, but never contains credential values.

## Loading and precedence

1. `godotenv.Load()` is attempted first; existing process environment values
   remain authoritative under the dotenv loader semantics.
2. `config.Load()` maps environment values into `Config`.
3. Typed parsers apply defaults when a key is unset or cannot be parsed:
   `getEnv`, `getEnvInt`, `getEnvInt64`, `getEnvBool`, and `splitCSV`.
4. JWT TTL compatibility is resolved after construction:
   `JWT_ACCESS_TTL_MINUTES` wins; otherwise `JWT_TTL_HOURS * 60`; otherwise
   access TTL defaults to 15 minutes. Refresh TTL defaults to 30 days.
5. Editor URL is loaded exclusively from `INSTAEDITOR_URL` while
   constructing `HTTPConfig`. `EDITOR_URL` is intentionally not read: a
   missing `INSTAEDITOR_URL` fails fast (empty launcher + 503 on editor
   endpoints, and a hard validation error in production) instead of silently
   falling back to a legacy or frontend destination.
6. `Config.validate()` applies cross-field and environment-specific gates.

Invalid integer/boolean text currently falls back to the declared default rather
than failing during parsing. Domain validation then rejects values that are
invalid after parsing (for example negative pool sizes or an invalid production
redirect URI).

## Shared field-spec resolver

The loader has one private resolver for repeated mapping shapes:

- `newDBPoolFieldSpec(prefix, defaults).resolve()` maps the four DB pool fields
  for `DB_API`, `DB_WORKER`, `DB_SERVER`, and `DB_MAINTENANCE`.
- `newYouTubeOAuthClientFieldSpec(slot).resolve()` maps the three fields for
  YouTube OAuth pool slots `A` and `B`.

The resolver only removes repeated key/fallback mapping. It does **not** merge
or move validation rules: pool bounds remain in `validateDBPoolProfile`, and
YouTube client completeness/secret-length rules remain in
`validateYouTubeOAuthClientPool`. The legacy single-client YouTube path is also
unchanged.

## Defaults and validation groups

| Group | Environment keys | Default / behavior | Validation gate |
|---|---|---|---|
| Runtime | `APP_ENV`, `APP_MODE`, `LOG_LEVEL` | `dev`, `production`, `info` | `APP_ENV`: dev/staging/production; `APP_MODE`: dev/testing/production |
| Database URL | `DATABASE_URL` | empty; otherwise individual DB fields | Password required when URL is empty |
| Database fields | `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | localhost, 5432, instaedit, empty, instaedit_login, disable | URL or password; production/staging installation UUID |
| Legacy DB pool | `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_SECONDS`, `DB_CONN_MAX_IDLE_TIME_SECONDS` | 25, 5, 1800, 300 | positive open/lifetimes; idle in range |
| DB profiles | `DB_{API,WORKER,SERVER,MAINTENANCE}_{MAX_OPEN_CONNS,MAX_IDLE_CONNS,CONN_MAX_LIFETIME_SECONDS,CONN_MAX_IDLE_TIME_SECONDS}` | API 15/7/1800/300; Worker 10/5/1800/300; Server 25/10/1800/300; Maintenance 3/1/1800/300 | each configured profile uses the same bounds |
| Storage | `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, `S3_PATH_STYLE`, `STORAGE_MAX_UPLOAD_BYTES` | required S3 values; path-style false; max upload 200 MiB | endpoint, bucket, access key and secret required |
| YouTube OAuth | `YOUTUBE_CLIENT_ID`, `YOUTUBE_CLIENT_SECRET`, `YOUTUBE_REDIRECT_URI` | disabled when ID/secret empty; localhost callback in dev | optional pair; production HTTPS canonical callback |
| YouTube OAuth pool | `YOUTUBE_OAUTH_CLIENT_{A,B}_{ID,SECRET,REDIRECT_URI}` | all three empty per slot | each slot all-or-none; secret ≥32 chars |
| Other OAuth | `META_*`, `TIKTOK_*`, `X_*`, `GOOGLE_DRIVE_*`, `LINKEDIN_*` | providers optional; local callback defaults | enabled platforms require ID + secret; secrets ≥32 chars |
| JWT/security | `JWT_SECRET`, `JWT_ACCESS_TTL_MINUTES`, `JWT_REFRESH_TTL_DAYS`, `JWT_TTL_HOURS`, `TRUSTED_PROXIES`, `ADMIN_INVITE_TOKEN` | JWT secret required; TTL 15m/30d; invite empty disables registration | JWT ≥32 bytes; invite ≥32 chars when set |
| Workers | `PUBLISH_*`, `RECONCILE_*`, `WEBHOOK_*`, `SESSION_CLEANUP_*`, `ASSET_CLEANUP_*`, `UPLOAD_*`, `RENDER_*`, `FFMPEG_*` | see `config_load.go`; main cadence defaults include 30s publish/upload and 86400s asset cleanup | provider-dependent positive/range checks |
| YouTube worker policy | `YOUTUBE_UPLOAD_*`, `YOUTUBE_DAILY_UPLOAD_LIMIT`, `YOUTUBE_SEARCH_QUOTA_LIMIT`, `YOUTUBE_GENERAL_QUOTA_LIMIT`, `YOUTUBE_GROUP_VIDEOS_*`, `PUBLISH_HORIZON_DAYS`, `VIDEO_RETENTION_BUFFER_DAYS` | 16 MiB chunks, 5 retries, 1s/5m backoff; 2026 quota buckets 100 uploads / 100 searches / 10000 general units per day; horizon 30d, buffer 7d | chunk multiple, retry/backoff ordering, positive limits; legacy `YOUTUBE_DAILY_QUOTA_LIMIT` honoured as uploads fallback |
| Sweeps | `TOKEN_REFRESH_SWEEP_*`, `SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS` | 900s/120d and 60s | token sweep positive when Google OAuth is enabled; snapshot zero normalizes to 60 |
| Observability | `METRICS_*`, `SENTRY_*` | metrics listener disabled at port 0; Sentry disabled with empty DSN | production metrics credentials; DSN URL shape and HTTPS in production |
| Integrations | `VELOX_*`, `NVIDIA_API_KEY`, `STRIPE_*` | optional; Stripe URLs derive from `FRONTEND_URL` | Velox control URL/secret must be paired; AI remains optional |
| Encryption | `ENCRYPTION_KEY`, `ENCRYPTION_KEYS`, `ACTIVE_ENCRYPTION_KEY_ID` | legacy single key or explicit multi-key | exactly one mode; base64 32-byte keys and active ID present |

## Complete loader key list

The following is the complete set of keys read directly by `config_load.go` (the
list intentionally excludes infrastructure-only variables consumed by Compose,
Docker, Vercel, or entrypoint scripts):

```text
NVIDIA_API_KEY
VELOX_API_TOKEN VELOX_CONTROL_URL VELOX_CONTROL_JWT_SECRET VELOX_WEBHOOK_SECRET
METRICS_BASIC_AUTH_USER METRICS_BASIC_AUTH_PASS METRICS_HOST METRICS_PORT
SENTRY_DSN SENTRY_ENVIRONMENT SENTRY_RELEASE
META_APP_ID META_APP_SECRET META_REDIRECT_URI
INSTAGRAM_REDIRECT_URI FACEBOOK_REDIRECT_URI THREADS_REDIRECT_URI
TIKTOK_CLIENT_ID TIKTOK_CLIENT_SECRET TIKTOK_REDIRECT_URI
X_CLIENT_ID X_CLIENT_SECRET X_REDIRECT_URI
YOUTUBE_CLIENT_ID YOUTUBE_CLIENT_SECRET YOUTUBE_REDIRECT_URI
YOUTUBE_OAUTH_CLIENT_A_ID YOUTUBE_OAUTH_CLIENT_A_SECRET YOUTUBE_OAUTH_CLIENT_A_REDIRECT_URI
YOUTUBE_OAUTH_CLIENT_B_ID YOUTUBE_OAUTH_CLIENT_B_SECRET YOUTUBE_OAUTH_CLIENT_B_REDIRECT_URI
GOOGLE_DRIVE_CLIENT_ID GOOGLE_DRIVE_CLIENT_SECRET GOOGLE_DRIVE_REDIRECT_URI
LINKEDIN_CLIENT_ID LINKEDIN_CLIENT_SECRET LINKEDIN_REDIRECT_URI
JWT_SECRET JWT_ACCESS_TTL_MINUTES JWT_REFRESH_TTL_DAYS JWT_TTL_HOURS TRUSTED_PROXIES ADMIN_INVITE_TOKEN
FRONTEND_URL INSTAEDITOR_URL EDITOR_URL CORS_ALLOWED_ORIGINS COOKIE_DOMAIN LOG_LEVEL APP_ENV
DATABASE_URL DB_MAX_OPEN_CONNS DB_MAX_IDLE_CONNS DB_CONN_MAX_LIFETIME_SECONDS DB_CONN_MAX_IDLE_TIME_SECONDS DB_POOL_ROLE
DB_API_MAX_OPEN_CONNS DB_API_MAX_IDLE_CONNS DB_API_CONN_MAX_LIFETIME_SECONDS DB_API_CONN_MAX_IDLE_TIME_SECONDS DB_WORKER_MAX_OPEN_CONNS DB_WORKER_MAX_IDLE_CONNS DB_WORKER_CONN_MAX_LIFETIME_SECONDS DB_WORKER_CONN_MAX_IDLE_TIME_SECONDS
DB_MAINTENANCE_MAX_OPEN_CONNS DB_MAINTENANCE_MAX_IDLE_CONNS DB_MAINTENANCE_CONN_MAX_LIFETIME_SECONDS DB_MAINTENANCE_CONN_MAX_IDLE_TIME_SECONDS
DB_HOST DB_PORT DB_USER DB_PASSWORD DB_NAME DB_SSLMODE EXPECTED_DATABASE_INSTALLATION_UUID
YOUTUBE_UPLOAD_CHUNK_BYTES YOUTUBE_UPLOAD_MAX_RETRIES YOUTUBE_UPLOAD_BACKOFF_BASE_MS YOUTUBE_UPLOAD_BACKOFF_CAP_MS
YOUTUBE_DAILY_UPLOAD_LIMIT YOUTUBE_SEARCH_QUOTA_LIMIT YOUTUBE_GENERAL_QUOTA_LIMIT
YOUTUBE_DAILY_QUOTA_LIMIT (legacy uploads fallback)
YOUTUBE_GROUP_VIDEOS_MAX_ACCOUNTS YOUTUBE_GROUP_VIDEOS_MAX_VIDEOS
YOUTUBE_GROUP_VIDEOS_CACHE_TTL_SECONDS YOUTUBE_GROUP_VIDEOS_DEFAULT_PAGE_SIZE
PUBLISH_HORIZON_DAYS VIDEO_RETENTION_BUFFER_DAYS
PUBLISH_WORKER_INTERVAL_SECONDS RECONCILE_WORKER_INTERVAL_SECONDS WEBHOOK_WORKER_INTERVAL_SECONDS
WEBHOOK_WORKER_CONCURRENCY WEBHOOK_HTTP_TIMEOUT_SECONDS WEBHOOK_LEASE_TTL_SECONDS WEBHOOK_HEARTBEAT_INTERVAL_SECONDS
SESSION_CLEANUP_INTERVAL_SECONDS ASSET_CLEANUP_INTERVAL_SECONDS UPLOAD_WORKER_INTERVAL_SECONDS
RENDER_MAX_CONCURRENCY FFMPEG_THREADS
TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS TOKEN_REFRESH_SWEEP_HORIZON_DAYS SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS
UPLOAD_INGEST_CONCURRENCY YOUTUBE_UPLOAD_CONCURRENCY UPLOAD_LEASE_TTL_SECONDS
UPLOAD_HEARTBEAT_INTERVAL_SECONDS UPLOAD_RECLAIM_INTERVAL_SECONDS UPLOAD_RECLAIM_ON_START
UPLOAD_EMPTY_QUEUE_BACKOFF_MIN_SECONDS UPLOAD_EMPTY_QUEUE_BACKOFF_MAX_SECONDS
APP_MODE
S3_ENDPOINT S3_BUCKET S3_PATH_STYLE S3_ACCESS_KEY S3_SECRET_KEY S3_REGION STORAGE_MAX_UPLOAD_BYTES
GOOGLE_DRIVE_API_KEY GOOGLE_DRIVE_UPLOAD_FOLDER_ID
ENCRYPTION_KEY ENCRYPTION_KEYS ACTIVE_ENCRYPTION_KEY_ID
STRIPE_SECRET_KEY STRIPE_WEBHOOK_SECRET STRIPE_SUCCESS_URL STRIPE_CANCEL_URL
```

`STRIPE_SUCCESS_URL` and `STRIPE_CANCEL_URL` are the only loader mappings with
an environment-derived default: when unset, they use `FRONTEND_URL` (falling
back to `http://localhost:5173`) and append the billing success/cancel path.

## Example files and drift

`.env.dev.example`, `.env.test.example`, and `.env.production.example` are
operator templates, not a complete substitute for the loader. For the editor
URL, new deployments should set `INSTAEDITOR_URL`; `EDITOR_URL` is retained as
an explicit backward-compatible fallback and may be removed only after every
runtime environment has migrated. Compose/system
variables such as `POSTGRES_*`, `MINIO_*`, `PORT`, and `VELOX_*_TIMEOUT` may be
consumed by infrastructure or entrypoints rather than `internal/config`.
Conversely, `DB_POOL_ROLE` and `EXPECTED_DATABASE_INSTALLATION_UUID` are
application settings that should be supplied explicitly for role-specific or
persistent deployments even when a template leaves them blank.

When adding a configuration field, update all applicable surfaces together:

- the typed field in `config.go`;
- the env mapping/default in `config_load.go`;
- validation in `config_validation.go` when a policy exists;
- focused tests under `internal/config`;
- the relevant `.env.*.example` and this inventory.

Do not add a generic reflection-based loader: explicit typed mappings keep
secret handling, defaults, and validation auditable.
