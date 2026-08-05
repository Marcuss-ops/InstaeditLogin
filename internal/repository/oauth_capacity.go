package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// OAuthPoolUsageRow is one (provider_subject_id, oauth_client_key)
// bucket of active YouTube refresh grants — the exact count Google's
// 100-refresh-token-per-(Google account, OAuth client) cap is measured
// against.
type OAuthPoolUsageRow struct {
	ProviderSubjectID   string
	OAuthClientKey      string
	ActiveRefreshTokens int64
}

// OAuthTokenCapacityRepository counts active YouTube OAuth refresh
// grants per (Google subject, pool client). An "active" grant is an
// oauth_connections row with status='active' that owns a bearer token
// row carrying a non-empty encrypted_refresh_token — the same
// definition used by the youtube_oauth_refresh_tokens_active pool
// metric (pkg/metrics/collector.go). A legacy row whose
// oauth_client_key is ” is counted under 'youtube_pool_a', the honest
// label for the historical single-client path.
//
// This is the counting half of the YouTube OAuth Client Pool capacity
// manager (services.OAuthTokenCapacityManager): SelectPool picks the
// least-loaded client, GetUsage reports per-client headroom. Secrets
// never pass through here — only subject IDs and client keys.
type OAuthTokenCapacityRepository struct {
	db *sql.DB
}

// NewOAuthTokenCapacityRepository creates a new capacity repository.
func NewOAuthTokenCapacityRepository(db *sql.DB) *OAuthTokenCapacityRepository {
	return &OAuthTokenCapacityRepository{db: db}
}

const countActiveRefreshTokensSQL = `SELECT COUNT(DISTINCT oc.id)
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id = $1
   AND oc.oauth_client_key = $2
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0`

// CountActiveRefreshTokens returns the number of active refresh grants
// for one Google subject on one pool client. Both the subject and the
// client key are required — an empty subject (or key) is rejected
// fail-closed instead of silently counting the ” bucket.
func (r *OAuthTokenCapacityRepository) CountActiveRefreshTokens(ctx context.Context, providerSubjectID, oauthClientKey string) (int64, error) {
	if providerSubjectID == "" {
		return 0, fmt.Errorf("oauth token capacity: providerSubjectID is required")
	}
	if oauthClientKey == "" {
		return 0, fmt.Errorf("oauth token capacity: oauthClientKey is required")
	}
	var count int64
	if err := r.db.QueryRowContext(ctx, countActiveRefreshTokensSQL, providerSubjectID, oauthClientKey).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active refresh tokens: %w", err)
	}
	return count, nil
}

const listPoolUsageSQL = `SELECT oc.provider_subject_id,
       oc.oauth_client_key,
       COUNT(DISTINCT oc.id) AS active_refresh_grants
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id = $1
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0
 GROUP BY oc.provider_subject_id, oc.oauth_client_key`

// ListPoolUsage returns the active refresh-grant count per pool client
// for one Google subject. Clients with zero active grants are absent
// from the result — the capacity manager zero-fills them from the
// registry so every configured client appears in GetUsage.
func (r *OAuthTokenCapacityRepository) ListPoolUsage(ctx context.Context, providerSubjectID string) ([]OAuthPoolUsageRow, error) {
	if providerSubjectID == "" {
		return nil, fmt.Errorf("oauth token capacity: providerSubjectID is required")
	}
	rows, err := r.db.QueryContext(ctx, listPoolUsageSQL, providerSubjectID)
	if err != nil {
		return nil, fmt.Errorf("list pool usage: %w", err)
	}
	defer rows.Close()
	var out []OAuthPoolUsageRow
	for rows.Next() {
		var row OAuthPoolUsageRow
		if err := rows.Scan(&row.ProviderSubjectID, &row.OAuthClientKey, &row.ActiveRefreshTokens); err != nil {
			return nil, fmt.Errorf("list pool usage scan: %w", err)
		}
		if row.OAuthClientKey == "" {
			// Legacy single-client rows default to youtube_pool_a
			// (mirrors the pool-metrics collector).
			row.OAuthClientKey = "youtube_pool_a"
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pool usage rows: %w", err)
	}
	return out, nil
}
