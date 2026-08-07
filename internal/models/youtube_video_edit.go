package models

import "time"

// YouTubeVideoEdit persists an InstaEdit-managed thumbnail editing
// session for a specific YouTube video. It links the InstaEdit
// workspace/account, the YouTube video, and the Velox InstaEditor
// project used to produce the thumbnail.
//
// ActualPrivacy and YouTubeSyncStatus are the YouTube-side
// projection of the publish outcome. They are stamped by the
// publish orchestrator right after the read-back videos.list call
// (P0#7) and updated again by the drift_reconciler when YouTube
// independently drifts (e.g. operator edited through YouTube Studio
// after our publish). Pointer-to-string for both because:
//   - the field can be unset (publish hasn't completed yet) —
//     `omitempty` on the JSON layer keeps the API contract clean;
//   - a NULL column scanned into Go string would lose the
//     "unset vs empty string" distinction (we use ” to mean
//     "operator erased the title", NULL to mean "publish hasn't
//     happened yet or read-back errored").
type YouTubeVideoEdit struct {
	ID                 string     `json:"id"`
	WorkspaceID        int64      `json:"workspace_id"`
	PlatformAccountID  int64      `json:"platform_account_id"`
	YouTubeVideoID     string     `json:"youtube_video_id"`
	VeloxProjectID     string     `json:"velox_project_id"`
	SourceThumbnailURL string     `json:"source_thumbnail_url,omitempty"`
	ThumbnailMediaID   *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy     string     `json:"desired_privacy"`
	PublishAt          *time.Time `json:"publish_at,omitempty"`
	// ActualPrivacy is what YouTube's videos.list reports when the
	// publish orchestrator reads back the published video's status.
	// It can diverge from DesiredPrivacy in two cases:
	//   1. Operator scheduled a private publish via the 'publish_at'
	//      field — YouTube initially honours DesiredPrivacy but flips
	//      the video to public at publish_at time; the value seen
	//      during read-back depends on whether the read-back fires
	//      before or after the schedule fires.
	//   2. Operator manually edited the privacy in YouTube Studio
	//      after our publish — the drift_reconciler will eventually
	//      re-stamp this on its sweep tick.
	//   Reconciliation converges ActualPrivacy toward DesiredPrivacy
	//   whenever the operator's intent remains Persist-At.
	ActualPrivacy *string `json:"actual_privacy,omitempty"`
	// YouTubeSyncStatus is the lifecycle marker the SPA uses to
	// colour the privacy badge (see the verdict's DTO projection).
	// Valid values (CHECK constraint enforced at the DB layer):
	//   - pending  : read-back hasn't happened yet OR errored
	//                transiently; the reconciler will retry.
	//   - confirmed: ActualPrivacy matches DesiredPrivacy at read-back.
	//   - drift    : ActualPrivacy diverges from DesiredPrivacy AND
	//                the publish is still terminal-published. The
	//                reconciler attempts convergence on the next sweep.
	//   - failed   : terminal publish failure (PublishThumbnail +
	//                read-back both errored). Operator replay needed.
	YouTubeSyncStatus *string   `json:"youtube_sync_status,omitempty"`
	Status            string    `json:"status"`
	LastError         string    `json:"last_error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	// Draft* fields are the InstaEditor auto-save persistence
	// (P2 — migration 073). They hold the operator's mid-edit
	// values for title/description/tags/default audio language /
	// translations/desired privacy between the moment they start
	// typing and the publish boundary. NULL = "no draft yet" (the
	// common case for a freshly-minted row from FindOrCreateEditableSession);
	// empty string = "operator cleared this field intentionally" (a real
	// state the SPA distinguishes). The SaveDraft endpoint mirrors these
	// fields from the request body verbatim; the publish endpoint reads
	// DesiredPrivacy / Tags / etc. as the source of truth at the time of
	// click, NOT the draft_*.mirror (the publish payload overrides the
	// draft on the publish side — Draft* is only persistence for "I closed
	// my laptop mid-edit and want to come back to the same form later").
	DraftTitle                *string                       `json:"draft_title,omitempty"`
	DraftDescription          *string                       `json:"draft_description,omitempty"`
	DraftTags                 []string                      `json:"draft_tags,omitempty"`
	DraftDefaultLanguage      *string                       `json:"draft_default_language,omitempty"`
	DraftDefaultAudioLanguage *string                       `json:"draft_default_audio_language,omitempty"`
	DraftTranslations         map[string]YouTubeTranslation `json:"draft_translations,omitempty"`
	DraftDesiredPrivacy       *string                       `json:"draft_desired_privacy,omitempty"`
	DraftUpdatedAt            *time.Time                    `json:"draft_updated_at,omitempty"`
	// DirtyFlag stamps the dashboard's "unsaved changes" card pill.
	// Set to TRUE by the SPA on form-change; set to FALSE in the same
	// SQL as draft_updated_at by SaveDraft (a successful 200). The
	// publish orchestrator does not touch it (its lifecycle is the
	// editor's lifecycle, not the publish's).
	DirtyFlag bool `json:"dirty_flag"`
}

// YouTubeVideoDetails is the narrow view of a YouTube video returned
// by the YouTubeOAuthService when validating a video before an editor
// session is created.
type YouTubeVideoDetails struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description,omitempty"`
	ChannelID    string     `json:"channel_id"`
	ThumbnailURL string     `json:"thumbnail_url,omitempty"`
	Privacy      string     `json:"privacy"`
	UploadStatus string     `json:"upload_status"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
}
