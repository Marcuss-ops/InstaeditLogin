package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// FindPlatformAccount finds a platform account by platform and platform user ID.
func (r *UserRepository) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	platform = models.NormalizePlatformIdentifier(platform)
	account := &models.PlatformAccount{}
	var metadata []byte
	var storedPlatform string
	err := r.db.QueryRow(
		`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
		        last_validated_at, last_refresh_at, reauth_required_at,
		        COALESCE(last_error_code, '') AS last_error_code,
		        COALESCE(last_error_message, '') AS last_error_message,
		        metadata, created_at, updated_at
		 FROM platform_accounts
		 WHERE (platform = $1 OR (platform = 'x' AND $1 = 'twitter'))
		   AND platform_user_id = $2
		 ORDER BY CASE WHEN platform = $1 THEN 0 ELSE 1 END, id ASC
		 LIMIT 1`,
		platform, platformUserID,
	).Scan(&account.ID, &account.UserID, &storedPlatform, &account.PlatformUserID,
		&account.Username, &account.Status, &account.ConnectedAt, &account.LastValidatedAt,
		&account.LastRefreshAt, &account.ReauthRequiredAt, &account.LastErrorCode,
		&account.LastErrorMessage, &metadata, &account.CreatedAt, &account.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find platform account: %w", err)
	}
	account.Metadata = scanMetadata(metadata)
	account.Platform = models.NormalizePlatformIdentifier(storedPlatform)
	return account, nil
}

// CreatePlatformAccount inserts a new platform account.
func (r *UserRepository) CreatePlatformAccount(account *models.PlatformAccount) error {
	if account == nil {
		return fmt.Errorf("failed to create platform account: nil account")
	}
	account.Platform = models.NormalizePlatformIdentifier(account.Platform)
	if account.Status == "" {
		account.Status = models.AccountStatusActive
	}
	now := time.Now()
	account.ConnectedAt = &now
	err := r.db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, status, connected_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at, updated_at`,
		account.UserID, account.Platform, account.PlatformUserID, account.Username, account.Status, account.ConnectedAt,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create platform account: %w", err)
	}
	return nil
}

// FindPlatformAccountByID fetches a platform account by internal id, or
// (nil, nil) when no row matches. (nil, nil) matches the rest of the
// repository layer's not-found convention so callers can write
//
//	if pa == nil { /* skip — row vanished */ }
//
// without inspecting sql.ErrNoRows.
//
// Used by background workers (publish worker) that need to look up an
// account knowing only its id, typically from a post_targets join row.
//
// The row's oauth_connection_id (the OAuth grant lineage, migrations
// 084/085) is populated too — the shared-grant disconnect flow
// (pkg/api handleDeleteAccount) gates its grant revocation on it.
// Legacy rows attached before migration 043 (or with a revoked grant)
// carry NULL and surface as OAuthConnectionID == nil.
func (r *UserRepository) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	account := &models.PlatformAccount{}
	var metadata []byte
	var oauthConnectionID int64
	err := r.db.QueryRow(
		`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
		        last_validated_at, last_refresh_at, reauth_required_at,
		        COALESCE(last_error_code, '') AS last_error_code,
		        COALESCE(last_error_message, '') AS last_error_message,
		        metadata, created_at, updated_at,
		        COALESCE(oauth_connection_id, 0) AS oauth_connection_id
		 FROM platform_accounts
		 WHERE id = $1`, id,
	).Scan(&account.ID, &account.UserID, &account.Platform, &account.PlatformUserID,
		&account.Username, &account.Status, &account.ConnectedAt, &account.LastValidatedAt,
		&account.LastRefreshAt, &account.ReauthRequiredAt, &account.LastErrorCode,
		&account.LastErrorMessage, &metadata, &account.CreatedAt, &account.UpdatedAt,
		&oauthConnectionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find platform account by id: %w", err)
	}
	account.Metadata = scanMetadata(metadata)
	if oauthConnectionID != 0 {
		account.OAuthConnectionID = &oauthConnectionID
	}
	account.Platform = models.NormalizePlatformIdentifier(account.Platform)
	return account, nil
}

// UpdatePlatformAccount persists lifecycle changes to a platform account.
func (r *UserRepository) UpdatePlatformAccount(account *models.PlatformAccount) error {
	metadataJSON, err := json.Marshal(account.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	result, err := r.db.Exec(
		`UPDATE platform_accounts
		 SET status = $1, connected_at = $2, last_validated_at = $3, last_refresh_at = $4,
		     reauth_required_at = $5, last_error_code = $6, last_error_message = $7,
		     metadata = $8, updated_at = $9
		 WHERE id = $10`,
		account.Status, account.ConnectedAt, account.LastValidatedAt, account.LastRefreshAt,
		account.ReauthRequiredAt, account.LastErrorCode, account.LastErrorMessage,
		metadataJSON, time.Now(), account.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update platform account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, account.ID)
	}
	return nil
}

// DeletePlatformAccount removes a platform account and its tokens (cascading).
func (r *UserRepository) DeletePlatformAccount(id int64) error {
	result, err := r.db.Exec(`DELETE FROM platform_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete platform account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, id)
	}
	return nil
}
