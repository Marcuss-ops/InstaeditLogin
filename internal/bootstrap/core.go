package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/providers"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Core holds the dependencies shared by the API and worker processes.
// It is constructed by WireCore and consumed by WireAPI and WireWorkers.
type Core struct {
	Cfg           *config.Config
	DB            *sql.DB
	Logger        *slog.Logger
	Vault         credentials.VaultAPI
	CapRouter     *services.CapabilityRouter
	Storage       services.StorageProvider
	Encryptor     *crypto.Encryptor
	MemoryLimiter *services.MemoryLimiter
	WorkerID      string
	WebhookRepo   *repository.WebhookRepository
	OneTimeCodes  api.OneTimeCodeStore

	authMgr     *auth.Manager
	sessionsSvc *services.SessionsService

	// Repositories shared between API and worker paths.
	userRepo                *repository.UserRepository
	tokenRepo               *repository.TokenRepository
	teamRepo                *repository.TeamRepository
	workspaceRepo           *repository.WorkspaceRepository
	apiKeyRepo              *repository.ApiKeyRepository
	idempotencyRepo         *repository.IdempotencyRepository
	postRepo                *repository.PostRepository
	mediaRepo               *repository.MediaAssetRepository
	uploadJobRepo           *repository.UploadJobRepository
	importBatchRepo         *repository.ImportBatchRepository
	connectionStateRepo     *repository.ConnectionStateRepository
	auditLogRepo            *repository.AuditLogRepository
	externalDestinationRepo *repository.ExternalDestinationRepository
	externalDeliveryRepo    *repository.ExternalDeliveryRepository
	connectLinkNonceRepo    *repository.ConnectLinkNonceRepository
}

// WireCore builds the shared runtime dependencies used by every binary.
// It does not construct the HTTP router or the worker registry, so the
// API process does not pay for worker wiring and vice versa.
func WireCore(ctx context.Context) (*Core, error) {
	_ = ctx // reserved for future context-aware config loading

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		return nil, fmt.Errorf("S3 storage is required: set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Environment", "app_env", cfg.AppEnv)

	db, err := database.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	workerID := metrics.NewWorkerID()
	slog.Info("worker_id initialised", "worker_id", workerID)

	memoryLimiter := services.NewMemoryLimiter()

	enc, err := crypto.NewEncryptor(cfg.ActiveEncryptionKeyID, cfg.EncryptionKeys)
	if err != nil {
		return nil, fmt.Errorf("init encryptor: %w", err)
	}
	slog.Info("encryption configured",
		"active_key_id", cfg.ActiveEncryptionKeyID,
		"key_count", len(cfg.EncryptionKeys),
		"key_ids", config.SortedKeyIDs(cfg.EncryptionKeys))

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	workspaceRepo := repository.NewWorkspaceRepository(db)
	apiKeyRepo := repository.NewApiKeyRepository(db)
	idempotencyRepo := repository.NewIdempotencyRepository(db)
	postRepo := repository.NewPostRepository(db)
	mediaRepo := repository.NewMediaAssetRepository(db)
	uploadJobRepo := repository.NewUploadJobRepository(db)
	importBatchRepo := repository.NewImportBatchRepository(db)
	connectionStateRepo := repository.NewConnectionStateRepository(db)
	auditLogRepo := repository.NewAuditLogRepository(db)
	externalDestinationRepo := repository.NewExternalDestinationRepository(db)
	externalDeliveryRepo := repository.NewExternalDeliveryRepository(db)
	connectLinkNonceRepo := repository.NewConnectLinkNonceRepository(db)

	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	registry, err := providers.BuildRegistry(cfg)
	if err != nil {
		return nil, fmt.Errorf("build provider registry: %w", err)
	}
	capRouter := registry

	authMgr := auth.NewManager(
		cfg.JWTSecret,
		time.Duration(cfg.JWTAccessTTLMinutes)*time.Minute,
		time.Duration(cfg.JWTRefreshTTLDays)*24*time.Hour,
	).WithEnv(cfg.AppEnv)

	oneTimeCodes := api.NewOneTimeCodePostgresStore(db, 60*time.Second)

	sessionRepo := repository.NewSessionRepository(db)
	sessionsSvc := services.NewSessionsService(sessionRepo, authMgr)

	storageProvider, err := services.NewS3Provider(
		cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
		cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3PathStyle, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("construct S3 provider: %w", err)
	}
	slog.Info("storage provider: S3-compatible configured",
		"endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket, "region", cfg.S3Region)

	return &Core{
		Cfg:                     cfg,
		DB:                      db,
		Logger:                  logger,
		Vault:                   vault,
		CapRouter:               capRouter,
		Storage:                 storageProvider,
		Encryptor:               enc,
		MemoryLimiter:           memoryLimiter,
		WorkerID:                workerID,
		WebhookRepo:             repository.NewWebhookRepository(db),
		OneTimeCodes:            oneTimeCodes,
		authMgr:                 authMgr,
		sessionsSvc:             sessionsSvc,
		userRepo:                userRepo,
		tokenRepo:               tokenRepo,
		teamRepo:                teamRepo,
		workspaceRepo:           workspaceRepo,
		apiKeyRepo:              apiKeyRepo,
		idempotencyRepo:         idempotencyRepo,
		postRepo:                postRepo,
		mediaRepo:               mediaRepo,
		uploadJobRepo:           uploadJobRepo,
		importBatchRepo:         importBatchRepo,
		connectionStateRepo:     connectionStateRepo,
		auditLogRepo:            auditLogRepo,
		externalDestinationRepo: externalDestinationRepo,
		externalDeliveryRepo:    externalDeliveryRepo,
		connectLinkNonceRepo:    connectLinkNonceRepo,
	}, nil
}
