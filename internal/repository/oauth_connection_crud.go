package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// FindOAuthConnectionByID returns a grant by its canonical lineage id.
// Missing rows use the repository convention (nil, nil). The method is
// intentionally read-only; token renewal and persistence remain owned by
// credentials.CredentialVault.
func (r *UserRepository) FindOAuthConnectionByID(ctx context.Context, id int64) (*models.OAuthConnection, error) {
	if id <= 0 {
		return nil, fmt.Errorf("find OAuth connection: invalid id %d", id)
	}
	grant := &models.OAuthConnection{}
	// last_refresh_error is NULL until the first failed renewal; scan it
	// into sql.NullString so a NULL row maps to "" instead of a Scan
	// error (the model keeps a plain string on the wire).
	var lastRefreshError sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, provider, provider_subject_id, provider_resource_id,
		        oauth_client_key,
		        status, COALESCE(NULLIF(granted_scopes, '{}'::TEXT[]), scopes), last_refresh_at,
		        last_refresh_error, created_at, updated_at
		 FROM oauth_connections
		 WHERE id = $1`, id,
	).Scan(
		&grant.ID, &grant.UserID, &grant.Provider, &grant.ProviderSubjectID,
		&grant.ProviderResourceID, &grant.OAuthClientKey, &grant.Status, pq.Array(&grant.GrantedScopes),
		&grant.LastRefreshAt, &lastRefreshError, &grant.CreatedAt,
		&grant.UpdatedAt,
	)
	grant.LastRefreshError = lastRefreshError.String
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find OAuth connection %d: %w", id, err)
	}
	grant.Provider = models.NormalizePlatformIdentifier(grant.Provider)
	return grant, nil
}
