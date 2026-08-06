package bootstrap

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

type wireState struct {
	cfg                       *config.Config
	logger                    *slog.Logger
	db                        *sql.DB
	workerID                  string
	memoryLimiter             *services.MemoryLimiter
	enc                       *crypto.Encryptor
	userRepo                  *repository.UserRepository
	tokenRepo                 *repository.TokenRepository
	teamRepo                  *repository.TeamRepository
	groupRepo                 *repository.GroupRepository
	workspaceRepo             *repository.WorkspaceRepository
	apiKeyRepo                *repository.ApiKeyRepository
	apiKeyAuth                *auth.Authenticator
	idempotencyRepo           *repository.IdempotencyRepository
	postRepo                  *repository.PostRepository
	mediaRepo                 *repository.MediaAssetRepository
	uploadJobRepo             *repository.UploadJobRepository
	importBatchRepo           *repository.ImportBatchRepository
	connectionStateRepo       *repository.ConnectionStateRepository
	auditLogRepo              *repository.AuditLogRepository
	externalDestinationRepo   *repository.ExternalDestinationRepository
	externalDeliveryRepo      *repository.ExternalDeliveryRepository
	connectLinkNonceRepo      *repository.ConnectLinkNonceRepository
	youtubeVideoEditRepo      *repository.YouTubeVideoEditRepository
	youtubeThumbnailBatchRepo *repository.YouTubeThumbnailBatchRepository
	livestreamRepo            *repository.LivestreamRepository
	thumbnailProjectRepo      *repository.ThumbnailProjectRepository
	thumbnailProjectService   *services.ThumbnailProjectService
	contentPipelineRepo       *repository.ContentPipelineRepository
	vault                     credentials.VaultAPI
	capRouter                 *services.CapabilityRouter
	authMgr                   *auth.Manager
	oneTimeCodes              api.OneTimeCodeStore
	storageProvider           services.StorageProvider
	sessionsSvc               *services.SessionsService
	webhookRepo               *repository.WebhookRepository
	bookingEventRepo          *repository.BookingEventRepository
	channelAuthorizer         services.ChannelAuthorizer
	youtubeCredentialResolver *services.YouTubeCredentialResolver
	youtubeLiveGateway        services.YouTubeLiveGateway
	renderRegistry            *worker.RenderConcurrencyRegistry
}

func buildDatabaseStorage(cfg *config.Config) (*wireState, error) {
	s := &wireState{cfg: cfg}
	var err error
	if cfg.Storage.S3Endpoint == "" || cfg.Storage.S3Bucket == "" || cfg.Storage.S3AccessKey == "" || cfg.Storage.S3SecretKey == "" {
		return nil, fmt.Errorf("S3 storage is required: set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
	}

	logLevel := slog.LevelInfo
	if cfg.HTTP.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	s.logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(s.logger)

	slog.Info("Environment", "app_env", cfg.HTTP.AppEnv)

	s.db, err = database.Connect(&cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	// Per-process worker id (commit DI refactor). Generated locally
	// rather than via metrics.InitWorkerID() so the value lives only
	// on App.WorkerID — each consumer (workers, log context lines)
	// receives it as an explicit value, not a global read.
	s.workerID = metrics.NewWorkerID()
	slog.Info("worker_id initialised", "worker_id", s.workerID)

	// Per-process rate-limit MemoryLimiter (commit DI refactor).
	// Constructed once, shared between RateLimitService and the
	// workers — single instance, no sync.Once-protected lazy global.
	s.memoryLimiter = services.NewMemoryLimiter()

	// One process-wide admission controller for every CPU-heavy media
	// subprocess. Workers receive this same instance through App so
	// separate ingest/publish paths cannot oversubscribe the host.
	s.renderRegistry = worker.NewRenderConcurrencyRegistry(
		cfg.Worker.RenderMaxConcurrency,
		cfg.Worker.FFmpegThreads,
	)

	// Blocco #2.2 — multi-key support. Wire() consumes the
	// post-validated EncryptionKeys map + ActiveEncryptionKeyID
	// regardless of which env-var surface the operator used:
	//   - ENCRYPTION_KEY (legacy single-key) → resolveEncryptionConfig
	//     promotes it into EncryptionKeys[1] with active=1
	//   - ENCRYPTION_KEYS + ACTIVE_ENCRYPTION_KEY_ID (multi-key) →
	//     the parsed CSV + the operator-chosen active id
	// This is the only call site in the codebase that constructs
	// the Encryptor from the Config — every other consumer reads
	// the already-validated *crypto.Encryptor through the App
	// struct or a narrower interface.
	s.enc, err = crypto.NewEncryptor(cfg.ActiveEncryptionKeyID, cfg.EncryptionKeys)
	if err != nil {
		return nil, fmt.Errorf("init encryptor: %w", err)
	}
	slog.Info("encryption configured",
		"active_key_id", cfg.ActiveEncryptionKeyID,
		"key_count", len(cfg.EncryptionKeys),
		"key_ids", config.SortedKeyIDs(cfg.EncryptionKeys))

	s.userRepo = repository.NewUserRepository(s.db)
	s.tokenRepo = repository.NewTokenRepository(s.db)
	s.teamRepo = repository.NewTeamRepository(s.db)
	s.groupRepo = repository.NewGroupRepository(s.db)
	s.workspaceRepo = repository.NewWorkspaceRepository(s.db)
	s.apiKeyRepo = repository.NewApiKeyRepository(s.db)
	s.apiKeyAuth = auth.NewApiKeyAuthenticator(s.apiKeyRepo)
	s.idempotencyRepo = repository.NewIdempotencyRepository(s.db)
	s.postRepo = repository.NewPostRepository(s.db)
	s.mediaRepo = repository.NewMediaAssetRepository(s.db)
	s.uploadJobRepo = repository.NewUploadJobRepository(s.db)
	s.importBatchRepo = repository.NewImportBatchRepository(s.db)
	s.connectionStateRepo = repository.NewConnectionStateRepository(s.db)
	s.auditLogRepo = repository.NewAuditLogRepository(s.db)
	s.externalDestinationRepo = repository.NewExternalDestinationRepository(s.db)
	s.externalDeliveryRepo = repository.NewExternalDeliveryRepository(s.db)
	s.connectLinkNonceRepo = repository.NewConnectLinkNonceRepository(s.db)
	s.youtubeVideoEditRepo = repository.NewYouTubeVideoEditRepository(s.db)
	s.youtubeThumbnailBatchRepo = repository.NewYouTubeThumbnailBatchRepository(s.db)
	s.livestreamRepo = repository.NewLivestreamRepository(s.db)
	s.thumbnailProjectRepo = repository.NewThumbnailProjectRepository(s.db)
	s.thumbnailProjectService = services.NewThumbnailProjectService(s.thumbnailProjectRepo)
	// Blocco Carosello — consolidated read-side fan-out for
	// GET /api/v1/content/{id}/pipeline (4 round-trips regardless of
	// target fan-out size). Wired via WithContentPipelineStore.
	s.contentPipelineRepo = repository.NewContentPipelineRepository(s.db)

	s.vault = credentials.NewCredentialVault(s.enc, s.db, s.tokenRepo)

	s.storageProvider, err = services.NewS3Provider(
		cfg.Storage.S3Endpoint, cfg.Storage.S3Bucket, cfg.Storage.S3Region,
		cfg.Storage.S3AccessKey, cfg.Storage.S3SecretKey, cfg.Storage.S3PathStyle, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("construct S3 provider: %w", err)
	}
	slog.Info("storage provider: S3-compatible configured",
		"endpoint", cfg.Storage.S3Endpoint, "bucket", cfg.Storage.S3Bucket, "region", cfg.Storage.S3Region)

	s.webhookRepo = repository.NewWebhookRepository(s.db)
	s.bookingEventRepo = repository.NewBookingEventRepository(s.db)

	return s, nil
}
