package repository

import (
	"context"
	"database/sql"
	"fmt"

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
	Update(ctx context.Context, edit *models.YouTubeVideoEdit) error
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

// Update persists lifecycle changes to an existing edit session.
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
