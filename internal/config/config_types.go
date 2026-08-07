package config

// Config holds all configuration for the application.
//
// Taglio 5b: SERVER_PORT + SERVER_HOST removed — the server listens on the
// PORT env var only (Vercel / Railway / Render standard). TWITTER_* env vars
// renamed to X_*; TIKTOK_CLIENT_KEY renamed to TIKTOK_CLIENT_ID.
type Config struct {
	// VeloxAPIToken authenticates artifact HEAD/GET requests back to Velox.
	// This is the REVERSE direction token (Velox → InstaEdit via
	// /internal/v1/* routes). Loaded from VELOX_API_TOKEN.

	// VeloxControlURL is the base URL of the Velox master that the
	// BFF calls when proxying user-facing /api/v1/velox/* requests.
	// The browser never sees this URL — only InstaEdit calls it.
	// Loaded from VELOX_CONTROL_URL. Empty = BFF routes not mounted.

	// VeloxControlJWTSecret is the shared HS256 secret for the
	// short-lived JWT InstaEdit signs when calling the Velox master.
	// This MUST be the same value as VeloxEditiingg's
	// INSTAEDIT_CONTROL_JWT_SECRET. It is DISTINCT from
	// VeloxAPIToken (the reverse-direction Bearer token) — the two
	// secrets MUST NOT be reused across directions. Loaded from
	// VELOX_CONTROL_JWT_SECRET. Empty = BFF routes not mounted.

	// VeloxWebhookSecret is the shared HMAC-SHA256 secret used to
	// sign callbacks sent from InstaEdit to Velox. It is distinct
	// from VeloxAPIToken and VeloxControlJWTSecret. Loaded from
	// VELOX_WEBHOOK_SECRET.
	// FrontendURL is where the OAuth callback should redirect.
	// AllowedCORSOrigins is the comma-separated list of origins.

	// Database (PostgreSQL).
	Database DatabaseConfig

	// Storage (S3-compatible + Google Drive upload folder).
	Storage StorageConfig

	// Auth (OAuth + JWT + security tokens).
	Auth AuthConfig

	// Velox integration secrets.
	Velox VeloxConfig

	// AI/ML provider secrets (NVIDIA, etc.).
	AI AIConfig

	// Monitoring (Sentry + metrics).
	Monitoring MonitoringConfig

	// P1#6 — YouTube resumable-upload tuning. The resumable upload
	// protocol streams the binary in N chunks; Google requires each
	// chunk be a multiple of 256 KB (262144 bytes) and recommends
	// larger chunks for fewer round-trips (valutazione doc spec:
	// 16 MB default). Backoff uses full-jitter exponential growth
	// capped at 5 min, with Retry-After from the server ALWAYS honored
	// (the cap applies only to the calculated fallback when the
	// server didn't send a hint — see youtube_oauth.go for the
	// rationale: capping a server hint to a smaller value guarantees
	// we'd hammer the API mid-quota-window and risk blacklisting).
	//
	// These are independent of the upload-job-level retries in
	// internal/worker/upload_worker.go::computeUploadBackoff; chunk
	// retries recover a transient network blip during the PUT, while
	// job-level retries recover a budget-exhausted publish that the
	// inner chunk loop couldn't escape.

	// Encryption (Blocco #2.2 — multi-key support).
	//
	// Two parallel surfaces:
	//
	//   1. ENCRYPTION_KEY + (implied) ACTIVE_ENCRYPTION_KEY_ID=1
	//      — the legacy single-key path. Pre-Blocco #2.2 deployments
	//      set only ENCRYPTION_KEY. Validate() promotes that single
	//      key into EncryptionKeys[1] and ActiveEncryptionKeyID=1,
	//      so Wire() and every consumer sees the same struct shape
	//      regardless of which env-var surface the operator uses.
	//
	//   2. ENCRYPTION_KEYS (CSV: id:base64key,id:base64key,...) +
	//      ACTIVE_ENCRYPTION_KEY_ID (uint32) — the multi-key path.
	//      Validate() parses the CSV, validates every key
	//      (base64 + 32 bytes), and confirms the active id is
	//      present in the map.
	//
	// Mixing both surfaces is a misconfiguration: validate() rejects
	// "both ENCRYPTION_KEY and ENCRYPTION_KEYS set" with a
	// descriptive error so operators can act on it.
	EncryptionKey string // LEGACY: single-key fallback
	// EncryptionKeys is the post-validation map of all key ids
	// known to this process. Always populated: either from
	// ENCRYPTION_KEYS (multi-key) or from the legacy fallback.
	EncryptionKeys map[uint32]string
	// ActiveEncryptionKeyID is the key id used for new Encrypt
	// calls. Always populated: either from ACTIVE_ENCRYPTION_KEY_ID
	// (multi-key) or 1 (legacy fallback).
	ActiveEncryptionKeyID uint32
	// EncryptionKeysRaw is the unparsed ENCRYPTION_KEYS string,
	// preserved here only for the validate() error message when
	// the CSV is malformed. Not used outside validation.
	EncryptionKeysRaw string
	// ActiveEncryptionKeyIDRaw is the unparsed ACTIVE_ENCRYPTION_KEY_ID
	// string, same purpose as EncryptionKeysRaw.
	ActiveEncryptionKeyIDRaw string

	// JWT
	// TrustedProxies is a comma-separated list of IP addresses and/or
	// CIDR ranges that are allowed to supply X-Forwarded-For /
	// X-Real-IP headers. When empty, the API trusts only the direct
	// peer address (RemoteAddr). Example: "10.0.0.0/8,127.0.0.1".

	// Metrics basic-auth credentials. In production both must be set;
	// validate() fail-closes the boot if either is empty.
	// MetricsHost/MetricsPort optionally start a separate internal
	// listener for the /metrics endpoint. When MetricsPort is 0, the
	// endpoint is served only on the main HTTP server at
	// /api/v1/metrics. When MetricsPort > 0, an additional listener
	// is started on MetricsHost:MetricsPort (default MetricsHost
	// 127.0.0.1 if empty) so scrapers on a private network can reach
	// metrics without exposing the main API.

	// Deprecated: JWT_TTL_HOURS is the legacy single-knob TTL.
	// If JWT_ACCESS_TTL_MINUTES is unset, the hours value is
	// converted to minutes. Prefer the explicit access/refresh
	// variables for new deployments.

	// Logging

	// AppEnv is the deployment environment.

	// Background worker tuning.
	// Taglio 5.x — independent tick interval for the new
	// ReconcileWorker goroutine. The driver (PublishWorker) ticks
	// at PublishWorkerIntervalSeconds (default 30s); the reconciler
	// ticks faster (default 5s) so an async publish's
	// publishing→published transition is observed promptly without
	// coupling to the driver's cadence. Both run as separate
	// goroutines on independent contexts with parallel shutdown.
	// SPRINT 4.2 — independent tick interval for the WebhookWorker
	// goroutine. Drains the webhook_deliveries table every
	// WEBHOOK_WORKER_INTERVAL_SECONDS (default 5s). Faster than
	// the publish driver so an end-to-end delivery latency is
	// bounded by a 1-2s ceiling under typical load. Same
	// lifecycle shape: independent goroutine, ctx-cancellable,
	// drained in parallel on shutdown.

	// SessionCleanupIntervalSeconds — cadence of the retention
	// policy goroutine (commit: cleanup-policy). Drives the
	// periodic SessionsCleanupWorker that DELETEs rows from the
	// `sessions` table whose revoked_at is older than 30 days OR
	// whose refresh_expires_at is older than 7 days. Default 300s
	// (5 min) is coarse enough to not thrash the DB under traffic
	// spikes but fine-grained enough to keep the sessions table
	// bounded under normal load.

	// UploadWorkerIntervalSeconds — cadence of the background upload
	// worker that drains upload_jobs (public or authenticated Google
	// Drive imports). Default 30s.

	// P1 step 2 — ingest pool / upload pool split. The upload_worker
	// package now spawns two parallel pools against the upload_jobs
	// queue, each with its own concurrency cap (the valutazione doc
	// recommends 2–3 ingest + 3–4 YouTube-upload on dev boxen,
	// scaling only after RAM/disk/bandwidth measurements).
	//
	// The ingest pool claims status IN ('pending','retry_wait') and
	// streams Drive→S3; the upload pool claims status =
	// 'ready_to_publish' and runs videos.insert. Both pools use the
	// same lease + heartbeat machinery, with distinct workerID
	// prefixes so a Mark* CAS can never collide.

	// Stripe billing (optional — billing endpoints are 501 when not configured).
	StripeSecretKey     string
	StripeWebhookSecret string
	StripeSuccessURL    string
	StripeCancelURL     string

	// Sentry (optional, Blocco #5.3).
	//
	// SENTRY_DSN is the SDK DSN string (`https://key@sentry.io/projid`).
	// Empty (the default) disables the entire observability surface:
	//   - sentry.Init is NOT called at startup.
	//   - the panic-catching middleware falls back to a plain
	//     `recover(http.Handler)` that writes 500 with NO outbound
	//     network traffic.
	// - Non-empty: sentry.Init runs at startup; the panic-catching
	//   middleware wraps with sentryhttp.New so CaptureException is
	//   called for every recovered panic and the SDK buffers out-of-band.
	//
	// SENTRY_ENVIRONMENT defaults to AppEnv ("dev"/"staging"/"production")
	// when empty; SENTRY_RELEASE is passed straight through to the SDK
	// (the operator typically wires this to the deploy SHA via the CI
	// pipeline). Both are passed via env so the production deploy can
	// set them without re-baking the binary.

	// CookieDomain is the optional `Domain` attribute applied to the
	// csrf_token cookie ONLY (session + refresh cookies stay host-only).
	// Defaults to empty so dev (localhost:5173 + localhost:8080) keeps
	// working unchanged. Production sets it to e.g. ".instaedit.org"
	// so the SPA on app.instaedit.org can read the csrf_token via
	// document.cookie over a cross-origin backend on api.instaedit.org.
	// Use a leading dot to make the cookie available to every
	// subdomain; the value is passed straight through to Go's
	// http.Cookie Domain field (which the browser interprets per
	// RFC 6265). Validation is intentionally NOT applied — the
	// HTTPS / SameSite=None / leading-dot trade-off is the
	// operator's call.

	// AdminInviteToken gates the public registration endpoint
	// (POST /api/v1/auth/register). The handler requires the request
	// to present the same value via the X-Admin-Token header
	// (constant-time compare). When empty, registration is fully
	// disabled (the handler returns 403 "registration is
	// invite-only"). Generate with `openssl rand -hex 32` and
	// rotate via VPS .env edit + `docker compose restart`. NOT logged, NOT exposed
	// in error messages.

	// AppMode lets operators pin the deployment to Google's OAuth-
	// consent-screen publishing status. "production" means refresh
	// tokens are durable (no automatic 7-day expiry). "testing"
	// means Google's Testing-mode 7-day refresh-token TTL applies
	// and any refresh attempt after day 7 returns invalid_grant.
	// Default "production" so a missing env var falls into the safer
	// bucket; ops must explicitly opt-in to "testing" when validating
	// against a staging OAuth-client. Loaded from env APP_MODE.
	AppMode string
	// HTTP/server surface configuration.
	HTTP HTTPConfig
	// Background worker tuning.
	Worker WorkerConfig
}
