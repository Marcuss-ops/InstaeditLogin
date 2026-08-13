package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UpdateScheduledContent changes the mutable draft of a pending upload job.
// It is scoped by user and status so a late edit cannot mutate a job already
// claimed by preparation. The worker reads these columns when it materialises
// the post, giving edits made before the preparation window last-write-wins
// semantics.
func (r *UploadJobRepository) UpdateScheduledContent(
	ctx context.Context,
	jobID, userID int64,
	title, caption *string,
	metadata json.RawMessage,
	metadataSet bool,
) (models.UploadJob, error) {
	if jobID <= 0 || userID <= 0 {
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET title      = COALESCE($3, title),
             caption    = COALESCE($4, caption),
             metadata   = CASE WHEN $5 THEN $6 ELSE metadata END,
             updated_at = NOW()
         WHERE id = $1
           AND user_id = $2
           AND status = 'pending'`,
		jobID, userID, title, caption, metadataSet, metadata,
	)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to update scheduled upload content: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to read scheduled content rows affected: %w", err)
	}
	if n == 0 {
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	job, err := r.FindByID(jobID)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to reread scheduled upload content: %w", err)
	}
	if job == nil || job.UserID != userID {
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	return *job, nil
}
