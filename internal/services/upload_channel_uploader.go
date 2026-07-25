package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UploadChannelUploader is the per-platform capability for uploading a
// video as 'private' to the target channel WITHOUT performing the publish
// phase (i.e., the resumable videos.insert upload + chunk PUT, returning
// the assigned provider-side video id, then stopping — NO follow-on
// videos.update). The publish phase (privacy=public + publishAt cursor)
// lives in publish_worker which owns the publish_at timeline.
//
// Blocco #1 P0 — immediate private upload right after the asset is ready,
// decoupled from publish_at. The YouTubeOAuthService implements this;
// the upload worker calls it via capRouter.Get(platform).(UploadChannelUploader)
// the same way it calls YouTubeChannelBinder / OAuthProvider today.
//
// UploadVideoAsPrivate returns:
//   - (videoID, nil): upload completed.
//   - ("", err): upload failed (transient OR structural). The caller
//     persists last_error on the youtube_target_publications row +
//     propagates the failure per policy (transient → retry via the
//     outer job's MarkRetry; structural mismatch → route to
//     blocked_auth via the channel-binding pre-flight BEFORE this
//     call).
//
// Refresh / resumable-session resume / cancel / reauth are the caller's
// concern; this method returns the LAST error from the resumable loop on
// failure so the caller can route per-policy.
type UploadChannelUploader interface {
	NameProvider
	// UploadVideoAsPrivate performs the resumable upload of mediaURL to
	// the channel controlled by accessToken, marking the video as
	// privacy='private' (NOT public) and returning the assigned
	// provider-side video id. The caller is responsible for token
	// refresh, channel-binding validation, and persistence of the
	// returned video id.
	UploadVideoAsPrivate(ctx context.Context, accessToken string, post *models.Post, videoURL string) (videoID string, err error)
}

// Compile-time assertion: services.YouTubeOAuthService must satisfy
// UploadChannelUploader. Caught by `go vet`, not at runtime. Mirror the
// pattern used for YouTubeChannelBinder (see youtube_oauth.go).
var _ UploadChannelUploader = (*YouTubeOAuthService)(nil)
