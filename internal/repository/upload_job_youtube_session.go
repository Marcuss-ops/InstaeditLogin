package repository

import (
	"context"
	"fmt"
	"time"
)

// SaveYouTubeSession persists resumable-upload progress under the same lease
// CAS as the upload worker. The session URI is already encrypted by the
// worker/service boundary before it reaches this repository.
func (r *UploadJobRepository) SaveYouTubeSession(
	ctx context.Context,
	id int64,
	workerID, sessionURI string,
	offset, chunkSize int64,
	expiresAt time.Time,
) error {
	if workerID == "" || sessionURI == "" || offset < 0 || chunkSize <= 0 || expiresAt.IsZero() {
		return fmt.Errorf("upload job SaveYouTubeSession: invalid session arguments")
	}
	res, err := r.db.ExecContext(ctx, SQLSaveYouTubeSession,
		id, sessionURI, offset, expiresAt, chunkSize, workerID,
	)
	if err != nil {
		return fmt.Errorf("upload job SaveYouTubeSession: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upload job SaveYouTubeSession rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

// ClearYouTubeSession removes resumable state after terminal success or when
// YouTube reports that the session expired. It is lease-CAS protected so a
// stale worker cannot clear a newer worker's session.
func (r *UploadJobRepository) ClearYouTubeSession(ctx context.Context, id int64, workerID string) error {
	if workerID == "" {
		return fmt.Errorf("upload job ClearYouTubeSession: empty worker ID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET youtube_session_uri = NULL,
             youtube_session_offset = NULL,
             youtube_session_expires_at = NULL,
             youtube_chunk_size = NULL,
             youtube_last_chunk_at = NULL,
             updated_at = NOW()
         WHERE id = $1
           AND lease_owner = $2
           AND status = 'leased'`,
		id, workerID,
	)
	if err != nil {
		return fmt.Errorf("upload job ClearYouTubeSession: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upload job ClearYouTubeSession rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}
