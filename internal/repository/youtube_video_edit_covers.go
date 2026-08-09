package repository

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListCoversByGroupAccounts returns every cover project linked to the
// supplied group accounts, newest-project-update first.
//
// The join chain is the canonical project-bridge contract
// (project-bridge-contract §3.1):
//
//	thumbnail_projects.id          (the cover project)
//	JOIN velox_project_bridges     (project_id = thumbnail project id)
//	JOIN youtube_video_edits       (velox_project_id = bridge
//	                                 external_project_id)
//
// The youtube_video_edits row scopes the cover to a (workspace,
// platform_account, youtube_video) tuple, so filtering on
// `yve.platform_account_id = ANY($2)` resolves "covers in group X" to
// "cover projects whose InstaEditor session belongs to one of group
// X's accounts" in ONE SQL round-trip (no per-cover session lookups).
//
// Cover projects that predate the bridge migration or that were never
// bridged to an editor session simply do not appear here — they are
// not addressable from any group, so a group-scoped covers view cannot
// attribute them (this mirrors the group-videos phantom logic).
//
// Project status filter: `tp.status <> 'deleted'` keeps archived
// covers visible (the covers hub shows the full history — the user
// explicitly wants old projects and old covers), while soft-deleted
// projects are hidden.
//
// The row set is capped (newest project-update first) so a group with
// a pathological number of archived covers can never blow up the
// response or the SPA's lazy preview fan-out; the cap mirrors the
// group-videos convention (groupYouTubeVideosMaxTotalVideos = 500).
//
// Empty inputs collapse to (nil, nil) — a misconfigured caller never
// triggers a Postgres-side error.
func (r *YouTubeVideoEditRepository) ListCoversByGroupAccounts(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.GroupCover, error) {
	if workspaceID <= 0 {
		return nil, nil
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT tp.id, tp.workspace_id, tp.name, tp.status,
		        tp.preview_media_id, tp.latest_export_id, tp.version,
		        tp.created_at, tp.updated_at,
		        yve.id, yve.platform_account_id, yve.youtube_video_id,
		        yve.velox_project_id, yve.thumbnail_media_id,
		        yve.source_thumbnail_url, yve.status, yve.draft_title,
		        yve.draft_description,
		        yve.created_at, yve.updated_at
		   FROM thumbnail_projects tp
		   JOIN velox_project_bridges vpb
		     ON vpb.project_id = tp.id AND vpb.workspace_id = tp.workspace_id
		   JOIN youtube_video_edits yve
		     ON yve.workspace_id = tp.workspace_id
		    AND yve.velox_project_id = vpb.external_project_id
		  WHERE tp.workspace_id = $1
		    AND tp.status <> $2
		    AND yve.platform_account_id = ANY($3)
		  ORDER BY tp.updated_at DESC, tp.id
		  LIMIT 500`,
		workspaceID, models.ThumbnailProjectStatusDeleted, pq.Array(accountIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("youtube video edit ListCoversByGroupAccounts query: %w", err)
	}
	defer rows.Close()
	out := make([]*models.GroupCover, 0, 32)
	for rows.Next() {
		c := &models.GroupCover{}
		if err := rows.Scan(
			&c.ProjectID, &c.WorkspaceID, &c.ProjectName, &c.ProjectStatus,
			&c.PreviewMediaID, &c.LatestExportID, &c.ProjectVersion,
			&c.ProjectCreatedAt, &c.ProjectUpdatedAt,
			&c.SessionID, &c.PlatformAccountID, &c.YouTubeVideoID,
			&c.VeloxProjectID, &c.ThumbnailMediaID,
			&c.SourceThumbnailURL, &c.EditStatus, &c.DraftTitle,
			&c.DraftDescription,
			&c.SessionCreatedAt, &c.SessionUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("youtube video edit ListCoversByGroupAccounts scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube video edit ListCoversByGroupAccounts rows: %w", err)
	}
	return out, nil
}
