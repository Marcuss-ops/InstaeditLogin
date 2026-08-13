package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// youtubeDeliveryClaimStates is the set of states the delivery pool may
// claim. Matches the partial predicate of migration 125's
// idx_yt_delivery_claim index (preflight, ready_to_upload, retry_wait,
// quota_wait) so the claim query can use the index without a filter
// mismatch:
//   - preflight      — row created without an explicit state (legacy /
//                      materializer race); the worker re-derives on claim.
//   - ready_to_upload — the normal enqueue state the materializer stamps.
//   - retry_wait     — failed; re-claimable once next_attempt_at elapses.
//   - quota_wait     — capacity-blocked; re-claimable once next_attempt_at
//                      elapses (resume_state records where to return).
const youtubeDeliveryClaimStates = `('preflight', 'ready_to_upload', 'retry_wait', 'quota_wait')`

// ytTargetPubsSelectColumnsQualified is ytTargetPubsSelectColumns with
// every column prefixed `yt.` — needed by the claim query's RETURNING
// clause, where both the updated row (yt) and the CTE (eligible) expose
// an `id` and an unqualified reference would be ambiguous (42702).
const ytTargetPubsSelectColumnsQualified = `
	yt.id, yt.upload_job_id, yt.post_target_id, yt.platform_account_id,
	yt.youtube_video_id, yt.youtube_upload_status, yt.youtube_processing_status,
	yt.youtube_uploaded_at, yt.youtube_processed_at,
	yt.editor_session_id, yt.velox_project_id, yt.thumbnail_media_id, yt.thumbnail_status,
	yt.desired_privacy, yt.publish_at, yt.native_publish_at, yt.published_at, yt.last_error, yt.attempt_count,
	yt.state, yt.priority, yt.prepare_at, yt.next_attempt_at, yt.max_attempts,
	yt.lease_owner, yt.lease_expires_at, yt.heartbeat_at, yt.resume_state,
	yt.last_error_code, yt.last_transition_at, yt.verified_at, yt.original_publish_at, yt.spillover_count,
	yt.created_at, yt.updated_at`

// ClaimReadyDeliveries atomically claims up to `limit` per-(video,
// channel) delivery rows from youtube_target_publications for workerID.
// This is the queue-unit the global delivery pool consumes: a single
// upload_job with N YouTube targets fans out to N independent rows, so
// N channels can be uploaded concurrently by different pool workers
// instead of one worker looping targets sequentially inside a job
// claim.
//
// Claim shape (same CTE + FOR UPDATE SKIP LOCKED + lease-CAS pattern as
// upload_jobs / webhook_deliveries):
//
//	1. SELECT the eligible ids (claimable state + due + unlocked),
//	   ordered by priority (lower first), then publish_at, then id, and
//	   lock them with FOR UPDATE SKIP LOCKED so concurrent replicas
//	   never block on each other.
//	2. UPDATE those ids to state='uploading' with lease_owner /
//	   lease_expires_at / heartbeat_at stamps.
//	3. RETURN the full rows so the worker has everything (video_id
//	   cursor, channel, publish_at, native_publish_at) without a second
//	   round-trip.
//
// Returns the claimed rows. A worker holds each row for at least
// `lease`; the caller MUST run HeartbeatDelivery on a cadence below
// lease/3 or ReclaimExpiredDeliveryLeases will hand the row to a peer.
func (r *YouTubeTargetPublicationRepository) ClaimReadyDeliveries(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]*models.YouTubeTargetPublication, error) {
	if workerID == "" {
		return nil, fmt.Errorf("claim ready deliveries: empty workerID")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("claim ready deliveries: non-positive limit %d", limit)
	}
	if lease <= 0 {
		return nil, fmt.Errorf("claim ready deliveries: non-positive lease %s", lease)
	}
	leaseSeconds := int(lease.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}

	rows, err := r.db.QueryContext(ctx,
		`WITH eligible AS (
		     SELECT id
		     FROM youtube_target_publications
		     WHERE state IN `+youtubeDeliveryClaimStates+`
		       AND (prepare_at IS NULL OR prepare_at <= NOW())
		       AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		       AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		     ORDER BY priority ASC, publish_at ASC NULLS LAST, id ASC
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		 )
		 UPDATE youtube_target_publications yt
		 SET state = 'uploading',
		     lease_owner = $2,
		     lease_expires_at = NOW() + ($3 * INTERVAL '1 second'),
		     heartbeat_at = NOW(),
		     last_transition_at = NOW()
		 FROM eligible
		 WHERE yt.id = eligible.id
		 RETURNING `+ytTargetPubsSelectColumnsQualified,
		limit, workerID, leaseSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("claim ready deliveries: %w", err)
	}
	defer rows.Close()

	var claimed []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("claim ready deliveries scan: %w", err)
		}
		claimed = append(claimed, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim ready deliveries iterate: %w", err)
	}
	return claimed, nil
}

// HeartbeatDelivery extends the lease on a claimed delivery row.
// Returns false when the row is no longer owned by workerID (peer stole
// the lease via ReclaimExpiredDeliveryLeases, or the row was
// completed/failed by another path) — the caller must stop processing.
func (r *YouTubeTargetPublicationRepository) HeartbeatDelivery(
	ctx context.Context,
	id int64,
	workerID string,
	lease time.Duration,
) (bool, error) {
	if workerID == "" {
		return false, fmt.Errorf("heartbeat delivery: empty workerID")
	}
	if lease <= 0 {
		return false, fmt.Errorf("heartbeat delivery: non-positive lease %s", lease)
	}
	leaseSeconds := int(lease.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET heartbeat_at = NOW(),
		     lease_expires_at = NOW() + ($3 * INTERVAL '1 second')
		 WHERE id = $1 AND lease_owner = $2`,
		id, workerID, leaseSeconds,
	)
	if err != nil {
		return false, fmt.Errorf("heartbeat delivery id=%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("heartbeat delivery rows affected id=%d: %w", id, err)
	}
	return n > 0, nil
}

// ReleaseDeliveryLease clears the lease stamps on a claimed row without
// changing its state. Used when the processor decides the row needs no
// work (idempotent skip of an already-uploaded delivery, non-YouTube
// platform, etc.) so the row becomes claimable again immediately.
func (r *YouTubeTargetPublicationRepository) ReleaseDeliveryLease(
	ctx context.Context,
	id int64,
	workerID string,
) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET lease_owner = NULL, lease_expires_at = NULL, heartbeat_at = NULL
		 WHERE id = $1 AND lease_owner = $2`,
		id, workerID,
	)
	if err != nil {
		return fmt.Errorf("release delivery lease id=%d: %w", id, err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkDeliveryUploaded is the delivery-queue success terminal for the
// private-upload phase: one atomic UPDATE folds the video_id stamp, the
// youtube_upload_status flip, the state transition to 'youtube_uploaded',
// the attempt++ bump and the lease release. The row becomes claimable
// by later phases (thumbnail / publish) whose queries key off
// youtube_upload_status / state rather than the lease columns.
func (r *YouTubeTargetPublicationRepository) MarkDeliveryUploaded(
	ctx context.Context,
	id int64,
	workerID string,
	videoID string,
) error {
	if videoID == "" {
		return fmt.Errorf("%w: pub=%d", ErrYouTubeUploadedEmptyVideoID, id)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_video_id = $3,
		     youtube_upload_status = 'youtube_uploaded',
		     youtube_uploaded_at = COALESCE(youtube_uploaded_at, NOW()),
		     attempt_count = attempt_count + 1,
		     state = 'youtube_uploaded',
		     lease_owner = NULL,
		     lease_expires_at = NULL,
		     heartbeat_at = NULL,
		     last_transition_at = NOW()
		 WHERE id = $1 AND lease_owner = $2`,
		id, workerID, videoID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkDeliveryUploaded: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkDeliveryFailed routes a failed delivery to retry_wait (with the
// given next_attempt_at backoff cursor) or to dead_letter once
// attempt_count reaches max_attempts. Folds the attempt bump, the
// error stamps and the lease release into one atomic UPDATE so a crash
// cannot leave the row half-mutated (same split-tx rationale as
// MarkYouTubeUploadedAtomic).
func (r *YouTubeTargetPublicationRepository) MarkDeliveryFailed(
	ctx context.Context,
	id int64,
	workerID string,
	errorCode, errMessage string,
	nextAttemptAt time.Time,
) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1,
		     last_error = $3,
		     last_error_code = $4,
		     next_attempt_at = $5,
		     state = CASE
		         WHEN attempt_count + 1 >= max_attempts THEN 'dead_letter'
		         ELSE 'retry_wait'
		     END,
		     lease_owner = NULL,
		     lease_expires_at = NULL,
		     heartbeat_at = NULL,
		     last_transition_at = NOW()
		 WHERE id = $1 AND lease_owner = $2`,
		id, workerID, errMessage, errorCode, nextAttemptAt,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkDeliveryFailed: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// ReclaimExpiredDeliveryLeases recovers rows stuck in 'uploading' whose
// lease expired (worker crash / network partition without a heartbeat).
// It returns them to 'ready_to_upload' so the next claim re-runs the
// upload idempotently (FindByPostTargetID short-circuits already-
// uploaded rows; the resumable-session columns on the parent
// upload_job survive, so YouTube-side work is not restarted from zero
// when the session is still valid).
func (r *YouTubeTargetPublicationRepository) ReclaimExpiredDeliveryLeases(
	ctx context.Context,
	maxRows int,
) (int64, error) {
	if maxRows <= 0 {
		maxRows = 1
	}
	res, err := r.db.ExecContext(ctx,
		`WITH expired AS (
		     SELECT id
		     FROM youtube_target_publications
		     WHERE state = 'uploading'
		       AND lease_expires_at IS NOT NULL
		       AND lease_expires_at < NOW()
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		 )
		 UPDATE youtube_target_publications yt
		 SET state = 'ready_to_upload',
		     lease_owner = NULL,
		     lease_expires_at = NULL,
		     heartbeat_at = NULL,
		     last_transition_at = NOW()
		 FROM expired
		 WHERE yt.id = expired.id`,
		maxRows,
	)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired delivery leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reclaim expired delivery leases rows affected: %w", err)
	}
	return n, nil
}

// MarkDeliveryBlockedAuth routes a delivery to the terminal 'failed'
// state on a channels.list(mine=true) channel-binding mismatch. The
// mismatch is structural (needs operator re-auth), so the row must NOT
// be retried — this stamps state='failed' + attempt++ + last_error +
// last_error_code and releases the lease in one atomic UPDATE. The
// post_target.status='blocked_auth' + platform_account.status=
// 'reauth_required' side effects are owned by the worker's
// handleTargetBlockedAuth (SetTargetStatus / MarkReauthRequired).
func (r *YouTubeTargetPublicationRepository) MarkDeliveryBlockedAuth(
	ctx context.Context,
	id int64,
	workerID string,
	reason string,
) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1,
		     last_error = $3,
		     last_error_code = 'blocked_auth',
		     state = 'failed',
		     lease_owner = NULL,
		     lease_expires_at = NULL,
		     heartbeat_at = NULL,
		     last_transition_at = NOW()
		 WHERE id = $1 AND lease_owner = $2`,
		id, workerID, reason,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkDeliveryBlockedAuth: %w", err)
	}
	return r.checkRowsAffected(res, id)
}
