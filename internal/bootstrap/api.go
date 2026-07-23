package bootstrap

import (
	"net/http"
	"os"

	"github.com/getsentry/sentry-go"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxclient"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// WireAPI builds the HTTP handler using only the shared Core.
// It must be called after WireCore. It constructs the router with
// all API-specific dependencies (repositories, services, middleware,
// feature flags) and returns a ready-to-serve http.Handler.
func WireAPI(core *Core) (http.Handler, error) {
	cfg := core.Cfg

	corsOrigins := cfg.AllowedCORSOrigins
	if len(corsOrigins) == 0 && cfg.FrontendURL != "" {
		corsOrigins = []string{cfg.FrontendURL}
	}

	trustedProxies, err := api.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}

	// channelAuthorizer — atomic OAuth finalize gate.
	var ytBinder services.YouTubeChannelBinder
	if ytp, ok := core.CapRouter.Get(models.PlatformYouTube); ok {
		b, typeOK := ytp.(services.YouTubeChannelBinder)
		if !typeOK {
			return nil, err
		}
		ytBinder = b
	}
	channelAuthorizer := services.NewChannelAuthorizationService(core.DB, core.Encryptor, core.tokenRepo, ytBinder)

	apiKeyAuth := auth.NewApiKeyAuthenticator(core.apiKeyRepo)
	userWorkspaceHelper := api.RepoUserWorkspaceHelper(core.workspaceRepo, core.teamRepo)
	authEmailBackend := services.NewAuthService(core.userRepo, core.workspaceRepo, core.teamRepo)
	authEmailSvc := api.NewAuthEmailServiceAdapter(authEmailBackend)

	rateLimitRepo := repository.NewRateLimitRepository(core.DB)
	rateLimitSvc := services.NewRateLimitServiceWithMemory(rateLimitRepo, core.MemoryLimiter)

	opts := []api.RouterOption{
		api.WithCredentialVault(core.Vault),
		api.WithChannelAuthorizer(channelAuthorizer),
		api.WithStorageProvider(core.Storage),
		api.WithMaxUploadBytes(cfg.MaxUploadBytes),
		api.WithApiKeyStore(core.apiKeyRepo),
		api.WithApiKeyAuthenticator(apiKeyAuth),
		api.WithIdempotencyStore(core.idempotencyRepo),
		api.WithUserWorkspaceHelper(userWorkspaceHelper),
		api.WithTeamStore(core.teamRepo),
		api.WithAuthEmailService(authEmailSvc),
		api.WithSessionsService(core.sessionsSvc),
		api.WithWorkspaceStore(core.workspaceRepo),
		api.WithPostStore(core.postRepo),
		api.WithMediaStore(core.mediaRepo),
		api.WithUploadJobStore(core.uploadJobRepo),
		api.WithAdminStore(repository.NewAdminRepository(core.DB)),
		api.WithImportBatchStore(core.importBatchRepo),
		api.WithConnectionStateStore(&connectionStateStoreWrapper{core.connectionStateRepo}),
		api.WithAuditLogStore(&auditLogStoreWrapper{core.auditLogRepo}),
		api.WithExternalDestinationStore(core.externalDestinationRepo),
		api.WithExternalDeliveryStore(core.externalDeliveryRepo),
		api.WithConnectLinkNonceStore(core.connectLinkNonceRepo),
		api.WithVeloxAPIToken(os.Getenv("VELOX_API_TOKEN")),
		api.WithVeloxDownloadJobChannel(core.VeloxDownloadJobs),
		api.WithVeloxBFFClient(veloxClient(cfg)),
		api.WithVeloxBFFAuthMiddleware(core.authMgr.Middleware),
		api.WithVeloxBFFCSRFMiddleware(func(next http.Handler) http.Handler {
			return auth.NewCSRF(auth.CSRFConfig{
				Secure:       true,
				Path:         "/",
				CookieDomain: cfg.CookieDomain,
				SameSite:     http.SameSiteNoneMode,
			}, next)
		}),
		api.WithCookieSecure(true),
		api.WithCookieDomain(cfg.CookieDomain),
		api.WithRateLimitService(rateLimitSvc),
		api.WithWebhookStore(core.WebhookRepo),
		api.WithAdminInviteToken(cfg.AdminInviteToken),
		api.WithSnapshotStore(repository.NewSnapshotRepository(core.DB)),
		api.WithMetricHistoryStore(repository.NewAccountMetricsRepository(core.DB)),
	}

	// Sentry init (lazy). Empty DSN means no SDK.
	var hub *sentry.Hub
	if cfg.SentryDSN != "" {
		clientOpts := sentry.ClientOptions{
			Dsn:         cfg.SentryDSN,
			Environment: cfg.SentryEnvironment,
			Release:     cfg.SentryRelease,
		}
		if err := sentry.Init(clientOpts); err != nil {
			core.Logger.Warn("sentry init failed; recovery middleware will run without Sentry capture", "error", err)
		} else {
			hub = sentry.CurrentHub()
			core.Logger.Info("sentry configured",
				"environment", cfg.SentryEnvironment,
				"release", cfg.SentryRelease)
		}
	} else {
		core.Logger.Info("sentry disabled (SENTRY_DSN empty)")
	}
	opts = append(opts, api.WithSentryHub(hub))
	opts = append(opts, api.WithTrustedProxies(trustedProxies))
	opts = append(opts, api.WithMetricsAuth(cfg.MetricsBasicAuthUser, cfg.MetricsBasicAuthPass))
	opts = append(opts, api.WithDB(core.DB))

	router, err := api.NewRouter(core.CapRouter, core.userRepo, core.authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(core.OneTimeCodes)}, opts...)...)
	if err != nil {
		return nil, err
	}

	core.Logger.Info("Router configured",
		"jwt_access_ttl_minutes", cfg.JWTAccessTTLMinutes,
		"jwt_refresh_ttl_days", cfg.JWTRefreshTTLDays,
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", core.CapRouter.Names(),
		"api_keys_enabled", core.apiKeyRepo != nil,
		"sentry_enabled", hub != nil,
		"ready_endpoint", "/ready")

	return router.Setup(), nil
}

func veloxClient(cfg *config.Config) *veloxclient.Client {
	vc := veloxclient.New(cfg.VeloxControlURL, cfg.VeloxControlJWTSecret)
	if vc == nil {
		return nil
	}
	return vc
}
