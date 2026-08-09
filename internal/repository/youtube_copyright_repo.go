package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/lib/pq"
)

type YouTubeCopyrightCheckStore interface {
	ListPendingCopyrightChecks(ctx context.Context, limit int, before time.Time) ([]models.YouTubeCopyrightCandidate, error)
	MarkCopyrightChecked(ctx context.Context, id int64, result models.YouTubeCopyrightResult) error
	MarkCopyrightCheckError(ctx context.Context, id int64, message string) error
}

type YouTubeCopyrightAlertStore interface {
	ListCopyrightAlertsByWorkspace(ctx context.Context, workspaceIDs []int64) ([]models.YouTubeCopyrightAlert, error)
}

func (r *YouTubeTargetPublicationRepository) ListPendingCopyrightChecks(ctx context.Context, limit int, before time.Time) ([]models.YouTubeCopyrightCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, platform_account_id, youtube_video_id
		FROM youtube_target_publications
		WHERE youtube_video_id IS NOT NULL
		  AND (copyright_checked_at IS NULL OR copyright_checked_at < $1)
		  AND copyright_status IN ('pending', 'processing', 'error')
		ORDER BY copyright_checked_at NULLS FIRST, id ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list youtube copyright checks: %w", err)
	}
	defer rows.Close()
	var out []models.YouTubeCopyrightCandidate
	for rows.Next() {
		var c models.YouTubeCopyrightCandidate
		var videoID sql.NullString
		if err := rows.Scan(&c.ID, &c.PlatformAccountID, &videoID); err != nil {
			return nil, fmt.Errorf("scan youtube copyright check: %w", err)
		}
		if videoID.Valid && videoID.String != "" {
			c.VideoID = videoID.String
			out = append(out, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate youtube copyright checks: %w", err)
	}
	return out, nil
}

func (r *YouTubeTargetPublicationRepository) MarkCopyrightChecked(ctx context.Context, id int64, result models.YouTubeCopyrightResult) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE youtube_target_publications
		SET copyright_status=$2, copyright_message=$3,
		    copyright_rejection_reason=NULLIF($4, ''), copyright_failure_reason=NULLIF($5, ''),
		    copyright_processing_status=NULLIF($6, ''), copyright_licensed_content=$7,
		    copyright_blocked_regions=$8, copyright_allowed_regions=$9,
		    copyright_checked_at=NOW(), copyright_check_error='', updated_at=NOW()
		WHERE id=$1`, id, string(result.Status), result.Message, result.RejectionReason,
		result.FailureReason, result.ProcessingStatus, result.LicensedContent,
		pq.Array(result.BlockedRegions), pq.Array(result.AllowedRegions))
	if err != nil {
		return fmt.Errorf("mark youtube copyright checked: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrYouTubeTargetPublicationNotFound
	}
	return nil
}

func (r *YouTubeTargetPublicationRepository) MarkCopyrightCheckError(ctx context.Context, id int64, message string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE youtube_target_publications
		SET copyright_status='error', copyright_message=$2, copyright_check_error=$2,
		    copyright_checked_at=NOW(), updated_at=NOW()
		WHERE id=$1`, id, message)
	if err != nil {
		return fmt.Errorf("mark youtube copyright error: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrYouTubeTargetPublicationNotFound
	}
	return nil
}

func (r *YouTubeTargetPublicationRepository) ListCopyrightAlertsByWorkspace(ctx context.Context, workspaceIDs []int64) ([]models.YouTubeCopyrightAlert, error) {
	if len(workspaceIDs) == 0 {
		return []models.YouTubeCopyrightAlert{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT y.id, pt.post_id, y.upload_job_id, y.post_target_id, y.platform_account_id, y.youtube_video_id,
		       y.copyright_status, y.copyright_message, COALESCE(y.copyright_rejection_reason, ''),
		       COALESCE(y.copyright_failure_reason, ''), COALESCE(y.copyright_processing_status, ''),
		       y.copyright_licensed_content, y.copyright_blocked_regions,
		       y.copyright_allowed_regions, y.copyright_checked_at
		FROM youtube_target_publications y
		JOIN post_targets pt ON pt.id = y.post_target_id
		JOIN posts p ON p.id = pt.post_id
		WHERE p.workspace_id = ANY($1::bigint[])
		  AND y.copyright_status IN ('claim', 'blocked', 'error')
		ORDER BY y.updated_at DESC`, pq.Array(workspaceIDs))
	if err != nil {
		return nil, fmt.Errorf("list youtube copyright alerts: %w", err)
	}
	defer rows.Close()
	var out []models.YouTubeCopyrightAlert
	for rows.Next() {
		var alert models.YouTubeCopyrightAlert
		var videoID string
		var blocked, allowed pq.StringArray
		if err := rows.Scan(&alert.ID, &alert.PostID, &alert.UploadJobID, &alert.PostTargetID, &alert.PlatformAccountID,
			&videoID, &alert.Status, &alert.Message, &alert.RejectionReason, &alert.FailureReason,
			&alert.ProcessingStatus, &alert.LicensedContent, &blocked, &allowed, &alert.CheckedAt); err != nil {
			return nil, fmt.Errorf("scan youtube copyright alert: %w", err)
		}
		alert.YouTubeVideoID, alert.BlockedRegions, alert.AllowedRegions = videoID, []string(blocked), []string(allowed)
		out = append(out, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate youtube copyright alerts: %w", err)
	}
	return out, nil
}
