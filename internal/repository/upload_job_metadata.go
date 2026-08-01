package repository

import (
	"context"
	"fmt"
	"time"
)

// SaveYouTubeSession persists the resumable upload session for a leased
// upload job. The session URI, byte offset, chunk size and token expiry
// are stamped so a crashed worker can resume the upload. The update is
// CAS-guarded by lease_owner and status='leased' so a recovered row
// cannot be overwritten by a stale worker.
func (r *UploadJobRepository) SaveYouTubeSession(ctx context.Context, id int64, workerID, sessionURI string, offset, chunkSize int64, expiresAt time.Time) error {
	if workerID == "" {
		return fmt.Errorf("upload job SaveYouTubeSession: empty workerID")
	}
	if sessionURI == "" {
		return fmt.Errorf("upload job SaveYouTubeSession: empty sessionURI")
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

// ClearYouTubeSession wipes the resumable upload session for a leased
// upload job. Called after a successful publish or when the session
// token expires and must be discarded. Like SaveYouTubeSession, the
// operation is CAS-guarded by lease_owner and status='leased'.
func (r *UploadJobRepository) ClearYouTubeSession(ctx context.Context, id int64, workerID string) error {
	if workerID == "" {
		return fmt.Errorf("upload job ClearYouTubeSession: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
		 SET youtube_session_uri       = NULL,
		     youtube_session_offset    = NULL,
		     youtube_session_expires_at  = NULL,
		     youtube_chunk_size        = NULL,
		     youtube_last_chunk_at     = NULL,
		     updated_at                = NOW()
		 WHERE id = $1
		   AND lease_owner = $2
		   AND status      = 'leased'`,
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
