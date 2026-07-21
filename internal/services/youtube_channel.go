package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ValidateChannelBinding implements services.YouTubeChannelBinder.
// It calls channels.list?part=id&mine=true with a fresh access token
// (the worker has already refreshed via the vault before calling) and
// verifies the returned channel id set includes expectedChannelID.
//
// A single Google Account OAuth grant can manage up to ~100 YouTube
// channels (server-side max today); channels.list?maxResults=50
// silently truncates at 50. We therefore follow nextPageToken until
// empty, with a hard upper bound (see maxChannelsPerGrant below) as a
// safety net against (a) a hostile / misconfigured grant reporting
// unbounded channel sets, (b) a future YouTube increase of the
// per-grant cap leaving the operator with N>cap channels visible.
//
// Behaviour matrix:
//   - 200 OK across N pages (1 <= N) with the expected channel id in
//     ANY page → nil. N is bounded by maxChannelsPerGrant (200 today).
//   - 200 OK across all pages, NO match →
//     fmt.Errorf("%w: expected %q, grant-bound channels=%v ...)",
//     ErrYouTubeChannelMismatch, expectedChannelID, <full list>)
//     — a SINGLE call site visibility problem where any channel-list
//     page contains the expected id materially helps the operator
//     diagnose the drift.
//   - 200 OK across all pages, 0 unique channels collected → same
//     sentinel as 'NO match' but with 'grant has 0 channels' diagnostic
//     (the grant lost all bindings — recoverable only via a fresh
//     OAuth dance).
//   - maxChannelsPerGrant safety cap hit BEFORE exhausting nextPageTokens
//     → ErrYouTubeChannelMismatch with 'safety cap reached' diagnostic.
//     Triggered when a manager is bound to >200 channels — today this
//     is impossible (server max ~100 per grant) but the cap guards
//     against future API surface changes.
//   - Non-200 / network / decode error at any page → plain wrapped
//     error (NOT wrapped in ErrYouTubeChannelMismatch) so the worker
//     treats it as transient. The transient contract is unchanged
//     from the pre-pagination single-GET path.
//
// ErrChannelListSafetyCap is the typed error returned by
// ValidateChannelBinding when the loop hit the maxChannelsPerGrant
// (200) safety cap BEFORE nextPageToken exhaustion. The struct fields
// are extracted by tests + cross-package callers via errors.As:
//
//	var cap *ErrChannelListSafetyCap
//	if errors.As(err, &cap) { ... cap.Expected, cap.Cap ... }
//
// Error() returns a string that preserves the original
// ErrYouTubeChannelMismatch prefix so existing log-spelunking on the
// message substring keeps working. Unwrap() returns
// ErrYouTubeChannelMismatch so errors.Is(err, ErrYouTubeChannelMismatch)
// is still green WITHOUT callers needing to know the typed-struct
// shape — the sentinel stays the canonical "channel binding failed"
// signal, and the typed-struct is a refinement that carries the
// "how" (cap-hit) diagnostic.
//
// Distinct from the exhaustion-path mismatch (which still wraps the
// plain ErrYouTubeChannelMismatch sentinel — distinguishable via a
// negative errors.As) and from the BindGrantToChannel mismatch
// (separate production path, OUT OF SCOPE for this refactor).
type ErrChannelListSafetyCap struct {
	// Expected is the channel id the caller asked the loop to find.
	Expected string
	// Cap is the maxChannelsPerGrant value AT THE TIME OF THE HIT.
	// Surfaced as a structured field so tests can assert against
	// it without grepping for the literal "200" in error.Error().
	Cap int
}

// Error returns the canonical human-readable form. The redundant
// "%v: ..." prefix re-emits ErrYouTubeChannelMismatch's text so the
// resulting message reads identically to the pre-refactor
// fmt.Errorf("%w: ...", ErrYouTubeChannelMismatch, ...). This keeps
// any operator-side log-grep recipe (the "must mention safety cap
// reached" diagnostic the old strings.Contains was enforcing)
// intact while letting go-side consumers switch on the typed
// struct. Pinning the format here means a future message-shape
// change is one-line and the test assertions don't need to update
// in lockstep.
func (e *ErrChannelListSafetyCap) Error() string {
	return fmt.Sprintf("%v: expected %q not found in first %d unique channel ids (safety cap reached)",
		ErrYouTubeChannelMismatch, e.Expected, e.Cap)
}

// Unwrap exposes ErrYouTubeChannelMismatch for both errors.Is and
// errors.As chains. Callers that DON'T care about the typed-struct
// refinement keep working with errors.Is(err, ErrYouTubeChannelMismatch);
// callers that DO care can do errors.As(err, &safetyCap) to recover
// the structured fields.
func (e *ErrChannelListSafetyCap) Unwrap() error {
	return ErrYouTubeChannelMismatch
}

// ErrChannelMismatchMsg formats the canonical operator-facing
// diagnostic for the ValidateChannelBinding EXHAUSTION path (and
// any future non-cap mismatch path). Centralising here means a
// future message-shape change is one-line and tests can pin ONE
// canonical rendering via the helper call rather than reaching
// into the wrapped error string. Currently returned only on the
// exhaustion path (lines below); the 0-channels / safety-cap paths
// use either the typed struct above or the existing inline format,
// and stay that way intentionally (each carries distinct semantics
// the operator needs to tell apart).
func ErrChannelMismatchMsg(expected string, bound []string) string {
	return fmt.Sprintf("expected %q, grant-bound channels=%v", expected, bound)
}

// The method is a paginated GET loop; it does NOT re-refresh the
// access token to avoid double-quota usage (the publish worker
// already refreshed in step 5 of publishTarget). The token MUST
// therefore be a fresh bearer token; OAuth-only access tokens (no
// refresh) are not supported on this path — they're an immediate 401
// and the worker should treat them as reauth-required via the
// existing token-refresh error path.
func (s *YouTubeOAuthService) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	if expectedChannelID == "" {
		return fmt.Errorf("youtube channel binding check: empty expected channel id")
	}

	// Safety cap. Server-side per-grant max is ~100 today; 200
	// leaves headroom for a future API change + a buffer before any
	// runaway loop would hit the underlying quota. Hitting the cap
	// also tells the operator their distribution planning needs to
	// change (see docs/OAUTH-PRODUCTION.md 'channels.list pagination
	// + 40-50 channels per manager').
	const maxChannelsPerGrant = 200

	var (
		pageToken string
		totalIDs  []string
		seen      = make(map[string]struct{}, 64)
	)
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("youtube channel binding: cancelled by %w at page %d", err, page)
		}

		params := url.Values{}
		params.Set("part", "id")
		params.Set("mine", "true")
		params.Set("maxResults", "50")
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://www.googleapis.com/youtube/v3/channels?"+params.Encode(), nil)
		if err != nil {
			return fmt.Errorf("youtube channel binding: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("youtube channel binding: channels.list request: %w", err)
		}

		var result youtubeChannelsResponse
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return fmt.Errorf("youtube channel binding: channels.list returned %d: %s", resp.StatusCode, string(body))
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&result); jerr != nil {
			resp.Body.Close()
			return fmt.Errorf("youtube channel binding: decode channels.list: %w", jerr)
		}
		resp.Body.Close()

		// Accumulate unique IDs in arrival order. Google does NOT
		// guarantee distinct channels across page boundaries, so
		// dedupe here: (a) the final mismatch count matches what
		// the operator will see in the dashboard, and (b) the
		// safety cap below counts UNIQUE channels rather than
		// request rows.
		for _, ch := range result.Items {
			if ch.ID == "" {
				continue
			}
			if _, dup := seen[ch.ID]; dup {
				continue
			}
			seen[ch.ID] = struct{}{}
			totalIDs = append(totalIDs, ch.ID)
			if ch.ID == expectedChannelID {
				return nil // bound to expected channel — proceed with upload
			}
		}

		if len(totalIDs) >= maxChannelsPerGrant {
			// Safety cap reached BEFORE nextPageToken exhausted.
			// Treat as mismatch because we cannot prove the expected
			// is NOT in the truncated set. This is a structural
			// escape valve; operators with >200 channels per grant
			// will be flagged to re-distribute or to raise the cap
			// via docs/OAUTH-PRODUCTION.md.
			//
			// Returns the typed struct (NOT a fmt.Errorf wrap) so
			// errors.As(err, &safetyCap) succeeds AND
			// errors.Is(err, ErrYouTubeChannelMismatch) still works
			// via Unwrap(). The error message is preserved 1:1
			// against the pre-refactor shape (same prefix, same
			// substrings the operator log-grep recipe cared about).
			return &ErrChannelListSafetyCap{
				Expected: expectedChannelID,
				Cap:      maxChannelsPerGrant,
			}
		}

		if result.NextPageToken == "" {
			break // API returned the final page; pagination complete
		}
		pageToken = result.NextPageToken
	}

	if len(totalIDs) == 0 {
		return fmt.Errorf("%w: expected %q, grant has 0 channels",
			ErrYouTubeChannelMismatch, expectedChannelID)
	}
	// Exhaustion path: pages walked to completion, expectedChannelID
	// not found in any. Wraps the canonical ErrYouTubeChannelMismatch
	// sentinel AND formats the operator-facing diagnostic via the
	// ErrChannelMismatchMsg helper so tests pin ONE canonical
	// rendering. Distinguishable from the safety-cap path (which
	// returns the *ErrChannelListSafetyCap typed struct above): an
	// errors.As(err, &safetyCap) check on this error returns false,
	// which ExhaustedMismatch_ReturnsMismatch asserts explicitly.
	return fmt.Errorf("%w: %s", ErrYouTubeChannelMismatch,
		ErrChannelMismatchMsg(expectedChannelID, totalIDs))
}

// VerifyChannelIdentity (Task 2/10) is the REUSABLE pre-action
// channel-bound guard. It is the public alias for
// YouTubeChannelBinder.ValidateChannelBinding — the canonical
// pre-tx (services.ChannelAuthorizationService.AuthorizeChannel) +
// pre-upload (internal/worker.PublishWorker.publishTarget)
// pre-flight check. Both call sites need exactly the same logic
// (channels.list(mine=true) on the just-refreshed access token,
// compare against the platform_account.platform_user_id) so the
// canonical implementation lives here, behind a typed helper, and
// every consumer delegates. The user's spec asked for a guard named
// verifyChannelIdentity(token, expectedChannelID); the binder
// argument is the narrow YouTube provider interface so tests can
// pass an in-memory fake (no real HTTP round-trip).
//
// Return contract mirrors YouTubeOAuthService.ValidateChannelBinding:
//   - nil → grant is bound to expectedChannelID, proceed.
//   - error wrapping ErrYouTubeChannelMismatch → grant is NOT bound
//     to expectedChannelID. The HTTP layer maps this to 422 +
//     status='reauth_required'; the publish worker maps this to
//     post_target.status='blocked_auth' + platform_account.status=
//     'reauth_required'; neither path crosses the publish boundary.
//   - any other error → transient (network, 5xx, decode). Caller
//     MUST treat as transient (retry on next tick) and MUST NOT
//     flag reauth_required — would lock out the operator for a
//     transient blip.
//
// Pass binder=nil as a no-op (returns nil immediately). Useful in
// tests that don't want to wire a real YouTube provider and for any
// future non-YouTube provider that shouldn't run the YouTube-specific
// channels.list check (the existing provider path already filters
// `account.Platform == models.PlatformYouTube` upstream).
func VerifyChannelIdentity(ctx context.Context, binder YouTubeChannelBinder, accessToken, expectedChannelID string) error {
	if binder == nil {
		return nil
	}
	return binder.ValidateChannelBinding(ctx, accessToken, expectedChannelID)
}

// ErrYouTubeAmbiguousAuthorization is the canonical sentinel returned
// by BindGrantToChannel when channels.list(mine=true) reports >1
// channels for the authenticated Google account AND no
// expected_channel_id was supplied at login time. Co-exists with the
// same-text declaration in pkg/api/handlers.go (the HTTP layer keeps
// a local copy for its 409 Conflict mapping); both layers own their
// own discovery flow.
//
// Cross-references:
//   - pkg/api/routes_test.go::TestHandleCallback_YouTube_MultipleChannels_NoExpected_Conflict
//   - pkg/api/handlers.go::attachDiscoveredAccounts (YouTube branch
//   - 409 mapping)
var ErrYouTubeAmbiguousAuthorization = errors.New("youtube authorization is ambiguous: re-authorize with expected_channel_id")

// BindGrantToChannel consolidates the 1-OAuth-grant-per-1-channel
// policy at the provider level. It is the YouTube analogue of
// "validate before you store": the OAuth callback handler (and any
// future per-channel re-link flow) calls this to ensure the bearer
// token is saved EXACTLY ONCE — for the channel the operator
// verified — and is never cloned across the whole
// channels.list(mine=true) result set.
//
// Behaviour matrix:
//   - expectedChannelID == "" AND len(discovered) == 1 → returns
//     the single *DiscoveredAccount, nil error (canonical happy
//     path for one-Google-account-one-channel operators).
//   - expectedChannelID == "" AND len(discovered) != 1 → returns
//     nil, ErrYouTubeAmbiguousAuthorization wrapped with the
//     observed channel count. Cloning the token across N channels
//     is wrong: YouTube's OAuth grant is bound to whichever Brand
//     Account the operator selected at consent, and silently
//     fanning the token out is exactly the misroute Google warns
//     about for third-party apps that ignore Brand Account
//     selection.
//   - expectedChannelID set AND present in the discovery set →
//     returns the matching *DiscoveredAccount, nil error.
//   - expectedChannelID set AND NOT present → returns nil, an
//     error wrapping ErrYouTubeChannelMismatch (the operator
//     authenticated the wrong Google account, mistyped the id, or
//     imported a Brand Account ID that has since been moved /
//     removed).
//   - transient (5xx / network / decode error, or 0-channels
//     reported by DiscoverAccounts) → returns nil and the error
//     un-sentineled so the caller retries rather than
//     misclassifying a transient as a reauth-required state.
//
// This method does NOT save or clone the token. It is the SINGLE
// source of truth for the YouTube 1:1 policy: any consumer tempted
// to "for each channel save the token" should defer to this method,
// which guarantees at most one *DiscoveredAccount is returned.
func (s *YouTubeOAuthService) BindGrantToChannel(ctx context.Context, accessToken, expectedChannelID string) (*DiscoveredAccount, error) {
	accounts, err := s.DiscoverAccounts(ctx, accessToken, "")
	if err != nil {
		// Preserve the existing 0-channel / network behaviour:
		// DiscoverAccounts already produces a typed error ("the
		// authenticated Google account has no YouTube channel")
		// that callers rely on. Re-wrap so the bind call site is
		// unambiguous in logs but keep the sentinel-free shape so
		// transient errors aren't misclassified as reauth.
		return nil, fmt.Errorf("youtube bind: discover channels: %w", err)
	}

	if expectedChannelID != "" {
		for _, acc := range accounts {
			if acc.Profile.PlatformUserID == expectedChannelID {
				return acc, nil
			}
		}
		return nil, fmt.Errorf("%w: %q is not in channels.list(mine=true) result",
			ErrYouTubeChannelMismatch, expectedChannelID)
	}

	if len(accounts) != 1 {
		return nil, fmt.Errorf("%w: got %d channels, expected 1",
			ErrYouTubeAmbiguousAuthorization, len(accounts))
	}
	return accounts[0], nil
}

// DiscoverAccounts returns the YouTube channels owned by the authenticated
// Google account. Uses channels.list with mine=true to retrieve all channels
// linked to the OAuth grant. Each channel becomes a distinct PlatformAccount
// with the real YouTube channel ID (UC...) as PlatformUserID.
func (s *YouTubeOAuthService) DiscoverAccounts(ctx context.Context, accessToken, _ string) ([]*DiscoveredAccount, error) {
	const maxChannels = 500

	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status,brandingSettings")
	params.Set("mine", "true")
	params.Set("maxResults", "50")

	var allAccounts []*DiscoveredAccount
	var pageToken string

	for {
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		} else {
			params.Del("pageToken")
		}

		reqURL := "https://www.googleapis.com/youtube/v3/channels?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create youtube channel request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("youtube channel discovery: %w", err)
		}

		var result youtubeChannelsResponse
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return nil, fmt.Errorf("youtube channel discovery returned %d: %s", resp.StatusCode, string(body))
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode youtube channels: %w", err)
		}
		resp.Body.Close()

		for _, ch := range result.Items {
			allAccounts = append(allAccounts, &DiscoveredAccount{
				Profile: models.PlatformProfile{
					PlatformUserID: ch.ID,
					Username:       ch.Snippet.Title,
				},
				Metadata: models.Metadata{
					"description":             ch.Snippet.Description,
					"handle":                  ch.Snippet.CustomURL,
					"avatar_url":              youtubeBestThumbnail(ch.Snippet.Thumbnails),
					"uploads_playlist_id":     ch.ContentDetails.RelatedPlaylists.Uploads,
					"country":                 ch.Snippet.Country,
					"subscriber_count":        ch.Statistics.SubscriberCount,
					"hidden_subscriber_count": ch.Statistics.HiddenSubscriberCount,
					"video_count":             ch.Statistics.VideoCount,
					"view_count":              ch.Statistics.ViewCount,
				},
			})
		}

		if len(allAccounts) >= maxChannels {
			break
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	if len(allAccounts) == 0 {
		return nil, fmt.Errorf("the authenticated Google account has no YouTube channel")
	}

	return allAccounts, nil
}

// youtubeBestThumbnail selects the highest-resolution thumbnail from a
// YouTube thumbnail set, falling back to default → medium → high.
func youtubeBestThumbnail(thumbs *youtubeThumbnails) string {
	if thumbs == nil {
		return ""
	}
	if thumbs.Maxres != nil && thumbs.Maxres.URL != "" {
		return thumbs.Maxres.URL
	}
	if thumbs.Standard != nil && thumbs.Standard.URL != "" {
		return thumbs.Standard.URL
	}
	if thumbs.High != nil && thumbs.High.URL != "" {
		return thumbs.High.URL
	}
	if thumbs.Medium != nil && thumbs.Medium.URL != "" {
		return thumbs.Medium.URL
	}
	if thumbs.Default != nil && thumbs.Default.URL != "" {
		return thumbs.Default.URL
	}
	return ""
}

// GetAccountDetails fetches the current state of a YouTube channel via
// channels.list with id=<platformUserID>. Returns rich account details
// including statistics, branding, and upload playlist ID.
func (s *YouTubeOAuthService) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=snippet,statistics,contentDetails,status,brandingSettings" +
		"&id=" + url.QueryEscape(platformUserID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube channel details request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube channel details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube channel details returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube channel details: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube channel %s not found", platformUserID)
	}

	ch := result.Items[0]
	now := s.now()

	details := &models.AccountDetails{
		ResourceType: "channel",
		ExternalID:   ch.ID,
		DisplayName:  ch.Snippet.Title,
		Description:  ch.Snippet.Description,
		Handle:       ch.Snippet.CustomURL,
		AvatarURL:    youtubeBestThumbnail(ch.Snippet.Thumbnails),
		PublicURL:    "https://www.youtube.com/channel/" + ch.ID,
		FetchedAt:    now,
		Metrics: []models.AccountMetric{
			{
				Key:          "subscribers",
				Label:        "Subscribers",
				Value:        ch.Statistics.SubscriberCount,
				DisplayValue: formatCount(ch.Statistics.SubscriberCount),
			},
			{
				Key:          "views",
				Label:        "Views",
				Value:        ch.Statistics.ViewCount,
				DisplayValue: formatCount(ch.Statistics.ViewCount),
			},
			{
				Key:          "videos",
				Label:        "Videos",
				Value:        ch.Statistics.VideoCount,
				DisplayValue: formatCount(ch.Statistics.VideoCount),
			},
		},
	}

	// Banner URL from branding settings.
	if ch.BrandingSettings.Image != nil {
		details.BannerURL = ch.BrandingSettings.Image.BannerImageUrl
	}

	// Platform-specific properties.
	details.Properties = map[string]any{
		"country":                 ch.Snippet.Country,
		"uploads_playlist_id":     ch.ContentDetails.RelatedPlaylists.Uploads,
		"hidden_subscriber_count": ch.Statistics.HiddenSubscriberCount,
	}

	return details, nil
}

// ListAccountContent returns recent videos from a YouTube channel by
// reading the channel's uploads playlist and then fetching video
// details. Pagination is supported via the cursor (nextPageToken).
func (s *YouTubeOAuthService) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Step 1: Get the uploads playlist ID for this channel.
	uploadsPlaylist, err := s.getUploadsPlaylistID(ctx, accessToken, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("get uploads playlist: %w", err)
	}

	// Step 2: List recent items from the uploads playlist.
	videoIDs, nextPageToken, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}

	if len(videoIDs) == 0 {
		return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
	}

	// Step 3: Fetch video details (snippet, statistics, contentDetails, status).
	items, err := s.getVideoDetails(ctx, accessToken, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("get video details: %w", err)
	}

	return &models.AccountContentPage{
		Items:      items,
		NextCursor: nextPageToken,
	}, nil
}

func (s *YouTubeOAuthService) getUploadsPlaylistID(ctx context.Context, accessToken, channelID string) (string, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=contentDetails" +
		"&id=" + url.QueryEscape(channelID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("channels.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Items) == 0 {
		return "", fmt.Errorf("channel %s not found", channelID)
	}

	return result.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

func (s *YouTubeOAuthService) listPlaylistItems(ctx context.Context, accessToken, playlistID, pageToken string, maxResults int) (videoIDs []string, nextPage string, err error) {
	params := url.Values{}
	params.Set("part", "snippet,contentDetails")
	params.Set("playlistId", playlistID)
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}

	reqURL := "https://www.googleapis.com/youtube/v3/playlistItems?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, "", fmt.Errorf("playlistItems.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubePlaylistItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", err
	}

	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.ContentDetails.VideoID != "" {
			ids = append(ids, item.ContentDetails.VideoID)
		}
	}

	return ids, result.NextPageToken, nil
}

func (s *YouTubeOAuthService) getVideoDetails(ctx context.Context, accessToken string, videoIDs []string) ([]models.AccountContentItem, error) {
	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status")
	params.Set("id", strings.Join(videoIDs, ","))

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("videos.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	items := make([]models.AccountContentItem, 0, len(result.Items))
	for _, v := range result.Items {
		item := models.AccountContentItem{
			ExternalID:   v.ID,
			Title:        v.Snippet.Title,
			Description:  v.Snippet.Description,
			ThumbnailURL: youtubeBestThumbnail(v.Snippet.Thumbnails),
			PublicURL:    "https://www.youtube.com/watch?v=" + v.ID,
			Privacy:      v.Status.PrivacyStatus,
			Status:       v.Status.UploadStatus,
			Metrics: []models.AccountMetric{
				{
					Key:          "views",
					Label:        "Views",
					Value:        v.Statistics.ViewCount,
					DisplayValue: formatCount(v.Statistics.ViewCount),
				},
				{
					Key:          "likes",
					Label:        "Likes",
					Value:        v.Statistics.LikeCount,
					DisplayValue: formatCount(v.Statistics.LikeCount),
				},
				{
					Key:          "comments",
					Label:        "Comments",
					Value:        v.Statistics.CommentCount,
					DisplayValue: formatCount(v.Statistics.CommentCount),
				},
			},
		}

		if v.Snippet.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, v.Snippet.PublishedAt); err == nil {
				item.PublishedAt = &t
			}
		}

		item.Properties = map[string]any{
			"duration": v.ContentDetails.Duration,
		}

		items = append(items, item)
	}

	return items, nil
}

// formatCount returns a human-readable count string (e.g. "125K", "1.2M").
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// YouTube's Data API encodes statistics counters as JSON strings (for
// example, "viewCount": "123"), while fixtures and some compatible API
// implementations may emit JSON numbers. Accept both wire formats so a
// valid OAuth callback cannot fail while discovering the user's channel.
func decodeYouTubeCount(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return strconv.ParseInt(value, 10, 64)
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}
