// Package bootstrap owns the shared startup wiring for every InstaEditLogin
// binary (cmd/api, cmd/worker, cmd/migrate, cmd/server).
//
// Blocco #2.1 split cmd/server/main.go into:
//   - cmd/api     — HTTP only
//   - cmd/worker  — 7 background goroutines (publish, reconcile, outbox,
//     webhook, metrics, sessions_cleanup, upload)
//   - cmd/migrate — Connect + Migrate + exit (one-shot pre-deploy job)
//   - cmd/server  — wrapper: dev/local-compat single-bundle that runs
//     migrate + api + (optionally) workers in one process.
//
// Migrate is NOT part of Wire() on purpose: the production deploy topology
// runs cmd/migrate as a one-shot pre-deploy job, so api/worker MUST NOT
// re-run Migrate() — they'd race against an in-flight migration job. The
// dev wrapper cmd/server does call Migrate() (via internal/database.Migrate)
// because it assumes "this is the only process touching the DB just now".
package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getsentry/sentry-go"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// Compile-time interface check: *services.YouTubeOAuthService must
// satisfy api.YouTubeOAuthService. If a future signature drift breaks
// the contract the build fails here instead of leaving r.youTubeSvc
// nil at runtime. Mirrors the existing
// `var _ YouTubeOAuthService = (*services.YouTubeOAuthService)(nil)`
// in pkg/api/router.go and is duplicated on purpose so a signature
// drift in either direction is caught at the nearest compile site.
var _ api.YouTubeOAuthService = (*services.YouTubeOAuthService)(nil)

type App struct {
	Cfg         *config.Config
	DB          *sql.DB
	Vault       credentials.VaultAPI
	CapRouter   *services.CapabilityRouter
	WebhookRepo *repository.WebhookRepository
	HTTPHandler http.Handler
	// Router is the wired *api.Router (NOT the http.Handler setup
	// result). RunWorkers reads it to build the
	// youtube_processing_reconciler adapter which depends on
	// Router.CreateEditorSession (Blocco #4 P0). Exposed as a
	// separate field rather than relying on HTTPHandler reversal:
	// keeping the live *Router pointer around is cheap (it owns
	// routes + stores) and lets worker wiring avoid an unholy
	// type-assertion chain on http.Handler.
	Router *api.Router
	Logger *slog.Logger

	// WorkerRegistry (Blocco #5.3 refactor) supervises the lifecycle
	// of every background goroutine. It is constructed in Wire() and
	// consumed by RunWorkers() + the worker health listener.
	WorkerRegistry *worker.Registry

	// SentryHub (Blocco #5.3). Nil when SENTRY_DSN is empty
	// (operator-disables-by-omission contract). When non-nil, the
	// panic-catching middleware uses sentryhttp.New() against
	// this hub so CaptureException flows correct on every panic.
	SentryHub *sentry.Hub

	// WorkerID (commit DI refactor) is the per-process identity
	// generated locally via metrics.NewWorkerID and threaded into
	// each worker's constructor — no global singleton, no
	// sync.Once. Stored on App so external callers (and the
	// RunWorkers goroutine-launch closures) can pass it on.
	WorkerID string

	// MemoryLimiter (commit DI refactor) is constructed once in
	// Wire() and shared between RateLimitService (request path)
	// and the workers (background path). Single instance per
	// process; explicit receiver avoids a sync.Once-protected
	// lazy global. The reaper goroutine dies with the process, so
	// no Shutdown() wiring is strictly required — the field is
	// exposed for future graceful-drain work.
	MemoryLimiter *services.MemoryLimiter

	// StorageProvider is the S3-compatible storage backend. Shared
	// between the API (presign / complete / drive import) and the
	// upload worker (background Drive → S3 streaming).
	StorageProvider services.StorageProvider

	// Encryptor (Task 8/10) exposes *crypto.Encryptor to RunWorkers
	// so the DeliveryRegistry can wire services.SessionEncryptor
	// for the Drive destination's session-URI ciphertext. Same
	// instance constructed at the top of Wire(); we expose it as
	// a field rather than a setter so RunWorkers reads a
	// single canonical reference.
	Encryptor *crypto.Encryptor

	// SessionsSvc is the wired *SessionsService, populated by
	// Wire(). cmd/worker reads it to drive the retention-policy
	// goroutine (SessionsCleanupWorker); cmd/api reads it through
	// the router (which already gets a copy via WithSessionsService
	// in the Wire's opts block). Exposing it as a field avoids
	// re-constructing the service in RunWorkers — the same instance
	// is shared across the api and worker processes.
	SessionsSvc *services.SessionsService

	// OneTimeCodes is the PostgreSQL-backed OAuth-callback bridge store
	// (Taglio 1.2). cmd/api consumes it via the router's
	// WithOneTimeCodeStore option (redirect/exchange handlers).
	// cmd/worker's RunWorkers calls OneTimeCodes.Stop() during
	// graceful shutdown so the background sweep goroutine exits
	// cleanly. Without this wiring, SIGTERM would let the sweeper
	// become a zombie until the process is killed.
	OneTimeCodes api.OneTimeCodeStore

	// YouTubeCredentialResolver is the shared runtime-only credential
	// boundary for YouTube Live workers and services.
	YouTubeCredentialResolver *services.YouTubeCredentialResolver
	YouTubeLiveGateway        services.YouTubeLiveGateway
}

// Wire connects to the database, builds every shared dependency, and
// returns a fully-wired *App. It does NOT run migrations and does NOT
// start any goroutine — callers choose what to run. Returns an error
// on config / database / encryption-key / provider-registry failures
// (these are fail-fast at startup, never silent).
//
// Taglio 3.1: S3 storage is mandatory. Wire panics — via the returned
// error — when S3 config is missing (the caller decides how to handle
// it; the wrapper cmd/server treats Wire errors as fatal-exit).
func Wire(ctx context.Context) (*App, error) {
	_ = ctx
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	s, err := buildDatabaseStorage(cfg)
	if err != nil {
		return nil, err
	}
	if err := buildProviderWiring(s); err != nil {
		return nil, err
	}
	router, hub, err := buildRouterWiring(s)
	if err != nil {
		return nil, err
	}
	return &App{
		Cfg: s.cfg, DB: s.db, Vault: s.vault, CapRouter: s.capRouter,
		WebhookRepo: s.webhookRepo, HTTPHandler: router.Setup(), Router: router,
		Logger: s.logger, WorkerRegistry: worker.NewRegistry(), SentryHub: hub,
		WorkerID: s.workerID, MemoryLimiter: s.memoryLimiter,
		StorageProvider: s.storageProvider, SessionsSvc: s.sessionsSvc,
		OneTimeCodes: s.oneTimeCodes, Encryptor: s.enc,
		YouTubeCredentialResolver: s.youtubeCredentialResolver,
		YouTubeLiveGateway:        s.youtubeLiveGateway,
	}, nil
}
