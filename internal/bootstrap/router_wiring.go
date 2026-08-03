package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxclient"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxjobs"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/getsentry/sentry-go"
)

func buildRouterWiring(s *wireState) (*api.Router, *sentry.Hub, error) {
	corsOrigins := s.cfg.HTTP.AllowedCORSOrigins
	if len(corsOrigins) == 0 && s.cfg.HTTP.FrontendURL != "" {
		corsOrigins = []string{s.cfg.HTTP.FrontendURL}
	}

	// Parse the trusted proxy list once at startup so IP extraction
	// and the rate limiter agree on which peers may supply
	// X-Forwarded-For / X-Real-IP headers.
	trustedProxies, err := api.ParseTrustedProxies(s.cfg.Auth.TrustedProxies)
	if err != nil {
		return nil, nil, fmt.Errorf("parse TRUSTED_PROXIES: %w", err)
	}

	userWorkspaceHelper := api.RepoUserWorkspaceHelper(s.workspaceRepo, s.teamRepo)
	authEmailBackend := services.NewAuthService(s.userRepo, s.workspaceRepo, s.teamRepo)
	authEmailSvc := api.NewAuthEmailServiceAdapter(authEmailBackend)

	sessionRepo := repository.NewSessionRepository(s.db)
	s.sessionsSvc = services.NewSessionsService(sessionRepo, s.authMgr)

	rateLimitRepo := repository.NewRateLimitRepository(s.db)
	rateLimitSvc := services.NewRateLimitServiceWithMemory(rateLimitRepo, s.memoryLimiter)
	analyticsClock := analytics.RealClock{}

	// Booking-event repo backing POST /api/v1/booking_events
	// (anonymous strategy-call capture from the marketing
	// BookingProvider modal). Wired unconditionally — the
	// module's nil-guard handles the "I really don't want this
	// endpoint" case via api.WithBookingEventStore(nil) instead
	// of a conditional Repo construction here.
	opts := []api.RouterOption{
		api.WithCredentialVault(s.vault),
		// Task 1/10 atomic OAuth finalize gate. Wired
		// unconditionally before the storage provider so the
		// field on Router is non-nil by the time Setup() runs.
		api.WithChannelAuthorizer(s.channelAuthorizer),
		api.WithStorageProvider(s.storageProvider),
		api.WithMaxUploadBytes(s.cfg.Storage.MaxUploadBytes),
		api.WithApiKeyStore(s.apiKeyRepo),
		api.WithApiKeyAuthenticator(s.apiKeyAuth),
		api.WithIdempotencyStore(s.idempotencyRepo),
		api.WithUserWorkspaceHelper(userWorkspaceHelper),
		api.WithTeamStore(s.teamRepo),
		api.WithGroupStore(s.groupRepo),
		api.WithAuthEmailService(authEmailSvc),
		api.WithSessionsService(s.sessionsSvc),
		api.WithWorkspaceStore(s.workspaceRepo),
		api.WithPostStore(s.postRepo),
		api.WithMediaStore(s.mediaRepo),
		api.WithUploadJobStore(s.uploadJobRepo),
		// P2 — ops dashboard store. AdminRepository powers every
		// /admin/* endpoint (channels / queue / health + their
		// .csv variants). When nil the route table short-circuits
		// the admin registration block (handlers.go Setup()).
		api.WithAdminStore(repository.NewAdminRepository(s.db)),
		// P1#7 — producer-side handler (POST
		// /api/v1/media/import/drive/folder/async) and poll endpoint
		// (GET .../async/{id}) share this ImportBatchStore. The
		// crawler worker uses the SAME *repository.ImportBatchRepository
		// but through a narrower CrawlerBatchStore interface
		// declared in internal/worker/drive_batch_crawler.go.
		api.WithImportBatchStore(s.importBatchRepo),
		// P1#7 — exporter for the crawler goroutine spawned in
		// RunWorkers. Same instance as ImportBatchStore above; the
		// split into two interfaces lets each consumer request only
		// the methods it actually calls.
		api.WithConnectionStateStore(&connectionStateStoreWrapper{s.connectionStateRepo}),
		api.WithAuditLogStore(&auditLogStoreWrapper{s.auditLogRepo}),
		api.WithExternalDestinationStore(s.externalDestinationRepo),
		api.WithExternalDeliveryStore(s.externalDeliveryRepo),
		api.WithConnectLinkNonceStore(s.connectLinkNonceRepo),
		api.WithVeloxAPIToken(os.Getenv("VELOX_API_TOKEN")),
		// P2 Velox BFF — wire the typed client that signs a short-lived
		// JWT (VELOX_CONTROL_JWT_SECRET) and calls the Velox master
		// (VELOX_CONTROL_URL). When either env is empty, veloxclient.New
		// returns nil and the VeloxBFFModule does not mount its routes
		// (nil-guard pattern matching the other feature flags). The auth
		// + CSRF middlewares mirror the destinations route wiring so the
		// /api/v1/velox/* chain is: auth → CSRF → handler.
		api.WithVeloxJobRegistry(veloxjobs.NewDefaultRegistry()),
		func() api.RouterOption {
			vc := veloxclient.New(s.cfg.Velox.VeloxControlURL, s.cfg.Velox.VeloxControlJWTSecret)
			if vc == nil {
				slog.Info("velox BFF client not configured (VELOX_CONTROL_URL or VELOX_CONTROL_JWT_SECRET empty) — /api/v1/velox/* routes not mounted")
				return func(*api.Router) {} // no-op option
			}
			slog.Info("velox BFF client configured",
				"control_url", s.cfg.Velox.VeloxControlURL)
			return api.WithVeloxBFFClient(vc)
		}(),
		api.WithVeloxBFFAuthMiddleware(s.authMgr.Middleware),
		api.WithVeloxBFFCSRFMiddleware(func(next http.Handler) http.Handler {
			return auth.NewCSRF(auth.CSRFConfig{
				Secure:       true,
				Path:         "/",
				CookieDomain: s.cfg.HTTP.CookieDomain,
				SameSite:     http.SameSiteNoneMode,
			}, next)
		}),
		api.WithCookieSecure(true),
		// csrf_token cookie Domain (Blocco #2.4): threaded from
		// cfg.HTTP.CookieDomain (COOKIE_DOMAIN env var). Empty stays
		// host-only, which is correct for dev (localhost crosses
		// different ports and a parent-domain match wouldn't help).
		// Production sets e.g. ".instaedit.org" so the SPA on
		// app.instaedit.org can read the csrf_token via
		// document.cookie against the API on api.instaedit.org.
		// Session + refresh cookies deliberately remain host-only:
		// they are HttpOnly on the API origin, JS cannot read them
		// anyway, and giving them a Domain would only widen the
		// CSRF attack surface for zero security upside.
		api.WithCookieDomain(s.cfg.HTTP.CookieDomain),
		api.WithRateLimitService(rateLimitSvc),
		api.WithWebhookStore(s.webhookRepo),
		api.WithBookingEventStore(s.bookingEventRepo),
		// ADMIN_INVITE_TOKEN gates public registration. If the env
		// is unset, registration is disabled (handler returns 403).
		api.WithAdminInviteToken(s.cfg.Auth.AdminInviteToken),
		api.WithSnapshotStore(repository.NewSnapshotRepository(s.db)),
		api.WithMetricHistoryStore(repository.NewAccountMetricsRepository(s.db)),
		api.WithChannelAnalyticsService(
			api.NewChannelAnalyticsService(
				s.userRepo,
				repository.NewAccountMetricsRepository(s.db),
				api.WithAnalyticsClock(analyticsClock),
			),
		),
		api.WithRouterAnalyticsClock(analyticsClock),
		api.WithYouTubeVideoEditStore(s.youtubeVideoEditRepo),
		api.WithYouTubeThumbnailBatchStore(s.youtubeThumbnailBatchRepo),
		api.WithLivestreamStore(s.livestreamRepo),
		api.WithContentPipelineStore(s.contentPipelineRepo),
		api.WithEditorURL(s.cfg.HTTP.EditorURL),
		// Blocco #2 P0 — wire the env-driven publish-horizon +
		// retention-buffer values into the Router so handleRescheduleUpload,
		// the batch V2 producer's horizon comparison, and
		// r.computeMediaAssetLifetime (all media_asset create sites)
		// read from a single source of truth. Without this option
		// the helpers fall through to the safe defaults (30/7).
		api.WithScheduleLimits(api.ScheduleLimits{
			PublishHorizonDays:       s.cfg.Worker.PublishHorizonDays,
			VideoRetentionBufferDays: s.cfg.Worker.VideoRetentionBufferDays,
		}),
		api.WithYouTubeGroupVideosConfig(api.YouTubeGroupVideosConfig{
			MaxAccounts:     s.cfg.Worker.YouTubeGroupVideosMaxAccounts,
			MaxVideos:       s.cfg.Worker.YouTubeGroupVideosMaxVideos,
			CacheTTL:        time.Duration(s.cfg.Worker.YouTubeGroupVideosCacheTTLSeconds) * time.Second,
			DefaultPageSize: s.cfg.Worker.YouTubeGroupVideosDefaultPageSize,
		}),
		// P1#7 — export the importBatchRepo on App so the
		// command-line crawler (cmd/worker) can wire it directly.
	}

	// Wire NVIDIA AI metadata generator (optional). When NVIDIA_API_KEY
	// is empty, the /generate-metadata endpoint returns 503 and manual
	// metadata entry works unchanged. Constructed unconditionally
	// (even with empty key) so the nil-guard is in the handler, not
	// in the wiring — a future env-var reload (unlikely but
	// architecturally sound) would pick up the key without a restart.
	nvidiaSvc := services.NewMetadataGenerator(s.cfg.AI.NVIDIAAPIKey)
	if s.cfg.AI.NVIDIAAPIKey != "" {
		slog.Info("NVIDIA AI metadata generator configured")
	} else {
		slog.Info("NVIDIA AI metadata generator NOT configured (NVIDIA_API_KEY empty) — /generate-metadata returns 503, manual entry still works")
	}
	opts = append(opts, api.WithNvidiaMetadataService(nvidiaSvc))

	// Wire the YouTube service into the router for editor-sessions
	// and validate-account endpoints. Hard-fail when YouTubeClientID
	// is configured but the provider is missing or does not implement
	// the capability interface — a silent no-op would leave
	// r.youTubeSvc nil and the /accounts/{id}/validate 4-step
	// pipeline would fall back to the legacy token-freshness probe.
	// The /cmd/api and /cmd/server entrypoints both call Wire() here
	// (NOT bootstrap.WireAPI in api.go), so this is the production
	// injection site; api.go carries the same wiring for the
	// WireCore+WireAPI refactor path.
	rawYouTubeService, youtubeRegistered :=
		s.capRouter.Get(models.PlatformYouTube)

	if s.cfg.Auth.YouTubeClientID != "" && !youtubeRegistered {
		return nil, nil, fmt.Errorf(
			"youtube configured but provider missing from capability registry",
		)
	}

	if youtubeRegistered {
		youtubeService, ok :=
			rawYouTubeService.(*services.YouTubeOAuthService)
		if !ok {
			return nil, nil, fmt.Errorf(
				"youtube registry returned unexpected provider type %T",
				rawYouTubeService,
			)
		}

		// This assignment is also a compile-time interface check.
		var editorService api.YouTubeOAuthService = youtubeService

		opts = append(
			opts,
			api.WithYouTubeService(editorService),
		)

		slog.Info(
			"YouTube API service wired",
			"provider_type", fmt.Sprintf("%T", youtubeService),
		)
	}

	// Blocco #5.3 — Sentry init (lazy). Empty DSN disables the SDK.
	var hub *sentry.Hub
	if s.cfg.Monitoring.SentryDSN != "" {
		clientOpts := sentry.ClientOptions{
			Dsn:         s.cfg.Monitoring.SentryDSN,
			Environment: s.cfg.Monitoring.SentryEnvironment,
			Release:     s.cfg.Monitoring.SentryRelease,
		}
		configuredHub, sentryErr := configureSentry(clientOpts)
		if sentryErr != nil {
			slog.Warn("sentry init failed; recovery middleware will run without Sentry capture", "error", sentryErr)
		} else if configuredHub != nil {
			hub = configuredHub
			slog.Info("sentry configured",
				"environment", s.cfg.Monitoring.SentryEnvironment,
				"release", s.cfg.Monitoring.SentryRelease)
		}
	} else {
		slog.Info("sentry disabled (SENTRY_DSN empty)")
	}

	// Inject the Sentry hub into the router options so the recovery
	// middleware can read it via the Router field (not via the App
	// field — pkg/api stays decoupled from internal/bootstrap).
	opts = append(opts, api.WithSentryHub(hub))

	// Trusted proxies are applied AFTER all options so both
	// clientIP() and the rate limiter see the same parsed list.
	opts = append(opts, api.WithTrustedProxies(trustedProxies))

	// Metrics basic-auth credentials are wired explicitly so the
	// /api/v1/metrics handler does not read env vars at request
	// time. Incomplete credentials trigger fail-closed 503 in the
	// handler; production boot already rejects them in
	// cfg.validate().
	opts = append(opts, api.WithMetricsAuth(s.cfg.Monitoring.MetricsBasicAuthUser, s.cfg.Monitoring.MetricsBasicAuthPass))

	// Blocco #5.3 — wire the DB into /ready's contract. API readiness
	// now only checks DB ping + schema migrations; worker readiness is
	// exposed separately by the worker process via the WorkerRegistry.
	opts = append(opts, api.WithDB(s.db))

	router, err := api.NewRouter(s.capRouter, s.userRepo, s.authMgr, s.cfg.HTTP.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(s.oneTimeCodes)}, opts...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("build router: %w", err)
	}

	slog.Info("Router configured",
		"jwt_access_ttl_minutes", s.cfg.Auth.JWTAccessTTLMinutes,
		"jwt_refresh_ttl_days", s.cfg.Auth.JWTRefreshTTLDays,
		"frontend_url", s.cfg.HTTP.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", s.capRouter.Names(),
		"api_keys_enabled", s.apiKeyRepo != nil,
		"sentry_enabled", hub != nil,
		"ready_endpoint", "/ready")

	return router, hub, nil
}
