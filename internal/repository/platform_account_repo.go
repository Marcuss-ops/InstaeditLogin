// Package-level note: the platform_account repository methods are split
// per responsibility (split-by-concern, 2026-08):
//
//	platform_account_repo.go — this file: list queries
//	                           (ListFilteredYouTubeAccounts /
//	                           ListPlatformAccountsByUser) + shared
//	                           scan helpers (scanPlatformAccountRows /
//	                           coalesceStr / scanMetadata)
//	platform_account_attach.go — FinalizeAttach + AttachPlatformAccount
//	                           (OAuth attach / finalize flow)
//	platform_account_reauth.go — MarkReauthRequired +
//	                           MarkOAuthConnectionAccountsReauthRequired
//	platform_account_crud.go   — FindPlatformAccount / CreatePlatformAccount /
//	                           FindPlatformAccountByID / UpdatePlatformAccount /
//	                           DeletePlatformAccount
package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListFilteredYouTubeAccounts returns the YouTube platform accounts for a user,
// optionally filtered by workspace, group_name (from workspace_channels),
// and language/manager values stored in the account metadata JSONB.
//
// Empty strings for the string filters and a nil/zero workspaceID mean "no
// filter" for that dimension. The join against workspace_channels uses DISTINCT
// so an account attached to multiple workspaces is not duplicated.
func (r *UserRepository) ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error) {
	wsArg := int64(0)
	if workspaceID != nil {
		wsArg = *workspaceID
	}

	rows, err := r.db.Query(
		`SELECT DISTINCT pa.id, pa.user_id, pa.platform, pa.platform_user_id, pa.username, pa.status, pa.connected_at,
		        pa.last_validated_at, pa.last_refresh_at, pa.reauth_required_at,
		        COALESCE(pa.last_error_code, '') AS last_error_code,
		        COALESCE(pa.last_error_message, '') AS last_error_message,
		        pa.metadata, pa.created_at, pa.updated_at
		 FROM platform_accounts pa
		 LEFT JOIN workspace_channels wc ON wc.platform_account_id = pa.id
		 WHERE pa.user_id = $1
		   AND pa.platform = 'youtube'
		   AND ($2::bigint = 0 OR wc.workspace_id = $2)
		   AND (NULLIF($3::text, '') IS NULL OR wc.group_name = $3)
		   AND (NULLIF($4::text, '') IS NULL OR pa.metadata->>'language' = $4)
		   AND (NULLIF($5::text, '') IS NULL OR pa.metadata->>'manager' = $5)
		 ORDER BY pa.created_at DESC`,
		userID, wsArg, group, language, manager,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list filtered platform accounts: %w", err)
	}
	defer rows.Close()

	return scanPlatformAccountRows(rows)
}

// ListPlatformAccountsByUser returns all platform accounts for a user, optionally filtered by platform.
func (r *UserRepository) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	var rows *sql.Rows
	var err error

	if platform == "" {
		rows, err = r.db.Query(
			`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
			        last_validated_at, last_refresh_at, reauth_required_at,
			        COALESCE(last_error_code, '') AS last_error_code,
			        COALESCE(last_error_message, '') AS last_error_message,
			        metadata, created_at, updated_at
			 FROM platform_accounts WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	} else {
		rows, err = r.db.Query(
			`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
			        last_validated_at, last_refresh_at, reauth_required_at,
			        COALESCE(last_error_code, '') AS last_error_code,
			        COALESCE(last_error_message, '') AS last_error_message,
			        metadata, created_at, updated_at
			 FROM platform_accounts WHERE user_id = $1 AND platform = $2 ORDER BY created_at DESC`,
			userID, platform)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list platform accounts: %w", err)
	}
	defer rows.Close()

	return scanPlatformAccountRows(rows)
}

// scanPlatformAccountRows scans rows matching the standard SELECT column
// order used by the platform account list queries. Extracted so filtered
// and unfiltered queries share the same mapping logic.
func scanPlatformAccountRows(rows *sql.Rows) ([]*models.PlatformAccount, error) {
	var accounts []*models.PlatformAccount
	for rows.Next() {
		a := &models.PlatformAccount{}
		var metadata []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.Platform, &a.PlatformUserID, &a.Username, &a.Status, &a.ConnectedAt,
			&a.LastValidatedAt, &a.LastRefreshAt, &a.ReauthRequiredAt, &a.LastErrorCode,
			&a.LastErrorMessage, &metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan platform account: %w", err)
		}
		a.Metadata = scanMetadata(metadata)
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// coalesceStr returns the first non-empty string.
func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// scanMetadata unmarshals a JSONB byte slice into a Metadata map.
func scanMetadata(data []byte) models.Metadata {
	if len(data) == 0 {
		return models.Metadata{}
	}
	var m models.Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return models.Metadata{}
	}
	return m
}
