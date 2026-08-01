package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Update persists arbitrary lifecycle changes to a publication row. Use
// the narrow Mark* methods for the common transitions; Update is the
// catch-all for callers driving the row through states the schema doesn't
// know about (e.g. external reconciliation setting both thumbnail_status
// and youtube_upload_status in one pass).
//
// Returns ErrYouTubeTargetPublicationNotFound (wrapped) when 0 rows
// match — distinct from a real *sql.DB error so callers can branch.
func (r *YouTubeTargetPublicationRepository) Update(ctx context.Context, pub *models.YouTubeTargetPublication) error {
	// Blocco #3 P0 — Update persists caller-supplied timestamp values
	// (e.g. operator backfill via a one-off script) verbatim. The
	// recommended transition path is the Mark* helpers (which stamp
	// NOW() atomically); Update is the catch-all for callers driving
	// the row through states the schema doesn't know about. We do
	// NOT touch youtube_uploaded_at/youtube_processed_at from Update's
	// SQL — those columns are exclusively Mark* managed to keep the
	// "timestamp implies status transition" invariant observable.
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET upload_job_id = $2, post_target_id = $3, platform_account_id = $4,
		     youtube_video_id = $5, youtube_upload_status = $6, youtube_processing_status = $7,
		     editor_session_id = $8, velox_project_id = $9, thumbnail_media_id = $10,
		     thumbnail_status = $11, desired_privacy = $12, publish_at = $13, published_at = $14,
		     last_error = $15, attempt_count = $16, updated_at = NOW()
		 WHERE id = $1`,
		pub.ID, pub.UploadJobID, pub.PostTargetID, pub.PlatformAccountID,
		ytPubsNullableString(pub.YouTubeVideoID), pub.YouTubeUploadStatus, ytPubsNullableString(pub.YouTubeProcessingStatus),
		ytPubsNullableString(pub.EditorSessionID), ytPubsNullableString(pub.VeloxProjectID),
		ytPubsNullableString(pub.ThumbnailMediaID), ytPubsNullableString(pub.ThumbnailStatus),
		pub.DesiredPrivacy, ytPubsNullableTime(pub.PublishAt), ytPubsNullableTime(pub.PublishedAt),
		pub.LastError, pub.AttemptCount,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication Update: %w", err)
	}
	return r.checkRowsAffected(res, pub.ID)
}

// MarkYouTubeUploaded stamps the YouTube video_id returned by the
// resumable upload API and transitions youtube_upload_status to
// 'youtube_uploaded'. Idempotent: a second call overwrites the
// video_id with whatever YouTube echoed (defensive against retries that
// picked a different URI on resume).
//
// Blocco #3 P0 (migration 067) — also stamps youtube_uploaded_at = NOW()
// atomically with the status transition. The composite write means
// any DB-shape check that asserts "youtube_upload_status='youtube_uploaded'
// ⇒ youtube_uploaded_at IS NOT NULL" holds without needing a trigger.
// The timestamp is NOT refreshed on a re-Mark (idempotency deliberately
// keeps the FIRST transition timestamp so the operator-triage
// dashboard's "time-to-upload" SLA accurate).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeUploaded(ctx context.Context, id int64, videoID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_video_id = $2,
		     youtube_upload_status = 'youtube_uploaded',
		     youtube_uploaded_at = COALESCE(youtube_uploaded_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id, videoID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeUploaded: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkThumbnailReady is the per-target equivalent of the global thumbnail
// ready event: stamps the media_assets.id the thumbnail worker resolved
// to AND transitions thumbnail_status to 'thumbnail_ready' atomically.
// Composite 1-row transition so the unified pipeline view never renders
// a half-state.
func (r *YouTubeTargetPublicationRepository) MarkThumbnailReady(ctx context.Context, id int64, mediaID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET thumbnail_media_id = $2, thumbnail_status = 'thumbnail_ready', updated_at = NOW()
		 WHERE id = $1`,
		id, mediaID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkThumbnailReady: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// IncrementAttempt bumps attempt_count and stamps last_error in one
// statement. The worker's Claim+increment pattern stays the right shape
// — pair IncrementAttempt with whichever transition the worker is
// retrying (e.g. MarkThumbnailReady once Velox responds 200).
//
// Blocco #1 followup — Finding #3 split-tx drift fix: IncrementAttempt
// is now the **FAILURE-PATH ONLY** bump. The SUCCESS path uses
// MarkYouTubeUploadedAtomic (which folds the attempt++ with the
// status flip + video_id stamp into one atomic UPDATE). Do NOT
// replace IncrementAttempt with MarkYouTubeUploadedAtomic on
// failure paths — IncrementAttempt stamps `last_error = "upload
// failed: ..."` for the operator-triage dashboard's retry-observability
// view; a status flip + video_id stamp on a failed-but-not-uploaded
// row would silently corrupt the unified-pipeline view. Call sites:
//   - FAILURE path (UploadVideoAsPrivate returns err): IncrementAttempt(...)
//   - SUCCESS path (UploadVideoAsPrivate returns non-empty videoID): MarkYouTubeUploadedAtomic(...)
func (r *YouTubeTargetPublicationRepository) IncrementAttempt(ctx context.Context, id int64, lastError string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1, last_error = $2, updated_at = NOW()
		 WHERE id = $1`,
		id, lastError,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication IncrementAttempt: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkYouTubeUploadedAtomic (Blocco #1 followup — Finding #3) is the
// success-path atomic transition: it folds IncrementAttempt-by-1
// + the status flip to 'youtube_uploaded' + youtube_video_id stamp
// + the youtube_uploaded_at timestamp (only on first transition, via
// COALESCE so re-runs preserve the FIRST transition timestamp) +
// updated_at touch into one UPDATE. Postgres row-level UPDATE is
// ACID-atomic so the attempt counter and the terminal-state stamp
// cannot desync if the worker crashes mid-call (the pre-fix split
// form — separate IncrementAttempt (failure) + MarkYouTubeUploaded
// (success) on different code paths — could leave a row with
// `attempt_count++ + status='youtube_uploading'` on a partial
// commit, making the next claim's idempotent-skip false and producing
// an orphan videos.insert). Upfront guards: videoID == "" is
// rejected with a typed error, no DB call. Returns
// ErrYouTubeTargetPublicationNotFound on 0 rows (same shape as
// MarkYouTubeUploaded).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeUploadedAtomic(ctx context.Context, id int64, videoID string) error {
	// Pre-flight guard (Finding #3). Empty videoID MUST be rejected
	// BEFORE issuing any UPDATE — otherwise the row would be
	// left in `status='youtube_uploading'` with attempt_count++, the
	// exact pre-fix failure mode. The typed sentinel wraps the id
	// so the worker log can attribute the rejection precisely.
	if videoID == "" {
		return fmt.Errorf("%w: pub=%d", ErrYouTubeUploadedEmptyVideoID, id)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1,
		     youtube_video_id = $2,
		     youtube_upload_status = 'youtube_uploaded',
		     youtube_uploaded_at = COALESCE(youtube_uploaded_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id, videoID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeUploadedAtomic: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// ClearYouTubeUpload (Blocco #1 followup — Finding #4 orphan-video
// recovery) nullifies the Phase-1 youtube_video_id stamp and resets
// youtube_upload_status / attempt_count to their pre-Publish
// initial values in ONE Postgres UPDATE so a future publish-worker
// tick that picks up the same post_target_id does NOT re-take the
// bypass branch with a dead video_id. Called by
// PublishWorker.publishTarget when videos.update returns a 404 on
// the Phase-1 stamped video_id (user deleted the orphan via YouTube
// Studio, moderator takedown, etc.). The next tick sees
// youtube_upload_status='upload_session_initiated' + NULL
// youtube_video_id and falls through to publisher.Publish +
// upload_worker.uploadVideoAsPrivateForTarget, which freshly uploads
// and stamps a new video_id via MarkYouTubeUploadedAtomic.
//
// Single-statement UPDATE = ACID-atomic at the row level (same
// isolation guarantee as MarkYouTubeUploadedAtomic). Returns
// ErrYouTubeTargetPublicationNotFound on 0 rows (matches the
// MarkYouTubeUploaded / MarkYouTubeUploadedAtomic shape so
// callers can use a single typed-sentinel branch).
func (r *YouTubeTargetPublicationRepository) ClearYouTubeUpload(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_video_id = NULL,
		     youtube_upload_status = 'upload_session_initiated',
		     attempt_count = 0,
		     updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication ClearYouTubeUpload: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkPublished stamps published_at and touches updated_at. youtube_upload_status
// is intentionally NOT bumped here — 'youtube_uploaded' continues to
// describe YouTube-side state; the publish phase's terminal moment lives
// on published_at alone. If the worker wants to surface a column for
// "publish phase done", the future migration can add an enum value.
func (r *YouTubeTargetPublicationRepository) MarkPublished(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET published_at = NOW(), updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkPublished: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// ListPendingEditorSessionTargets (Blocco #4 P0) returns YT pub rows
// where youtube_processing_status='processed' AND editor_session_id IS
// NULL, ordered by id ASC, bounded by limit. Used by
// youtube_processing_reconciler. (nil, nil) when no rows match —
// distinct from a real *sql.DB error so callers can branch.
func (r *YouTubeTargetPublicationRepository) ListPendingEditorSessionTargets(ctx context.Context, limit int) ([]*models.YouTubeTargetPublication, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE youtube_processing_status = 'processed'
		   AND editor_session_id IS NULL
		 ORDER BY id ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets rows: %w", err)
	}
	return out, nil
}

// MarkEditorSessionCreated (Blocco #4 P0) is the atomic CAS-link from
// a created youtube_video_edits session back to the YT pub row.
// Predicate `editor_session_id IS NULL` (combined with the
// ytTargetPubsSelectColumns row state read-out from the caller) means
// the FIRST CAS wins; a second reconciler that races the same YT pub
// row matches 0 rows and surfaces ErrYouTubeTargetPublicationNotFound
// — clean branch for the caller (skip + log, no partial write).
//
// Migration 068's UNIQUE constraint on editor_session_id is the
// defence-in-depth layer: if a future refactor accidentally drops the
// predicate, the UNIQUE index still prevents a duplicate stamp at the
// cost of an INSERT failure. The composite 1-row UPDATE keeps the
// reconciler's success path to a single SQL roundtrip.
func (r *YouTubeTargetPublicationRepository) MarkEditorSessionCreated(ctx context.Context, id int64, editorSessionID, veloxProjectID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET editor_session_id = $2,
		     velox_project_id = $3,
		     updated_at = NOW()
		 WHERE id = $1
		   AND editor_session_id IS NULL`,
		id, editorSessionID, veloxProjectID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkEditorSessionCreated: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkYouTubeProcessed (Blocco #3 P0, migration 067) flips
// youtube_processing_status to 'processed' AND stamps
// youtube_processed_at = NOW() in one atomic statement. Called by:
//   - the YouTube webhook callback when processingStatus='processed'
//     arrives via the topics/push notification channel;
//   - the future reconcile worker poll (YouTube videos.list every
//     60s for in-progress 'processing' rows).
//
// COALESCE on youtube_processed_at keeps idempotency: a second
// reconcile poll that finds the row already 'processed' won't
// re-stamp the timestamp (preserves "time-to-processed" SLA
// accuracy across retries).
//
// Returns ErrYouTubeTargetPublicationNotFound when 0 rows match —
// distinct from a real DB error so callers can branch on
// errors.Is(..., ErrYouTubeTargetPublicationNotFound).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeProcessed(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_processing_status = 'processed',
		     youtube_processed_at = COALESCE(youtube_processed_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeProcessed: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

func (r *YouTubeTargetPublicationRepository) checkRowsAffected(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("youtube target publication rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrYouTubeTargetPublicationNotFound, id)
	}
	return nil
}
