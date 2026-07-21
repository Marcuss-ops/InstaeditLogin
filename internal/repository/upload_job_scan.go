package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const (
	// Task 10.10.x polish #2 — const-export production SQL. Each SQL
	// statement referenced by the test file (internal/worker/task_10_10_recovery_test.go)
	// is declared here as an EXPORTED Go constant so the test's sqlmock
	// expectations are pinned to the production SQL byte-for-byte via
	// import. A change to any constant fires a compile error in the test
	// (the variable name moves + the regex match fails simultaneously)
	// so the drift is caught at PR review, not in production.
	//
	// Naming convention: SQL<Method> matches the user's spec example
	// (const SQLReclaimExpiredLeases = "..."). Layout follows the
	// method order in this file so the block reads top-to-bottom in
	// approximate source order. Inline SQL literals elsewhere in this
	// file are still inline; extracting EXPORTED constants for the
	// five methods whose SQL is duplicated in the test file (5/19)
	// is the minimum-viable scope. A future commit can sweep the
	// remaining 14 methods if drift detection is desired for them
	// too.
	SQLReclaimExpiredLeases = `WITH expired AS (
            SELECT id
            FROM upload_jobs
            WHERE status          = 'leased'
              AND lease_expires_at < NOW()
              AND heartbeat_at    IS NOT NULL
              AND heartbeat_at    < NOW() - INTERVAL '5 minutes'
            ORDER BY lease_expires_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE upload_jobs j
        SET status                    = 'pending',
            lease_owner               = NULL,
            lease_expires_at          = NULL,
            heartbeat_at              = NULL,
            error_code                = COALESCE(error_code, 'lease_expired'),
            youtube_session_uri       = NULL,
            youtube_session_offset    = NULL,
            youtube_session_expires_at = NULL,
            youtube_chunk_size        = NULL,
            youtube_last_chunk_at     = NULL,
            updated_at                = NOW()
        FROM expired
        WHERE j.id = expired.id`

	SQLMarkDeadLetter = `UPDATE upload_jobs
         SET status           = 'dead_letter',
             error_message    = $2,
             error_code       = $3,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             completed_at     = NOW(),
             updated_at       = NOW()
         WHERE id = $1
           AND lease_owner   = $4
           AND status        = 'leased'`

	SQLSaveYouTubeSession = `UPDATE upload_jobs
         SET youtube_session_uri       = $2,
             youtube_session_offset    = $3,
             youtube_session_expires_at = $4,
             youtube_chunk_size        = $5,
             youtube_last_chunk_at     = NOW(),
             progress_bytes            = $3,
             updated_at                = NOW()
         WHERE id = $1
           AND lease_owner            = $6
           AND status                 = 'leased'`

	SQLClaimBatchForPublish = `WITH candidates AS (
            SELECT id
            FROM upload_jobs
            WHERE status = 'ingest_completed'
              AND (publish_at IS NULL OR publish_at <= NOW())
              AND COALESCE(next_attempt_at, NOW()) <= NOW()
              AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
            ORDER BY priority ASC, created_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE upload_jobs j
        SET status           = 'leased',
            lease_owner      = $2,
            lease_expires_at = $3,
            heartbeat_at     = NOW(),
            attempt_count    = attempt_count + 1,
            started_at       = COALESCE(started_at, NOW()),
            updated_at       = NOW()
        FROM candidates
        WHERE j.id = candidates.id
        RETURNING j.id, j.user_id, j.workspace_id, j.source_type, j.source_id, j.drive_account_id, j.folder_id, j.title, j.caption,
                  j.targets, j.status, j.error_message, j.post_id, j.asset_id, j.ingest_after, j.publish_at, j.created_at, j.updated_at,
                  j.attempt_count, j.max_attempts, j.next_attempt_at, j.lease_owner, j.lease_expires_at, j.heartbeat_at,
                  j.progress_bytes, j.total_bytes, j.error_code, j.priority, j.started_at, j.completed_at,
                  j.youtube_session_uri, j.youtube_session_offset, j.youtube_session_expires_at, j.youtube_chunk_size, j.youtube_last_chunk_at,
                  j.default_privacy_level`
)

// scanUploadJobRows is the *sql.Rows equivalent of scanUploadJob —
// the column list is identical but we iterate in the caller. Splitting
// them keeps the row-vs-rows contract distinct at the type level so
// adding a column doesn't have to thread through two function bodies.
//
// The 29 column list must stay in lockstep with the SELECT projections
// in FindByID / ListByUser / ClaimBatch; a mismatch causes sql.Scan to
// return an error on the first row. Columns past index 16 are the P1
// worker-pool additions (migration 046) and use sql.Null* wrappers
// because they're nullable on disk.
func scanUploadJobRows(rows *sql.Rows) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var publishAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString
	var nextAttemptAt, leaseExpiresAt, heartbeatAt, startedAt, completedAt sql.NullTime
	var leaseOwner sql.NullString
	var totalBytes sql.NullInt64
	var errorCode sql.NullString
	// P1#5 — YouTube resumable session columns (migration 048). All
	// nullable: NULL means "no session yet" or "session was cleared".
	var youtubeSessionURI sql.NullString
	var youtubeSessionOffset, youtubeChunkSize sql.NullInt64
	var youtubeSessionExpiresAt, youtubeLastChunkAt sql.NullTime

	err := rows.Scan(
		&job.ID,
		&job.UserID,
		&job.WorkspaceID,
		&rawSource,
		&job.SourceID,
		&driveAccountID,
		&folderID,
		&job.Title,
		&job.Caption,
		&targetsJSON,
		&rawStatus,
		&errorMessage,
		&job.PostID,
		&assetID,
		&job.IngestAfter,
		&publishAt,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextAttemptAt,
		&leaseOwner,
		&leaseExpiresAt,
		&heartbeatAt,
		&job.ProgressBytes,
		&totalBytes,
		&errorCode,
		&job.Priority,
		&startedAt,
		&completedAt,
		&youtubeSessionURI,
		&youtubeSessionOffset,
		&youtubeSessionExpiresAt,
		&youtubeChunkSize,
		&youtubeLastChunkAt,
		&job.DefaultPrivacyLevel,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan upload job: %w", err)
	}

	job.SourceType = models.UploadJobSource(rawSource)
	job.Status = models.UploadJobStatus(rawStatus)
	if driveAccountID.Valid {
		v := driveAccountID.Int64
		job.DriveAccountID = &v
	}
	if folderID.Valid {
		v := folderID.String
		job.FolderID = &v
	}
	if publishAt.Valid {
		t := publishAt.Time
		job.PublishAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time
		job.NextAttemptAt = &t
	}
	if leaseOwner.Valid {
		v := leaseOwner.String
		job.LeaseOwner = &v
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		job.LeaseExpiresAt = &t
	}
	if heartbeatAt.Valid {
		t := heartbeatAt.Time
		job.HeartbeatAt = &t
	}
	if totalBytes.Valid {
		v := totalBytes.Int64
		job.TotalBytes = &v
	}
	if errorCode.Valid {
		v := errorCode.String
		job.ErrorCode = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		job.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		job.CompletedAt = &t
	}
	// P1#5 — YouTube session field assignments. youtube_session_uri
	// is the only credential-adjacent field; it carries the
	// `json:"-"` tag on the model so it never leaves the backend
	// via any API response. Workers MUST redact it on log lines
	// (see internal/worker/redactYouTubeSessionURI).
	if youtubeSessionURI.Valid {
		v := youtubeSessionURI.String
		job.YouTubeSessionURI = &v
	}
	if youtubeSessionOffset.Valid {
		v := youtubeSessionOffset.Int64
		job.YouTubeSessionOffset = &v
	}
	if youtubeSessionExpiresAt.Valid {
		t := youtubeSessionExpiresAt.Time
		job.YouTubeSessionExpiresAt = &t
	}
	if youtubeChunkSize.Valid {
		v := youtubeChunkSize.Int64
		job.YouTubeChunkSize = &v
	}
	if youtubeLastChunkAt.Valid {
		t := youtubeLastChunkAt.Time
		job.YouTubeLastChunkAt = &t
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}

func scanUploadJob(row *sql.Row) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var publishAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString
	var nextAttemptAt, leaseExpiresAt, heartbeatAt, startedAt, completedAt sql.NullTime
	var leaseOwner sql.NullString
	var totalBytes sql.NullInt64
	var errorCode sql.NullString
	// P1#5 — YouTube resumable session columns (migration 048). All
	// nullable: NULL means "no session yet" or "session was cleared".
	var youtubeSessionURI sql.NullString
	var youtubeSessionOffset, youtubeChunkSize sql.NullInt64
	var youtubeSessionExpiresAt, youtubeLastChunkAt sql.NullTime

	err := row.Scan(
		&job.ID,
		&job.UserID,
		&job.WorkspaceID,
		&rawSource,
		&job.SourceID,
		&driveAccountID,
		&folderID,
		&job.Title,
		&job.Caption,
		&targetsJSON,
		&rawStatus,
		&errorMessage,
		&job.PostID,
		&assetID,
		&job.IngestAfter,
		&publishAt,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextAttemptAt,
		&leaseOwner,
		&leaseExpiresAt,
		&heartbeatAt,
		&job.ProgressBytes,
		&totalBytes,
		&errorCode,
		&job.Priority,
		&startedAt,
		&completedAt,
		&youtubeSessionURI,
		&youtubeSessionOffset,
		&youtubeSessionExpiresAt,
		&youtubeChunkSize,
		&youtubeLastChunkAt,
		&job.DefaultPrivacyLevel,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan upload job: %w", err)
	}

	job.SourceType = models.UploadJobSource(rawSource)
	job.Status = models.UploadJobStatus(rawStatus)
	if driveAccountID.Valid {
		v := driveAccountID.Int64
		job.DriveAccountID = &v
	}
	if folderID.Valid {
		v := folderID.String
		job.FolderID = &v
	}
	if publishAt.Valid {
		t := publishAt.Time
		job.PublishAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time
		job.NextAttemptAt = &t
	}
	if leaseOwner.Valid {
		v := leaseOwner.String
		job.LeaseOwner = &v
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		job.LeaseExpiresAt = &t
	}
	if heartbeatAt.Valid {
		t := heartbeatAt.Time
		job.HeartbeatAt = &t
	}
	if totalBytes.Valid {
		v := totalBytes.Int64
		job.TotalBytes = &v
	}
	if errorCode.Valid {
		v := errorCode.String
		job.ErrorCode = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		job.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		job.CompletedAt = &t
	}
	// P1#5 — YouTube session field assignments. youtube_session_uri
	// is the only credential-adjacent field; it carries the
	// `json:"-"` tag on the model so it never leaves the backend
	// via any API response. Workers MUST redact it on log lines
	// (see internal/worker/redactYouTubeSessionURI).
	if youtubeSessionURI.Valid {
		v := youtubeSessionURI.String
		job.YouTubeSessionURI = &v
	}
	if youtubeSessionOffset.Valid {
		v := youtubeSessionOffset.Int64
		job.YouTubeSessionOffset = &v
	}
	if youtubeSessionExpiresAt.Valid {
		t := youtubeSessionExpiresAt.Time
		job.YouTubeSessionExpiresAt = &t
	}
	if youtubeChunkSize.Valid {
		v := youtubeChunkSize.Int64
		job.YouTubeChunkSize = &v
	}
	if youtubeLastChunkAt.Valid {
		t := youtubeLastChunkAt.Time
		job.YouTubeLastChunkAt = &t
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}
