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
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, provider, provider_subject_id, provider_resource_id,
		        status, COALESCE(NULLIF(granted_scopes, '{}'::TEXT[]), scopes), last_refresh_at,
		        last_refresh_error, created_at, updated_at
		 FROM oauth_connections
		 WHERE id = $1`, id,
	).Scan(
		&grant.ID, &grant.UserID, &grant.Provider, &grant.ProviderSubjectID,
		&grant.ProviderResourceID, &grant.Status, pq.Array(&grant.GrantedScopes),
		&grant.LastRefreshAt, &grant.LastRefreshError, &grant.CreatedAt,
		&grant.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find OAuth connection %d: %w", id, err)
	}
	return grant, nil
}
