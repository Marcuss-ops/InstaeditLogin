package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const defaultFolderID = "1Kssuh0eQ7Wmg8uMg29aI7fShXSLCaw3x"

var postAphorisms = []string{
	"Every small step builds something great.",
	"Courage begins where comfort ends.",
	"Ideas become real when you take the first step.",
	"Consistency turns dreams into milestones.",
	"Choose to shine, even on difficult days.",
	"Your energy creates your direction.",
	"Make room for possibility.",
	"The right moment is the one you create.",
	"Simplicity is where greatness begins.",
	"Turn your vision into action.",
	"Every day is a new chance to begin again.",
	"Confidence grows when you keep promises to yourself.",
}

type driveTokenFile struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token"`
	TokenURI     string   `json:"token_uri"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("link_drive_and_import failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	userID, err := requiredEnvInt64("INSTAEDIT_USER_ID")
	if err != nil {
		return err
	}
	workspaceID, err := requiredEnvInt64("INSTAEDIT_WORKSPACE_ID")
	if err != nil {
		return err
	}
	facebookAccountID, err := requiredEnvInt64("FACEBOOK_PLATFORM_ACCOUNT_ID")
	if err != nil {
		return err
	}
	folderID := driveFolderID()
	if folderID == "" {
		return fmt.Errorf("DRIVE_FOLDER_ID or DRIVE_FOLDER_URL is required")
	}
	minHours, err := envFloat("DRIVE_SCHEDULE_MIN_HOURS", 4)
	if err != nil {
		return err
	}
	maxHours, err := envFloat("DRIVE_SCHEDULE_MAX_HOURS", 6)
	if err != nil {
		return err
	}
	if minHours < 0 || maxHours < minHours {
		return fmt.Errorf("invalid schedule range: min=%g max=%g", minHours, maxHours)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := openDB(cfg)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	encryptor, err := crypto.NewEncryptor(1, map[uint32]string{1: cfg.EncryptionKey})
	if err != nil {
		return fmt.Errorf("new encryptor: %w", err)
	}

	tokenRepo := repository.NewTokenRepository(db)
	vault := credentials.NewCredentialVault(encryptor, db, tokenRepo)
	userRepo := repository.NewUserRepository(db)

	driveSvc, err := services.NewGoogleDriveOAuthService(cfg)
	if err != nil {
		return fmt.Errorf("new google drive service: %w", err)
	}
	if driveSvc == nil {
		return fmt.Errorf("google drive service is disabled (check GOOGLE_DRIVE_CLIENT_ID)")
	}

	// Reuse the Google Drive OAuth account already linked through the app.
	driveAccounts, err := userRepo.ListPlatformAccountsByUser(userID, "google-drive")
	if err != nil {
		return fmt.Errorf("find linked drive account: %w", err)
	}
	if len(driveAccounts) == 0 {
		return fmt.Errorf("no linked google-drive account found for user %d; complete Google Drive login first", userID)
	}
	driveAccountID := driveAccounts[0].ID
	slog.Info("using linked google-drive platform account", "id", driveAccountID)

	// The refresh token is encrypted in the database vault; no token file is needed.
	refreshed, err := vault.Renew(ctx, driveAccountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return driveSvc.RefreshOAuthToken(ctx, refreshToken)
		})
	if err != nil {
		return fmt.Errorf("refresh linked drive token: %w", err)
	}

	// List the folder contents using the authenticated Drive grant (paginated).
	folderIDVar := folderID
	files, err := listAllFolderFiles(ctx, driveSvc, folderID, refreshed.AccessToken)
	if err != nil {
		return fmt.Errorf("list folder: %w", err)
	}
	slog.Info("listed drive folder", "folder_id", folderID, "videos", len(files))

	if len(files) == 0 {
		slog.Info("no videos found in folder")
		return nil
	}

	// Remove duplicate rows left by older scheduler runs before adding anything
	// new. Only redundant pending rows are failed; completed/processing history
	// remains intact and failed sources can still be retried.
	uploadRepo := repository.NewUploadJobRepository(db)
	suppressed, err := uploadRepo.SuppressPendingDuplicates(userID)
	if err != nil {
		return fmt.Errorf("suppress pending duplicate uploads: %w", err)
	}
	if suppressed > 0 {
		slog.Warn("suppressed duplicate pending upload jobs", "count", suppressed)
	}

	// Schedule only Drive files that have never been queued, processed, or
	// completed for this page. Gaps advance only when a new job is created, so
	// skipped duplicates do not push valid videos farther into the future.
	now := time.Now()
	cursor := now
	minSeconds := int(minHours * 60 * 60)
	maxSeconds := int(maxHours * 60 * 60)
	createdJobs := 0
	skippedDuplicates := 0

	for idx, f := range files {
		title := aphorismFor(idx)
		scheduledAt := cursor
		if createdJobs > 0 {
			gap, err := randomDuration(minSeconds, maxSeconds)
			if err != nil {
				return fmt.Errorf("random duration: %w", err)
			}
			scheduledAt = cursor.Add(gap)
		}

		job := &models.UploadJob{
			UserID:         userID,
			WorkspaceID:    workspaceID,
			SourceType:     models.UploadJobSourceAuthenticatedDrive,
			SourceID:       f.ID,
			DriveAccountID: &driveAccountID,
			FolderID:       &folderIDVar,
			Title:          title,
			Caption:        title,
			Targets:        []int64{facebookAccountID},
			Status:         models.UploadJobStatusPending,
			PublishAt:      &scheduledAt,
		}
		created, err := uploadRepo.CreateIfSourceAbsent(job)
		if err != nil {
			return fmt.Errorf("create upload job for %s: %w", f.Name, err)
		}
		if !created {
			skippedDuplicates++
			slog.Info("skipping already queued or uploaded drive video", "file", f.Name, "source_id", f.ID)
			continue
		}

		slog.Info("scheduled upload job", "job_id", job.ID, "file", f.Name, "publish_at", scheduledAt)
		cursor = scheduledAt
		createdJobs++
	}

	slog.Info(
		"done",
		"folder_videos", len(files),
		"new_jobs", createdJobs,
		"skipped_duplicates", skippedDuplicates,
		"suppressed_pending_duplicates", suppressed,
	)
	return nil
}

func aphorismFor(index int) string {
	if len(postAphorisms) == 0 {
		return ""
	}
	return postAphorisms[index%len(postAphorisms)]
}

func envInt64(name string, fallback int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return n, nil
}

func requiredEnvInt64(name string) (int64, error) {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return 0, fmt.Errorf("%s is required (use the platform_account id for the Caleb Foster Facebook Page)", name)
	}
	return envInt64(name, 0)
}

func envFloat(name string, fallback float64) (float64, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return n, nil
}

func driveFolderID() string {
	if id := strings.TrimSpace(os.Getenv("DRIVE_FOLDER_ID")); id != "" {
		return id
	}
	url := strings.TrimSpace(os.Getenv("DRIVE_FOLDER_URL"))
	if url == "" {
		return defaultFolderID
	}
	const marker = "/folders/"
	if i := strings.Index(url, marker); i >= 0 {
		id := strings.Split(strings.TrimSpace(url[i+len(marker):]), "?")[0]
		return strings.Trim(id, "/")
	}
	return url
}

func openDB(cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.Database.DSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Minute * 5)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func randomDuration(minSeconds, maxSeconds int) (time.Duration, error) {
	span := int64(maxSeconds - minSeconds)
	n, err := rand.Int(rand.Reader, big.NewInt(span+1))
	if err != nil {
		return 0, err
	}
	secs := int64(minSeconds) + n.Int64()
	return time.Duration(secs) * time.Second, nil
}

func fetchGoogleUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	// Drive tokens may not have the userinfo.profile scope; use the
	// Drive v3 about endpoint to get a stable user id and email.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/drive/v3/about?fields=user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("drive about failed (status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		User struct {
			PermissionID string `json:"permissionId"`
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &models.PlatformProfile{
		PlatformUserID: result.User.PermissionID,
		Username:       result.User.DisplayName,
		Name:           result.User.DisplayName,
		Email:          result.User.EmailAddress,
	}, nil
}

func listAllFolderFiles(ctx context.Context, svc *services.GoogleDriveOAuthService, folderID, accessToken string) ([]services.GoogleDriveFile, error) {
	// Task 6/10 — Shared Drive auto-resolve. Resolve the folder's
	// driveId ONCE before the pagination loop so the Shared Drive
	// scope is preserved across pages (a folder's driveId is stable
	// for its lifetime; per-page resolve would burn quota). On any
	// failure, log a warn-level remediation hint and fall back to
	// the empty driveId (pre-T6/10 My Drive corpus behaviour, full
	// back-compat for the operator CLI).
	driveID, err := services.ResolveFolderDriveID(ctx, svc, folderID, accessToken)
	if err != nil {
		slog.Warn("link-drive-and-import: folder metadata fetch failed; falling back to My Drive corpus",
			"folder_id", folderID,
			"error", err,
		)
		driveID = ""
	}
	var all []services.GoogleDriveFile
	pageToken := ""
	for {
		files, nextPageToken, err := svc.ListFolder(ctx, folderID, driveID, accessToken, pageToken)
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
		if nextPageToken == "" {
			break
		}
		pageToken = nextPageToken
	}
	return all, nil
}
