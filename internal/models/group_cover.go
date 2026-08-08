package models

import "time"

// GroupCover is the joined read-model backing
// GET /api/v1/groups/{group_id}/covers: a thumbnail cover project
// (thumbnail_projects) bridged to its InstaEditor project
// (velox_project_bridges) and to the YouTube editor session that
// scopes it to a group account (youtube_video_edits).
//
// The two created_at/updated_at pairs are kept distinct on purpose:
// the project pair is the cover library timestamp ("when did I last
// touch this cover"), the session pair is the publish-flow timestamp
// ("when did the editor session last change"). The SPA renders the
// project timestamps in the covers grid.
type GroupCover struct {
	// Project fields (thumbnail_projects)
	ProjectID        string
	WorkspaceID      int64
	ProjectName      string
	ProjectStatus    ThumbnailProjectStatus
	PreviewMediaID   *string
	LatestExportID   *string
	ProjectVersion   int64
	ProjectCreatedAt time.Time
	ProjectUpdatedAt time.Time

	// Session fields (youtube_video_edits)
	SessionID          string
	PlatformAccountID  int64
	YouTubeVideoID     string
	VeloxProjectID     string
	ThumbnailMediaID   *string
	SourceThumbnailURL string
	EditStatus         string
	DraftTitle         *string
	SessionCreatedAt   time.Time
	SessionUpdatedAt   time.Time
}
