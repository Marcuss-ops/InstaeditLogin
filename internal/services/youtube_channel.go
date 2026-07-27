package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
func (s *YouTubeOAuthService) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacyFilter string) (*models.AccountContentPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Step 1: Get the uploads playlist ID for this channel.
	uploadsPlaylist, err := s.getUploadsPlaylistID(ctx, accessToken, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("get uploads playlist: %w", err)
	}

	// No privacy filter: preserve the original single-page behaviour.
	if privacyFilter == "" {
		return s.listUnfilteredAccountContent(ctx, accessToken, uploadsPlaylist, cursor, limit)
	}

	// Filtered path: walk playlist pages until we collect enough private
	// videos, run out of pages, or hit a safety cap. We use maxResults=50
	// per page to minimise round-trips and pass the cursor through so
	// load-more continues from the next YouTube page.
	const maxPages = 10
	var (
		items     []models.AccountContentItem
		pageToken = cursor
		pages     int
	)
	for pages < maxPages {
		videoIDs, nextPage, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, pageToken, 50)
		if err != nil {
			return nil, fmt.Errorf("list playlist items: %w", err)
		}
		if len(videoIDs) == 0 {
			return &models.AccountContentPage{Items: items}, nil
		}

		details, err := s.getVideoDetails(ctx, accessToken, videoIDs)
		if err != nil {
			return nil, fmt.Errorf("get video details: %w", err)
		}

		for _, item := range details {
			if item.Privacy == privacyFilter {
				items = append(items, item)
			}
		}

		if len(items) >= limit {
			// We have enough items to satisfy the request. Return what we
			// collected plus the next YouTube page token so the client can
			// load more. We intentionally do NOT slice to exactly `limit`
			// because that would drop already-fetched private videos from
			// the current chunk, causing data loss on the next page.
			return &models.AccountContentPage{Items: items, NextCursor: nextPage}, nil
		}

		if nextPage == "" {
			return &models.AccountContentPage{Items: items}, nil
		}

		pageToken = nextPage
		pages++
	}

	return &models.AccountContentPage{Items: items, NextCursor: pageToken}, nil
}

func (s *YouTubeOAuthService) listUnfilteredAccountContent(ctx context.Context, accessToken, uploadsPlaylist, cursor string, limit int) (*models.AccountContentPage, error) {
	// Step 1: List recent items from the uploads playlist.
	videoIDs, nextPageToken, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}

	if len(videoIDs) == 0 {
		return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
	}

	// Step 2: Fetch video details (snippet, statistics, contentDetails, status).
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
			Duration:     v.ContentDetails.Duration,
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

// SetThumbnail uploads a JPEG/PNG image to YouTube and applies it as the
// custom thumbnail for the given video. The caller must supply a valid
// access token (retrieved from the vault) and the image must meet the
// YouTube Data API constraints (JPEG or PNG, max 2 MB).
//
// Error handling:
//   - 200 OK → success, nil error.
//   - 401 Unauthorized → the access token is invalid or expired.
//   - 403 Forbidden → the grant lacks permission or the channel is
//     ineligible for custom thumbnails.
//   - 404 Not Found → the video does not exist.
//   - 429 Too Many Requests → rate limited; the caller should retry.
//   - 5xx → transient server error; the caller should retry.
//
// The access token is never included in any returned error.
func (s *YouTubeOAuthService) SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error {
	if videoID == "" {
		return fmt.Errorf("youtube set thumbnail: empty video id")
	}
	if size <= 0 {
		return fmt.Errorf("youtube set thumbnail: invalid image size")
	}
	const maxThumbnailBytes = 2 * 1024 * 1024
	if size > maxThumbnailBytes {
		return fmt.Errorf("youtube set thumbnail: image exceeds 2 MB limit")
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return fmt.Errorf("youtube set thumbnail: unsupported content type %q (only image/jpeg and image/png allowed)", mimeType)
	}

	params := url.Values{}
	params.Set("videoId", videoID)
	reqURL := "https://www.googleapis.com/upload/youtube/v3/thumbnails/set?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return fmt.Errorf("youtube set thumbnail: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", mimeType)
	req.ContentLength = size

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube set thumbnail: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		// Drain the body so the underlying connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube set thumbnail: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube set thumbnail: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube set thumbnail: video not found (status 404)"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube set thumbnail: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube set thumbnail: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube set thumbnail: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// GetYouTubeVideo fetches the details for a single YouTube video by id
// and returns the narrow subset of fields the InstaEdit BFF needs to
// validate a video before creating a thumbnail editor session. It
// returns an error when the video does not exist or the upstream call
// fails.
// UpdateVideoPrivacy updates the privacy status (and optionally the
// snippet title and/or description) of an existing YouTube video via
// videos.update. For immediate publication set privacy to "public" or
// "unlisted" and leave publishAt nil. For scheduled publication set
// privacy to "private" and provide a future publishAt timestamp; YouTube
// will make the video public at that time. Non-empty title/description
// are included in the snippet part and sent with part=snippet,status.
// PublishThumbnail uploads a custom thumbnail to YouTube, then
// updates the video privacy status (and, when supplied, the snippet
// title + description) in a single videos.update(part=snippet,status)
// call. Retries transient failures internally (3 retries with
// linear-backoff reset via doWithRetry).
//
// Returns the public YouTube watch URL on success.
func (s *YouTubeOAuthService) PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
	const maxThumbnailBytes = 2 * 1024 * 1024
	if len(thumbnailData) == 0 {
		return "", fmt.Errorf("youtube publish thumbnail: empty thumbnail data")
	}
	if len(thumbnailData) > maxThumbnailBytes {
		return "", fmt.Errorf("youtube publish thumbnail: thumbnail exceeds 2 MB limit")
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return "", fmt.Errorf("youtube publish thumbnail: unsupported content type %q", mimeType)
	}

	// 1. Upload thumbnail with retry.
	setErr := doWithRetry(ctx, 3, time.Second, func() error {
		return s.SetThumbnail(ctx, accessToken, videoID, mimeType, bytes.NewReader(thumbnailData), int64(len(thumbnailData)))
	})
	if setErr != nil {
		return "", fmt.Errorf("youtube publish thumbnail: set thumbnail failed: %w", setErr)
	}

	// 2. Update video metadata + privacy with retry. When opts carries
	//    the P1 extensions (tags / default language / default audio
	//    language) we update them together via the extended-snippet
	//    payload; otherwise we delegate to the byte-identical
	//    UpdateVideoPrivacy path used by every other caller (job
	//    workers, the publish reconciler, …) so the pre-extension
	//    behaviour for callers that only supply title/description is
	//    preserved byte-for-byte.
	updateErr := doWithRetry(ctx, 3, time.Second, func() error {
		if hasExtendedSnippet(opts) {
			return s.updateVideoWithExtendedSnippet(ctx, accessToken, videoID, privacyStatus, publishAt, opts)
		}
		return s.UpdateVideoPrivacy(ctx, accessToken, videoID, privacyStatus, publishAt, opts.Title, opts.Description)
	})
	if updateErr != nil {
		return "", fmt.Errorf("youtube publish thumbnail: update video failed: %w", updateErr)
	}

	return "https://www.youtube.com/watch?v=" + videoID, nil
}

// hasExtendedSnippet reports whether opts carries any of the
// P1 snippet extensions beyond plain title/description. The
// orchestrator uses this gate to decide whether to fold tags +
// default languages into the single videos.update call or to
// delegate to the pre-extension UpdateVideoPrivacy path.
//
// Localizations (Translations) are NOT included here — they are
// applied via separate UpsertLocalizations calls (one per
// language) AFTER the snippet+status update succeeds.
func hasExtendedSnippet(opts models.YouTubePublishOptions) bool {
	return len(opts.Tags) > 0 || opts.DefaultLanguage != "" || opts.DefaultAudioLanguage != ""
}

// updateVideoWithExtendedSnippet issues a single
// videos.update(part=snippet,status) call carrying:
//   - status.privacyStatus + (optional) status.publishAt
//   - snippet.title + snippet.description (when supplied)
//   - snippet.tags[] (when supplied)
//   - snippet.defaultLanguage + snippet.defaultAudioLanguage
//     (when supplied)
//
// YouTube charges 1600 quota units per videos.update call, so
// folding tags + default languages into the SAME call as the
// privacy change saves one round-trip vs. running a separate
// snippet-only update after the status update. The payload
// shape mirrors the existing UpdateVideoPrivacy path so a
// downstream reader that already parses that payload can accept
// the new keys without a refactor.
//
// Returns the same typed errors as UpdateVideoPrivacy
// (YouTubeAPIError, snippet-validation, etc.) so callers'
// failure-path handling stays unchanged.
func (s *YouTubeOAuthService) updateVideoWithExtendedSnippet(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) error {
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	switch privacyStatus {
	case "public", "unlisted", "private":
		// ok
	default:
		return fmt.Errorf("youtube update video: invalid privacy status %q", privacyStatus)
	}
	if err := ValidateYouTubeSnippet(opts.Title, opts.Description); err != nil {
		return fmt.Errorf("youtube update video: %w", err)
	}

	// status object — always present.
	status := map[string]interface{}{
		"privacyStatus": privacyStatus,
	}
	if publishAt != nil && !publishAt.IsZero() {
		if privacyStatus != "private" {
			return fmt.Errorf("youtube update video: publishAt requires privacyStatus=private")
		}
		status["publishAt"] = publishAt.UTC().Format(time.RFC3339)
	}

	// snippet object — only added when at least one snippet field
	// is non-empty. Without this gate YouTube would 4xx on an
	// empty snippet.
	snippet := make(map[string]interface{})
	if opts.Title != "" {
		snippet["title"] = opts.Title
	}
	if opts.Description != "" {
		snippet["description"] = opts.Description
	}
	if len(opts.Tags) > 0 {
		// Defensive copy so a calling test that re-uses opts
		// after the call still sees consistent state.
		tagsCopy := make([]string, len(opts.Tags))
		copy(tagsCopy, opts.Tags)
		snippet["tags"] = tagsCopy
	}
	if opts.DefaultLanguage != "" {
		snippet["defaultLanguage"] = opts.DefaultLanguage
	}
	if opts.DefaultAudioLanguage != "" {
		snippet["defaultAudioLanguage"] = opts.DefaultAudioLanguage
	}

	payload := map[string]interface{}{
		"id":     videoID,
		"status": status,
	}
	if len(snippet) > 0 {
		payload["snippet"] = snippet
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube update video: marshal metadata: %w", err)
	}

	parts := "status"
	if len(snippet) > 0 {
		parts = "snippet,status"
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=" + parts
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("youtube update video: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube update video: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube update video: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube update video: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube update video: video not found (status 404)"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube update video: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube update video: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube update video: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// UpsertLocalizations sets (or replaces) a single per-language
// localization on a YouTube video via videos.update(part=localizations).
// YouTube expects one language per call (the body is shaped as
// {id, localizations: {<lang>: {title, description}}}); the
// orchestrator loops over opts.Translations calling this once per
// language AFTER the snippet+status update succeeds.
//
// Retries transient failures (3x) via doWithRetry; permanent
// errors propagate. lang is validated upstream by
// YouTubePublishOptions.Validate so this method does not re-check
// the BCP-47 shape — a malformed lang is the orchestrator's bug,
// not the API call's.
func (s *YouTubeOAuthService) UpsertLocalizations(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
	if err := ValidateYouTubeSnippet(tr.Title, tr.Description); err != nil {
		return fmt.Errorf("youtube upsert localizations %s: %w", lang, err)
	}
	payload := map[string]interface{}{
		"id": videoID,
		"localizations": map[string]interface{}{
			lang: map[string]interface{}{
				"title":       tr.Title,
				"description": tr.Description,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube upsert localizations %s: marshal payload: %w", lang, err)
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=localizations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("youtube upsert localizations %s: create request: %w", lang, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	return doWithRetry(ctx, 3, time.Second, func() error {
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube upsert localizations %s: request: %v", lang, err)}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}
		rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: fmt.Sprintf("youtube upsert localizations %s: unauthorized (status 401)", lang)}
		case resp.StatusCode == http.StatusForbidden:
			return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: fmt.Sprintf("youtube upsert localizations %s: forbidden (status 403)", lang)}
		case resp.StatusCode == http.StatusNotFound:
			return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: fmt.Sprintf("youtube upsert localizations %s: video not found (status 404)", lang)}
		case resp.StatusCode == http.StatusTooManyRequests:
			return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: fmt.Sprintf("youtube upsert localizations %s: rate limited (status 429)", lang)}
		case resp.StatusCode >= 500:
			return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube upsert localizations %s: server error (status %d)", lang, resp.StatusCode)}
		default:
			return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube upsert localizations %s: unexpected status %d: %s", lang, resp.StatusCode, string(rbody))}
		}
	})
}

func (s *YouTubeOAuthService) UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error {
	if videoID == "" {
		return fmt.Errorf("youtube update video: empty video id")
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	switch privacyStatus {
	case "public", "unlisted", "private":
		// ok
	default:
		return fmt.Errorf("youtube update video: invalid privacy status %q", privacyStatus)
	}

	if err := ValidateYouTubeSnippet(title, description); err != nil {
		return fmt.Errorf("youtube update video: %w", err)
	}

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	status := map[string]string{
		"privacyStatus": privacyStatus,
	}
	if publishAt != nil && !publishAt.IsZero() {
		if privacyStatus != "private" {
			return fmt.Errorf("youtube update video: publishAt requires privacyStatus=private")
		}
		status["publishAt"] = publishAt.UTC().Format(time.RFC3339)
	}

	snippet := make(map[string]string)
	if title != "" {
		snippet["title"] = title
	}
	if description != "" {
		snippet["description"] = description
	}

	payload := map[string]interface{}{
		"id":     videoID,
		"status": status,
	}
	if len(snippet) > 0 {
		payload["snippet"] = snippet
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube update video: marshal metadata: %w", err)
	}

	parts := "status"
	if len(snippet) > 0 {
		parts = "snippet,status"
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=" + parts
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("youtube update video: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube update video: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube update video: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube update video: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube update video: video not found (status 404)"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube update video: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube update video: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube update video: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// ValidateYouTubeSnippet returns an error if the supplied title or
// description exceed YouTube's documented snippet limits (title 100
// characters, description 5000 characters). It counts runes, not
// bytes, and trims surrounding whitespace before measuring.
func ValidateYouTubeSnippet(title, description string) error {
	const maxTitleLen = 100
	const maxDescriptionLen = 5000
	if utf8.RuneCountInString(strings.TrimSpace(title)) > maxTitleLen {
		return fmt.Errorf("title exceeds %d characters", maxTitleLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(description)) > maxDescriptionLen {
		return fmt.Errorf("description exceeds %d characters", maxDescriptionLen)
	}
	return nil
}

// YouTubeAPIError carries the HTTP status code and a machine-readable
// category for a YouTube Data API failure. It is returned by low-level
// YouTube service methods so callers can decide whether the error is
// transient and worth retrying.
type YouTubeAPIError struct {
	StatusCode int
	Category   string
	Message    string
}

// Error implements the error interface.
func (e *YouTubeAPIError) Error() string {
	return e.Message
}

// Transient reports whether the error is likely to resolve on retry.
// Network-level failures (category "network") and explicit rate-limit / 5xx
// HTTP responses are considered transient and safe to retry.
func (e *YouTubeAPIError) Transient() bool {
	if e.Category == "network" {
		return true
	}
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if e.StatusCode >= 500 {
		return true
	}
	return false
}

// retryableError reports whether err is a transient error that should be
// retried. It returns true for YouTubeAPIError marked as transient (429,
// 5xx, network failures) and false for context cancellation/deadline errors
// and any other non-transient errors.
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *YouTubeAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Transient()
	}
	// Unknown errors are not retried by default to avoid masking application
	// bugs or repeatedly failing deterministic pre-conditions.
	return false
}

// doWithRetry runs fn up to maxAttempts with exponential backoff and
// bounded jitter. It only retries when fn returns a retryable error and
// honors context cancellation before each attempt and between attempts.
//
// The delay for attempt i is min(cap, base * 2^i) and then jittered
// between 50% and 100% of that value to prevent synchronized retries
// (thundering herd) when many goroutines retry simultaneously.
func doWithRetry(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := fn(); err == nil {
			return nil
		} else if !retryableError(err) {
			return err
		} else {
			lastErr = err
		}
		if attempt < maxAttempts-1 {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			// Add bounded jitter to avoid thundering herd. The wait time is
			// uniformly distributed in [delay/2, delay].
			if delay > 0 {
				half := delay / 2
				jitter := time.Duration(rand.Int63n(int64(half) + 1))
				delay = half + jitter
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	if lastErr == nil {
		return fmt.Errorf("exceeded retry attempts")
	}
	return lastErr
}

func (s *YouTubeOAuthService) GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
	if videoID == "" {
		return nil, fmt.Errorf("youtube video details: empty video id")
	}

	params := url.Values{}
	params.Set("part", "snippet,status,contentDetails")
	params.Set("id", videoID)

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube video details: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube video details: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube video details: videos.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("youtube video details: decode: %w", err)
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube video details: video %s not found", videoID)
	}

	v := result.Items[0]
	return &models.YouTubeVideoDetails{
		ID:           v.ID,
		Title:        v.Snippet.Title,
		ChannelID:    v.Snippet.ChannelID,
		ThumbnailURL: youtubeBestThumbnail(v.Snippet.Thumbnails),
		Privacy:      v.Status.PrivacyStatus,
		UploadStatus: v.Status.UploadStatus,
	}, nil
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
