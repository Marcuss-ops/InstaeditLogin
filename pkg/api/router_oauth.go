package api

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"io"
	"time"
)

// YouTubeOAuthService is the narrow capability-subset of
// *services.YouTubeOAuthService that the 4-step
// /accounts/{id}/validate pipeline (introduced in Commit C2) needs.
// Defined inline in pkg/api to keep tests mockable and avoid pkg/api
// directly importing internal/services for the interface ONLY (the
// service struct itself is injected via WithYouTubeService at
// production wiring time and its exported method-results are
// referenced via the interface below).
//
// The 4 steps map 1:1 onto the four interface methods:
//   - RefreshOAuthToken      → STEP 1 (refresh-grant via vault.Renew)
//   - GetTokenInfo          → STEP 2 (introspect access token + scope)
//   - ValidateChannelBinding → STEP 3 (paginated channels.list bind)
//   - CanaryUpload          → STEP 4 (optional private video + bind-reconcile)
//   - ClientID              → STEP 2 aud check (aud must equal the OAuth client
//     that issued the grant — guards against
//     Production-vs-Testing token drift)
//
// YouTubeRevoker is the provider capability used by the account disconnect
// flow. The token argument must be the encrypted-vault-decoded refresh token;
// implementations must never log it or include it in returned errors.
type YouTubeRevoker interface {
	Revoke(ctx context.Context, token string) error
}

// RefreshTokenReader is the narrow optional vault capability needed to revoke
// a provider grant remotely. CredentialVault implements it; legacy test vaults
// may omit it and are used only by non-YouTube account flows.
type RefreshTokenReader interface {
	GetRefreshToken(ctx context.Context, platformAccountID int64) (string, error)
}

// RefreshTokenTxReader reads and decrypts a grant's refresh token using the
// transaction that already locked the grant. This keeps remote revocation
// coordinated with the local grant cleanup and prevents token rotation races.
type RefreshTokenTxReader interface {
	GetRefreshTokenForOAuthConnectionTx(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) (string, error)
}

// YouTubeDisconnectStore performs the local, grant-scoped disconnect. The
// implementation resolves oauth_connection_id from the account and executes
// token deletion, grant status, channel status, and resumable-session cleanup
// in one PostgreSQL transaction.
type YouTubeDisconnectStore interface {
	DisconnectYouTubeOAuthConnection(ctx context.Context, platformAccountID int64) error
}

type YouTubeOAuthService interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
	GetTokenInfo(ctx context.Context, accessToken string) (*services.YouTubeTokenInfo, error)
	ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error
	CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error)
	FetchEarnings(ctx context.Context, accessToken, channelID string, days int) ([]repository.AccountMetricPoint, error)
	ClientID() string
	// GetYouTubeVideo validates that a video exists on the connected
	// YouTube channel and returns a narrow summary of its metadata.
	GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error)
	// ListEditableVideos (P0 group videos endpoint) returns one page
	// of processed private/unlisted videos belonging to channelID.
	// pageToken="" starts from the first page; subsequent pages are
	// fetched with the NextPageToken from the previous response. The
	// service-level filter (privacy != public AND uploadStatus =
	// processed) already filters out the long tail of public/
	// uploading/deleted rows the editor flow rejects at create time.
	ListEditableVideos(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error)
	// SetThumbnail uploads a JPEG/PNG image to YouTube and applies it
	// as the custom thumbnail for the given video. The caller must
	// supply a valid access token (retrieved from the vault).
	SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error
	// UpdateVideoPrivacy changes the privacy status (and optionally the
	// snippet title/description) of an existing YouTube video via
	// videos.update. For scheduled publishing pass a future publishAt and
	// privacyStatus="private".
	UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error
	// PublishThumbnail uploads a thumbnail to YouTube and updates the
	// video privacy + snippet in a SINGLE videos.update(part=snippet,status)
	// call. Title / Description are still supported but moved into the
	// YouTubePublishOptions struct so the signature doesn't grow
	// unboundedly as more snippet fields (tags / localizations / default
	// languages) are added.
	//
	// Retries transient failures internally and returns the public
	// YouTube URL on success.
	PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error)
	// UpsertLocalizations sets (or replaces) one per-language
	// localization entry on a YouTube video via
	// videos.update(part=localizations). YouTube expects a single
	// language per call; the orchestrator loops over the
	// Translations map calling this once per entry after the
	// snippet+status update succeeds.
	//
	// The lang argument is a BCP-47 code (e.g. "en", "it", "pt-BR");
	// the orchestrator validates against the YouTubePublishOptions
	// sanity bounds before invoking the call so quota isn't burned
	// on a guaranteed-4xx response.
	UpsertLocalizations(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error
	// UpdateVideoMetadata applies a PARTIAL snippet patch
	// (title / description / categoryId) to an existing video. The
	// implementation reads the current canonical snippet first and
	// preserves every field the patch omits (tags, default languages)
	// — videos.update replaces the snippet, it does not merge.
	// expectedChannelID, when non-empty, gates the update to videos
	// owned by that channel. Returns the merged snippet projection.
	UpdateVideoMetadata(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error)
	// ListVideoCategories returns the YouTube videoCategories.list
	// projection for a region (empty regionCode = global default). The
	// endpoint is NOT channel-scoped, so any valid OAuth token of a
	// connected account serves the list.
	ListVideoCategories(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error)
}
