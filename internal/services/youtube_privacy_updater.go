package services

import (
	"context"
	"time"
)

// YouTubePrivacyUpdater is the capability interface for services that
// can update a YouTube video's privacy status (and publishAt cursor)
// via videos.update. Called by PublishWorker in Phase 2 (the periodic
// publish loop) when Phase 1 (upload_worker.processPublishJob) has
// already uploaded the video privately via videos.insert — Phase 2
// reuses the existing youtube_video_id and videos.updates to the
// desired privacy rather than uploading a fresh copy.
//
// Companion to YouTubeChannelBinder; distinct because the two
// capabilities serve different sides of the publish flow:
//   - YouTubeChannelBinder: pre-publish READ-side check
//     (channels.list GET, gates reauth_required routing).
//   - YouTubePrivacyUpdater: post-upload WRITE-side operation
//     (videos.update POST, drives the actual privacy transition).
//
// Embedding NameProvider mirrors the YouTubeChannelBinder convention
// so the capability router lookup `router.Get(name).(YouTubePrivacyUpdater)`
// returns the same provider type the publish-worker already holds.
//
// Blocco #1 followup: this interface is the publish-side companion
// to the upload-phase YouTubeChannelUploader.OnUploaded hook
// (upload_worker.processPublishJob::uploadVideoAsPrivateForTarget).
// The publish worker discovers the Phase-1 row via the
// youtube_target_publications.youtube_video_id the upload worker
// stamped via MarkYouTubeUploaded.
type YouTubePrivacyUpdater interface {
	NameProvider

	// UpdateVideoPrivacy issues a videos.update(part=snippet,status)
	// to transition an existing YouTube video's privacyStatus to
	// the supplied value. The token MUST already be a fresh
	// post-renew access token (the worker has called vault.Renew
	// before this method is invoked) — do NOT re-refresh internally
	// (would double the OAuth quota).
	//
	// privacyStatus is one of "public" | "unlisted" | "private".
	// publishAt, when non-nil and in the future, instructs YouTube
	// to schedule the privacy transition (the existing
	// buildUploadMetadata + UpdateVideoPrivacy paths share the same
	// scheduling semantics — verify via
	// services/youtube_channel_test.go::TestUpdateVideoPrivacy_ScheduledPublishing).
	// title and description, when non-empty, are applied alongside the
	// privacy transition in the same videos.update(part=snippet,status)
	// call.
	//
	// Returns the same typed errors as YouTubeChannelBinder's
	// ValidateChannelBinding:
	//   - typed ErrYouTubeChannelMismatch on grant-channel drift
	//   - transient (5xx / network) errors wrapped plainly so the
	//     worker can branch on retryable vs auth-class
	UpdateVideoPrivacy(
		ctx context.Context,
		accessToken, videoID, privacyStatus string,
		publishAt *time.Time,
		title, description string,
	) error
}

// Compile-time assertion: YouTubeOAuthService implements
// YouTubePrivacyUpdater. The canonical assertion lives next to the
// other YouTube platform-capability assertions in
// services/youtube_oauth.go (clustered with YouTubeChannelBinder +
// YouTubeCanaryUploader so a signature drift surfaces there in one
// place). Declared: var _ YouTubePrivacyUpdater = (*YouTubeOAuthService)(nil)
