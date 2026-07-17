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
	"time"

	_ "github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const (
	userID   = 3
	folderID = "1HregS58okcSoe8597qdXgpZM6K4CwEBD"
)

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

	filePath := "/home/pierone/freebuff_agents/profilo2/velox_cleanup_backup_root/VeloxLEgit_backup_20260711_121452/DataServer/.velox/secrets/drive/tokens/account_manual.json"
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read token file: %w", err)
	}
	var tf driveTokenFile
	if err := json.Unmarshal(fileData, &tf); err != nil {
		return fmt.Errorf("parse token file: %w", err)
	}

	driveSvc, err := services.NewGoogleDriveOAuthService(cfg)
	if err != nil {
		return fmt.Errorf("new google drive service: %w", err)
	}
	if driveSvc == nil {
		return fmt.Errorf("google drive service is disabled (check GOOGLE_DRIVE_CLIENT_ID)")
	}

	// Refresh the access token so we can fetch user info and use it for listing.
	refreshed, err := driveSvc.RefreshOAuthToken(ctx, tf.RefreshToken)
	if err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}

	profile, err := fetchGoogleUserInfo(ctx, refreshed.AccessToken)
	if err != nil {
		return fmt.Errorf("get user info: %w", err)
	}

	// Create or reuse the google-drive platform account.
	existing, err := userRepo.FindPlatformAccount("google-drive", profile.PlatformUserID)
	if err != nil {
		return fmt.Errorf("find existing drive account: %w", err)
	}
	var driveAccountID int64
	if existing != nil {
		driveAccountID = existing.ID
		slog.Info("reusing existing google-drive platform account", "id", driveAccountID)
	} else {
		account := &models.PlatformAccount{
			UserID:         userID,
			Platform:       "google-drive",
			PlatformUserID: profile.PlatformUserID,
			Username:       profile.Username,
		}
		if err := userRepo.CreatePlatformAccount(account); err != nil {
			return fmt.Errorf("create platform account: %w", err)
		}
		driveAccountID = account.ID
		slog.Info("created google-drive platform account", "id", driveAccountID)
	}

	// Persist the encrypted token (access + refresh) in the vault.
	tokenData := &models.TokenData{
		AccessToken:  refreshed.AccessToken,
		RefreshToken: refreshed.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    refreshed.ExpiresIn,
		Scopes:       refreshed.Scopes,
	}
	if err := vault.Save(ctx, driveAccountID, tokenData); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	slog.Info("saved encrypted drive token", "account_id", driveAccountID)

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

	// Schedule upload jobs with a random 3-4.5h gap.
	uploadRepo := repository.NewUploadJobRepository(db)
	workspaceID := int64(3) // user 3's personal workspace
	facebookAccountID := int64(2)

	now := time.Now()
	cursor := now
	minSeconds := 3 * 60 * 60
	maxSeconds := int(4.5 * 60 * 60)

	for idx, f := range files {
		scheduledAt := cursor
		if idx > 0 {
			gap, err := randomDuration(minSeconds, maxSeconds)
			if err != nil {
				return fmt.Errorf("random duration: %w", err)
			}
			scheduledAt = cursor.Add(gap)
		}

		job := &models.UploadJob{
			UserID:         userID,
			WorkspaceID:    workspaceID,
			SourceType:     models.UploadJobSourcePublicDrive,
			SourceID:       f.ID,
			DriveAccountID: &driveAccountID,
			FolderID:       &folderIDVar,
			Title:          f.Name,
			Caption:        f.Name,
			Targets:        []int64{facebookAccountID},
			Status:         models.UploadJobStatusPending,
			ScheduledAt:    &scheduledAt,
		}
		if err := uploadRepo.Create(job); err != nil {
			return fmt.Errorf("create upload job for %s: %w", f.Name, err)
		}
		slog.Info("scheduled upload job", "job_id", job.ID, "file", f.Name, "scheduled_at", scheduledAt)
		cursor = scheduledAt
	}

	slog.Info("done", "total_jobs", len(files))
	return nil
}

func openDB(cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DSN())
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
	var all []services.GoogleDriveFile
	pageToken := ""
	for {
		files, nextPageToken, err := svc.ListFolder(ctx, folderID, accessToken, pageToken)
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
