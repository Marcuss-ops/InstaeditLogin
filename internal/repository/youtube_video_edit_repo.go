package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeVideoEditStore is the persistence contract for
// youtube_video_edits. It is declared in pkg/api and implemented by
// YouTubeVideoEditRepository so the API layer can depend on the
// interface while production wiring passes the concrete repository.
//
// The concrete type is intentionally placed in internal/repository
// (not pkg/api) to keep the API package free of *sql.DB details.
type YouTubeVideoEditStore interface {
	Create(ctx context.Context, edit *models.YouTubeVideoEdit) error
	FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	Update(ctx context.Context, edit *models.YouTubeVideoEdit) error
	// MarkPublishing (Blocco #5 P0 #2) atomically transitions status →
	// 'publishing' WITH desired_privacy + publish_at stamped in the
	// same statement. CAS predicate (extended form):
	//   status IN ('editing','failed')                  -- primary path
	//   OR (status='publishing' AND updated_at <
	//       NOW() - make_interval(secs => inFlightTimeout))  -- orphan recovery
	// The two-branch SQL is selected in Go (E1) based on
	// inFlightTimeout > 0 to avoid the timeout=0 degenerate case where
	// make_interval(secs => 0) would match ALL 'publishing' rows.
	// Returns (nil, ErrYouTubeVideoEditNotFound) on 0-rows match — the
	// handler maps to 409 (CAS-loss).
	MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error)
}

// YouTubeVideoEditRepository handles CRUD for youtube_video_edits.
type YouTubeVideoEditRepository struct {
	db *sql.DB
}

// NewYouTubeVideoEditRepository creates a new YouTubeVideoEditRepository.
func NewYouTubeVideoEditRepository(db *sql.DB) *YouTubeVideoEditRepository {
	return &YouTubeVideoEditRepository{db: db}
}

// Create inserts a new YouTube video edit session. It is the caller's
// responsibility to generate the id and velox_project_id.
func (r *YouTubeVideoEditRepository) Create(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO youtube_video_edits
			(id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
			 source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
			 status, last_error, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		edit.ID, edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID, edit.VeloxProjectID,
		edit.SourceThumbnailURL, edit.ThumbnailMediaID, edit.DesiredPrivacy, edit.PublishAt,
		edit.Status, edit.LastError, edit.CreatedAt, edit.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create youtube video edit: %w", err)
	}
	return nil
}

// FindByID returns the edit session with the given id, or (nil, nil)
// when no row matches.
func (r *YouTubeVideoEditRepository) FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
		        source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
		        status, last_error, created_at, updated_at
		 FROM youtube_video_edits
		 WHERE id = $1`,
		id,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID, &edit.VeloxProjectID,
		&edit.SourceThumbnailURL, &edit.ThumbnailMediaID, &edit.DesiredPrivacy, &edit.PublishAt,
		&edit.Status, &edit.LastError, &edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find youtube video edit: %w", err)
	}
	return edit, nil
}

// FindByVeloxProjectID returns the edit session for the given velox
// project id, or (nil, nil) when no row matches.
func (r *YouTubeVideoEditRepository) FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	if err := r.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
		        source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
		        status, last_error, created_at, updated_at
		 FROM youtube_video_edits
		 WHERE velox_project_id = $1`,
		projectID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID, &edit.VeloxProjectID,
		&edit.SourceThumbnailURL, &edit.ThumbnailMediaID, &edit.DesiredPrivacy, &edit.PublishAt,
		&edit.Status, &edit.LastError, &edit.CreatedAt, &edit.UpdatedAt,
	); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to find youtube video edit by project: %w", err)
	}
	return edit, nil
}

// Update persists lifecycle changes to an existing edit session.
// MarkPublishing (Blocco #5 P0 #2) is the atomic CAS claim for the
// thumbnail-publish transition. The handler's previous read-then-update
// pattern (`edit.Status = "publishing"; Update(ctx, edit)`) had a TOCTOU
// race: two concurrent publish requests could both pass the row-state
// read and fire two PublishThumbnail calls (a real production bug —
// video would be thumbnail-published twice).
//
// CAS structure:
//   STRICT BRANCH (inFlightTimeout <= 0):
//     UPDATE ... SET status, updated_at, desired_privacy, publish_at
//      WHERE id=$1 AND status IN ('editing','failed')
//      RETURNING ...
//   EXTENDED BRANCH (inFlightTimeout > 0):
//     UPDATE ... SET ...  WHERE id=$1 AND (
//        status IN ('editing','failed')
//        OR (status='publishing' AND updated_at < NOW() - make_interval(secs => $4))
//     ) RETURNING ...
//
// The extended branch is the orphan-recovery path: a previous publish
// was claimed but the worker died mid-call (status stuck at
// 'publishing' with no published_at stamp). The handler's in-flight
// read at the top of the function would have already rejected a
// recent (within timeout) sticky 'publishing' row with 409; this
// branch picks up only rows older than the timeout.
//
// include-desired_privacy-and-publish_at-in-CAS so the publish call sees
// the values that were atomically stamped (no chance of a concurrent
// reader flipping the resolved privacy between the MarkPublishing
// return and the PublishThumbnail call). Avoiding the
// read-then-modify-write reverts every race that the original
// handler was subject to (privacy/publish_at swap while the row was
// being updated).
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) when 0 rows match —
// distinct from a real *sql.DB error so the handler can branch on
// errors.Is(..., ErrYouTubeVideoEditNotFound) to map to HTTP 409.
func (r *YouTubeVideoEditRepository) MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error) {
	selectColumns := `id, workspace_id, platform_account_id, youtube_video_id,
	                   velox_project_id, source_thumbnail_url, thumbnail_media_id,
	                   desired_privacy, publish_at, status, last_error,
	                   created_at, updated_at`
	edit := &models.YouTubeVideoEdit{}
	var err error
	if inFlightTimeout <= 0 {
		// Strict branch: only editing/failed are claimable.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1 AND status IN ('editing','failed')
			 RETURNING `+selectColumns,
			id, desiredPrivacy, publishAt,
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	} else {
		// Extended branch: also re-claim a stale 'publishing' row.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1
			   AND (
			       status IN ('editing','failed')
			       OR (
			            status = 'publishing'
			            AND updated_at < NOW() - make_interval(secs => $4)
			        )
			   )
			 RETURNING `+selectColumns,
			id, desiredPrivacy, publishAt, int(inFlightTimeout.Seconds()),
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit MarkPublishing: %w", err)
	}
	return edit, nil
}

func (r *YouTubeVideoEditRepository) Update(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE youtube_video_edits
		 SET workspace_id = $2,
		     platform_account_id = $3,
		     youtube_video_id = $4,
		     velox_project_id = $5,
		     source_thumbnail_url = $6,
		     thumbnail_media_id = $7,
		     desired_privacy = $8,
		     publish_at = $9,
		     status = $10,
		     last_error = $11,
		     updated_at = $12
		 WHERE id = $1`,
		edit.ID, edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID, edit.VeloxProjectID,
		edit.SourceThumbnailURL, edit.ThumbnailMediaID, edit.DesiredPrivacy, edit.PublishAt,
		edit.Status, edit.LastError, edit.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update youtube video edit: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, edit.ID)
	}
	return nil
}
