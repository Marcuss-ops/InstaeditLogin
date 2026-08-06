package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ErrLivestreamNotFound is returned by Delete when no row matched the
// id (the handler maps it to 404).
var ErrLivestreamNotFound = errors.New("livestream not found")

// LivestreamStore is the persistence contract for the livestream
// module. The CRUD handlers depend on this interface; the concrete
// *LivestreamRepository implements it against Postgres.
type LivestreamStore interface {
	Create(ctx context.Context, ls *models.Livestream) error
	FindByID(ctx context.Context, id string) (*models.Livestream, error)
	ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.Livestream, error)
	// Update writes the operator-owned configuration columns
	// (title/description/privacy/playback/schedule/encoding). State
	// columns AND the YouTube resource references are worker-owned
	// and deliberately NOT updated here — the worker follow-up adds
	// dedicated methods for those.
	Update(ctx context.Context, ls *models.Livestream) error
	Delete(ctx context.Context, id string) error
}

type LivestreamRepository struct{ db *sql.DB }

func NewLivestreamRepository(db *sql.DB) *LivestreamRepository {
	return &LivestreamRepository{db: db}
}

const livestreamColumns = `id, workspace_id, platform_account_id, created_by, title,
description, privacy_status, playback_mode, schedule_type, scheduled_start_at,	desired_state, actual_state, desired_generation, configuration_version,
youtube_broadcast_id, youtube_stream_id,
resolution, frame_rate, auto_restart,
category, made_for_kids, language, thumbnail_media_id,
dvr_enabled, auto_start, auto_stop, latency_preference,
created_at, updated_at`

func scanLivestream(row interface{ Scan(...any) error }) (*models.Livestream, error) {
	ls := &models.Livestream{}
	if err := row.Scan(&ls.ID, &ls.WorkspaceID, &ls.PlatformAccountID, &ls.CreatedBy, &ls.Title,
		&ls.Description, &ls.PrivacyStatus, &ls.PlaybackMode, &ls.ScheduleType, &ls.ScheduledStartAt,
		&ls.DesiredState, &ls.ActualState, &ls.DesiredGeneration, &ls.ConfigurationVersion,
		&ls.YouTubeBroadcastID, &ls.YouTubeStreamID,
		&ls.Resolution, &ls.FrameRate, &ls.AutoRestart,
		&ls.Category, &ls.MadeForKids, &ls.Language, &ls.ThumbnailMediaID,
		&ls.DVREnabled, &ls.AutoStart, &ls.AutoStop, &ls.LatencyPreference,
		&ls.CreatedAt, &ls.UpdatedAt); err != nil {
		return nil, err
	}
	return ls, nil
}

func (r *LivestreamRepository) Create(ctx context.Context, ls *models.Livestream) error {
	if ls == nil || ls.ID == "" || ls.WorkspaceID <= 0 || ls.PlatformAccountID <= 0 {
		return errors.New("invalid livestream")
	}
	if !models.LivestreamDesiredState(ls.DesiredState).Valid() || !models.ValidLivestreamState(ls.ActualState) {
		return errors.New("invalid livestream state")
	}
	if ls.DesiredGeneration <= 0 {
		ls.DesiredGeneration = 1
	}
	if ls.ConfigurationVersion <= 0 {
		ls.ConfigurationVersion = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO livestreams
			(id, workspace_id, platform_account_id, created_by, title, description,
			 privacy_status, playback_mode, schedule_type, scheduled_start_at,
			 desired_state, actual_state, desired_generation, configuration_version,
			 youtube_broadcast_id, youtube_stream_id,
			 resolution, frame_rate, auto_restart,
			 category, made_for_kids, language, thumbnail_media_id,
			 dvr_enabled, auto_start, auto_stop, latency_preference)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
		        $20, $21, $22, $23, $24, $25, $26, $27)`,
		ls.ID, ls.WorkspaceID, ls.PlatformAccountID, ls.CreatedBy, ls.Title, ls.Description,
		ls.PrivacyStatus, ls.PlaybackMode, ls.ScheduleType, ls.ScheduledStartAt,
		ls.DesiredState, ls.ActualState, ls.DesiredGeneration, ls.ConfigurationVersion,
		ls.YouTubeBroadcastID, ls.YouTubeStreamID,
		ls.Resolution, ls.FrameRate, ls.AutoRestart,
		ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
		ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference)
	if err != nil {
		return fmt.Errorf("insert livestream: %w", err)
	}
	return nil
}

func (r *LivestreamRepository) FindByID(ctx context.Context, id string) (*models.Livestream, error) {
	ls, err := scanLivestream(r.db.QueryRowContext(ctx,
		`SELECT `+livestreamColumns+` FROM livestreams WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find livestream: %w", err)
	}
	return ls, nil
}

func (r *LivestreamRepository) ListByWorkspacePage(ctx context.Context, workspaceID int64, afterTime *time.Time, afterID string, limit int) ([]models.Livestream, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	var after interface{}
	if afterTime != nil {
		after = *afterTime
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+livestreamColumns+` FROM livestreams
		 WHERE workspace_id = $1
		   AND ($2::timestamptz IS NULL OR (updated_at, id) < ($2, $3))
		 ORDER BY updated_at DESC, id DESC LIMIT $4`, workspaceID, after, afterID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list paginated livestreams: %w", err)
	}
	defer rows.Close()
	result := make([]models.Livestream, 0, limit+1)
	for rows.Next() {
		ls, scanErr := scanLivestream(rows)
		if scanErr != nil {
			return nil, false, fmt.Errorf("scan paginated livestream: %w", scanErr)
		}
		result = append(result, *ls)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate paginated livestreams: %w", err)
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

func (r *LivestreamRepository) ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+livestreamColumns+` FROM livestreams
		 WHERE workspace_id = $1 ORDER BY updated_at DESC, id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list livestreams: %w", err)
	}
	defer rows.Close()
	var result []models.Livestream
	for rows.Next() {
		ls, scanErr := scanLivestream(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan livestream: %w", scanErr)
		}
		result = append(result, *ls)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate livestreams: %w", err)
	}
	return result, nil
}

func (r *LivestreamRepository) Update(ctx context.Context, ls *models.Livestream) error {
	return r.UpdateWithVersion(ctx, ls, ls.ConfigurationVersion)
}

// UpdateWithVersion applies operator-owned configuration using an optimistic
// version check. A successful update increments configuration_version; a
// stale worker/API snapshot is rejected without changing the row.
func (r *LivestreamRepository) UpdateWithVersion(ctx context.Context, ls *models.Livestream, expectedVersion int64) error {
	if ls == nil || ls.ID == "" || expectedVersion <= 0 {
		return errors.New("invalid livestream or configuration version")
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE livestreams SET
			title = $2, description = $3, privacy_status = $4, playback_mode = $5,
			schedule_type = $6, scheduled_start_at = $7, resolution = $8,
			frame_rate = $9, auto_restart = $10, category = $11,
			made_for_kids = $12, language = $13, thumbnail_media_id = $14,
			dvr_enabled = $15, auto_start = $16, auto_stop = $17,
			latency_preference = $18, configuration_version = configuration_version + 1,
			updated_at = NOW()
		WHERE id = $1 AND configuration_version = $19`,
		ls.ID, ls.Title, ls.Description, ls.PrivacyStatus, ls.PlaybackMode,
		ls.ScheduleType, ls.ScheduledStartAt, ls.Resolution, ls.FrameRate, ls.AutoRestart,
		ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
		ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference, expectedVersion)
	if err != nil {
		return fmt.Errorf("update livestream: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update livestream rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: livestream_id=%s expected_version=%d", models.ErrLivestreamConfigurationStale, ls.ID, expectedVersion)
	}
	ls.ConfigurationVersion = expectedVersion + 1
	return nil
}

// SetDesiredState records operator intent and increments desired_generation
// only when the current desired state/version match the caller's snapshot.
func (r *LivestreamRepository) SetDesiredState(ctx context.Context, livestreamID string, expectedVersion int64, from, to models.LivestreamDesiredState) (int64, error) {
	if livestreamID == "" || expectedVersion <= 0 || !models.CanTransitionDesiredState(from, to) {
		return 0, fmt.Errorf("%w: %s -> %s", models.ErrInvalidLivestreamDesiredTransition, from, to)
	}
	var generation int64
	err := r.db.QueryRowContext(ctx, `
		UPDATE livestreams
		   SET desired_state = $2,
		       desired_generation = desired_generation + 1,
		       configuration_version = configuration_version + 1,
		       updated_at = NOW()
		 WHERE id = $1
		   AND configuration_version = $3
		   AND desired_state = $4
		RETURNING desired_generation`, livestreamID, to, expectedVersion, from).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: livestream_id=%s", models.ErrLivestreamConfigurationStale, livestreamID)
	}
	if err != nil {
		return 0, fmt.Errorf("set livestream desired state: %w", err)
	}
	return generation, nil
}

func (r *LivestreamRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM livestreams WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete livestream: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete livestream rows affected: %w", err)
	}
	if n == 0 {
		return ErrLivestreamNotFound
	}
	return nil
}
