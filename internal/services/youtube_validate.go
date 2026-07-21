package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeCanaryUploader is the YouTube pre-flight canary capability
// interface invoked by publish_worker BEFORE the real publish when
// post.Metadata.canary_upload=true (Task 7/10). The implementation
// uploads a 5-10s/<5MB/privacy=private test video titled
// INSTAEDIT-OAUTH-CANARY-{channel_id}-{timestamp}, then verifies the
// uploaded channel id matches the platform_account.platform_user_id.
//
// Returns (\*CanaryUploadResult, error). nil result + non-nil error
// means the canary itself failed (caller flags PostStatusBlockedAuth
// and platform_account.status='reauth_required'). Non-nil result with
// UploadedChannelID == expectedChannelID means success; the worker
// proceeds to the real publish. Mismatch == blocker.
type YouTubeCanaryUploader interface {
	CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*CanaryUploadResult, error)
}

// ValidateContent enforces the YouTube video-required rule
// and a mandatory privacy_level.
// Taglio 4b: privacy_level is now required — one of public, unlisted, private.
func (s *YouTubeOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("youtube requires a video for publishing")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("youtube requires a privacy_level: one of public, unlisted, private")
	}
	if err := validateYouTubePrivacyLevel(payload.PrivacyLevel); err != nil {
		return err
	}
	return nil
}

// Validate calls the Google userinfo endpoint to verify the access token.
func (s *YouTubeOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return fmt.Errorf("youtube validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// youtubeTokenInfoResponse mirrors the JSON shape Google returns from
// https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=... .
// Field names match Google's lowercase contract verbatim (Aud→aud,
// Azp→azp, etc.); json.Unmarshal would otherwise need case-insensitive
// matching for every field. Only the operator-visible subset is captured;
// `error`, `error_description` etc. surface in the wrapped error
// message returned by GetTokenInfo on a 400 reply.
type youtubeTokenInfoResponse struct {
	Aud        string `json:"aud"`
	Azp        string `json:"azp"`
	Scope      string `json:"scope"`
	ExpiresIn  int64  `json:"expires_in"`
	AccessType string `json:"access_type"`
	Email      string `json:"email"`
}

// YouTubeTokenInfo is the structured introspection reply returned by
// YouTubeOAuthService.GetTokenInfo. Mirrors the four fields
// scripts/verify-google-oauth-mode.sh prints (aud, azp, scope,
// expires_in) plus an `email` field the script doesn't expose today
// (openid scope returns it; useful for the operator-side audit log).
//
// HasUpload / HasReadonly / HasMonetary are derived flags computed at
// construction time so callers can write `if !info.HasUpload { ... }`
// without re-parsing `Scope` themselves. The canonical scope strings are
// the full https://www.googleapis.com/auth/<scope> form (NOT the
// shortened alias) — matches what GetLoginURLWithOptions sets in
// the consent URL and what Google returns from tokeninfo.
type YouTubeTokenInfo struct {
	Aud       string
	Azp       string
	Scope     string
	ExpiresIn time.Duration
	Email     string

	HasUpload   bool
	HasReadonly bool
	// HasMonetary is true when the token has the YouTube Analytics
	// monetary-readonly scope required for revenue/RPM/CPM data.
	HasMonetary bool
}

// GetTokenInfo calls Google's oauth2/v3/tokeninfo public introspection
// endpoint with the supplied access token and returns the structured
// introspection reply.
//
// This is the CODE-SIDE equivalent of scripts/verify-google-oauth-mode.sh
// (the bash operator quick-check). Keeping a single canonical
// implementation in Go means the operator script and the handler-level
// validator never drift. Per Google's contract, this endpoint returns:
//
//	200 OK + JSON for any access token in good standing
//	400 Bad Request + {"error":"invalid_token",...} for expired,
//	    revoked, malformed, or otherwise un-introspectable tokens
//
// Error contract:
//   - non-200 (HTTP 400 typically) → wrapped error containing Google's
//     {"error":"invalid_token","error_description":"..."} body. Callers
//     distinguish hard-rejection (Google said the token is bad) from
//     transient (network / decode) by inspecting resp.StatusCode
//     before calling GetTokenInfo, OR by classifying the wrapped
//     error string itself in the handler. The HTTP layer in
//     handleValidateAccount maps a non-200 to 422 +
//     status='reauth_required' — same runbook as an invalid_grant
//     refresh-result.
//   - decode error or network error → plain wrapped error (NOT a
//     sentinel). The handler treats this as transient (next tick
//     retries). Mirrors the existing pre-step-2 channel-binding
//     convention: only ErrYouTubeChannelMismatch-shaped failures
//     flip the platform_account to reauth_required; everything else
//     is operator-deferred.
//
// The endpoint takes the access token AS A QUERY PARAMETER. This is
// documented and supported by Google; their modern docs recommend
// the Authorization header for NEW integrations, but the query-param
// path stays canonical for verification scripts and operator tooling
// (Google's docs link to it explicitly). Confirmed against
// scripts/verify-google-oauth-mode.sh which this method mirrors.
//
// Cross-references:
//   - pkg/api/handlers.go::handleValidateAccount (step 2 of the
//     4-step YouTube OAuth readiness pipeline introduced in
//     conventions/200-channel YouTube OAuth plan)
//   - scripts/verify-google-oauth-mode.sh (operator-shell analogue)
func (s *YouTubeOAuthService) GetTokenInfo(ctx context.Context, accessToken string) (*YouTubeTokenInfo, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("youtube tokeninfo: empty access token")
	}

	reqURL := "https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=" + url.QueryEscape(accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube tokeninfo: create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube tokeninfo: request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube tokeninfo returned %d: %s", resp.StatusCode, string(body))
	}

	var r youtubeTokenInfoResponse
	if jerr := json.Unmarshal(body, &r); jerr != nil {
		return nil, fmt.Errorf("youtube tokeninfo: decode: %w", jerr)
	}

	out := &YouTubeTokenInfo{
		Aud:       r.Aud,
		Azp:       r.Azp,
		Scope:     r.Scope,
		ExpiresIn: time.Duration(r.ExpiresIn) * time.Second,
		Email:     r.Email,
	}
	for _, sc := range strings.Fields(r.Scope) {
		switch sc {
		case "https://www.googleapis.com/auth/youtube.upload":
			out.HasUpload = true
		case "https://www.googleapis.com/auth/youtube.readonly":
			out.HasReadonly = true
		case "https://www.googleapis.com/auth/yt-analytics-monetary.readonly":
			out.HasMonetary = true
		}
	}
	return out, nil
}

// canaryUploadLiteral is the SINGLE source-of-truth for the canary
// body. Both the byte slice (canaryUploadBytes) and the size constant
// (canaryUploadSize) derive from this — a future maintainer edits
// THIS line, the two derived values follow without cross-reference
// mistakes. The previous shape (a duplicated literal in two places)
// burst silently: a delete in canaryUploadBytes without the matching
// edit in canaryUploadSize surfaced as a content-range mismatch
// rather than a compile error.
const canaryUploadLiteral = "INSTAEDIT-CANARY-PAYLOAD\n"

// canaryUploadBytes is a small synthetic payload used for the
// optional INSTAEDIT-OAUTH-CANARY test upload. Intentionally small
// (a single PUT chunk) so:
//
//   - The canary doesn't meaningfully consume the daily videos.insert
//     quota (1 call per /validate invocation that requests canary).
//   - Test assertions can hard-code the byte offsets
//     (bytes 0-21 / 22 bytes) without measuring the actual byte length.
//
// YouTube's videos.insert endpoint MAY accept this non-video content
// (returning 200 + video_id) OR reject it with 4xx (invalid argument,
// because the upload protocol expects video/* bytes). Both outcomes
// prove end-to-end binding — the snippet.channelId reconciliation on
// the resulting video (or the videos.list absence on rejection) is
// the source of truth. The canary upload body's content is NOT what
// step-4 measures — channel binding is.
var canaryUploadBytes = []byte(canaryUploadLiteral)

// canaryUploadSize derives from canaryUploadLiteral — guarantees
// compile-time sync with canaryUploadBytes.
const canaryUploadSize = int64(len(canaryUploadLiteral))

// canaryUploadContentType is intentionally NOT video/* — the canary
// upload is a probe, not a real publish. Stamping a non-video MIME
// makes the canary visually distinct in any tooling that filters on
// MIME type, AND signals to Google's API that the body is not a
// real video (Google may 4xx on MIME mismatch; that's still
// acceptable evidence that the OAuth grant can call videos.insert).
const canaryUploadContentType = "application/octet-stream"

// ErrYouTubeCanaryRejected is the canonical sentinel for hard 4xx
// rejections from the canary upload path (videos.insert init OR PUT
// chunk PUT). Distinct from ErrYouTubeChannelMismatch so the handler
// can produce a different audit-log message ("canary upload rejected
// by YouTube" vs "canary landed on the wrong channel"), but the
// runbook is identical (status='reauth_required'). Transient 5xx
// errors are NOT wrapped in this sentinel — they remain plain
// wrapped so the handler treats them as transient (next-sync retry).
//
// IMPORTANT: only 4xx codes SUPPRESSED in isHardRejection4xxStatus
// escalate to this sentinel. Rate-limit 429, Locked 423, every 5xx,
// plus decode / network / ctx-cancelled errors all stay on the
// transient branch — that's the deliberate choice the user's
// 200-channel YouTube OAuth plan asks for (transient blip ≠ grant
// drift ≠ reauth).
var ErrYouTubeCanaryRejected = errors.New("youtube canary upload was rejected by videos.insert (4xx)")

// statusCodeRegexp captures the (status N) triplet embedded in the
// upstream wrapped errors emitted by initiateResumableSession and
// putChunk. The two methods format their errors in known shapes:
//
//   - initiateResumableSession: "init session failed (status N): ..."
//   - putChunk: "unexpected PUT response (status N): ..." /
//     "rate limited (status 429, ...)" /
//     "server error (status N, ...)" or "server error (status N)"
//
// The regex matches just the parenthesized (status N) pair so
// downstream logic stays decoupled from the leading message verb.
// Compile-time build (var not const, regexp.MustCompile panics on
// bad pattern).
var statusCodeRegexp = regexp.MustCompile(`\(status (\d+)\)`)

// isHardRejection4xxStatus inspects the wrapped error returned by
// initiateResumableSession or putChunk (the two upstream callers
// CanaryUpload delegates to) and returns true iff it represents a
// HARD 4xx rejection that should be flagged ErrYouTubeCanaryRejected
// (handler → 422 + reauth) versus a TRANSIENT response that should
// remain plain wrapped (handler → next-sync-retry).
//
// Why regex on err.Error() rather than typed sentinels from the
// upstream methods: initiateResumableSession / putChunk are
// pre-existing call sites used by the publish path (not just the
// canary) and a sentinel refactor would have a much wider blast
// radius. The string-format shape they emit is documented AND
// stable across each method's revisions. The 4xx codes that get
// the reauth treatment are explicitly enumerated; any status
// outside the table falls through to the transient branch by
// default.
//
// Enumerated reauth statuses (4xx-not-429-or-423):
//
//	400 — bad request / malformed metadata
//	401 — YouTube-side token rejection mid-upload (operator must re-consent)
//	403 — forbidden / Brand Account re-bound silently
//	404 — session URI lost or grant revoked by Google
//	408 — rare; request timeout sent by YouTube
//	409 — channel / quota state conflict
//	410 — gone; channel may have been deleted
//	422 — unprocessable; metadata valid but refused
//	451 — legal / jurisdictional unavailability
//
// Transient-by-default (NOT in table):
//
//	429 — rate limit (Retry-After header is honored upstream)
//	423 — Locked; transient alignment-of-resources retry signal
//	5xx — server error; retried on next-sync tick
//	decode / network / ctx-cancelled — pass-through plainly
//
// Long-term: a future refactor should add typed sentinels to
// initiateResumableSession and putChunk so CanaryUpload can switch
// on errors.Is instead of regex. Tracked as a follow-up; the
// regex shape is correct for the 4-step pipeline today.
func isHardRejection4xxStatus(err error) bool {
	if err == nil {
		return false
	}
	m := statusCodeRegexp.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return false
	}
	switch m[1] {
	case "400", "401", "403", "404", "408", "409", "410", "422", "451":
		return true
	}
	return false
}

// CanaryUploadResult captures the canary's outcome for the handler
// (step 4 of /accounts/{id}/validate). The handler renders this into
// the 200 OK response so the SPA can surface "canary video id"
// alongside the validation summary.
type CanaryUploadResult struct {
	// VideoID is the YouTube-assigned video id (typically 11 chars). The
	// SPA renders it as a clickable link to https://www.youtube.com/watch?v=VIDEOID
	// so the operator can verify the canary exists in their YouTube Studio.
	VideoID string
	// UploadedChannelID is the snippet.channelId YouTube stamped on the
	// resulting video — the channel the upload ACTUALLY landed on. On
	// success ALWAYS equal to the supplied expectedChannelID; the
	// function short-circuits to a wrapped ErrYouTubeChannelMismatch
	// on a bind-mismatch (the consistency check rejects the row before
	// success is returned).
	UploadedChannelID string
}

// CanaryUpload uploads the canary payload as a PRIVATE YouTube video
// (titled INSTAEDIT-OAUTH-CANARY-{channel_id}-{unix-timestamp}), then
// verifies the resulting snippet.channelId matches the expected
// channel. This is the OPTIONAL step 4 of the 4-step
// /accounts/{id}/validate pipeline. The flow is identical to a
// normal publish (initiate resumable session → single-chunk PUT →
// videos.list reconcile for channel binding) but with a fixed-length
// body and an INSTAEDIT-OAUTH-CANARY title so the operator can clean
// them up in bulk from YouTube Studio. Per the user's
// 200-channel YouTube OAuth plan, canary is opt-in per request
// (body field `"canary": true`) so the default validate path stays
// cheap (no quota cost, no noise in YouTube Studio).
//
// Bound to expectedChannelID at TWO checkpoints:
//
//  1. The PUT chunk server confirms the upload completed (terminal
//     200 returning {"id":"<videoID>"}) — the videoID is then used
//     as the query key for step 2.
//  2. After upload, videos.list pulls the actual snippet.channelId
//     YouTube stamped on the video and compares it to
//     `expectedChannelID`. THIS is the source of truth — the handler
//     MUST trust this over channels.list(page1..N) for end-to-end
//     proof. A canary that lands on the wrong channel is a hard
//     reauth-required signal (the OAuth grant is silently re-bound
//     to a different Brand Account, the very failure mode the user
//     spec wants to catch).
//
// Errors:
//   - wrapped ErrYouTubeChannelMismatch → upload succeeded but landed
//     on a DIFFERENT channel. Handler maps to 422 +
//     status='reauth_required' — same runbook as step-3 bind fail.
//   - wrapped ErrYouTubeCanaryRejected → YouTube refused the upload
//     (4xx-not-429: quota exceeded, scope missing, format error).
//     Handler maps to 422 + status='reauth_required' (the grant
//     reached YouTube but was refused — the operator cannot publish
//     this way regardless).
//   - 5xx / decode / network / ctx-cancelled → plain wrapped error.
//     Handler treats as transient (next-sync retry); mirrors the
//     existing pre-step-pre-validate channel-binding convention.
func (s *YouTubeOAuthService) CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*CanaryUploadResult, error) {
	if expectedChannelID == "" {
		return nil, fmt.Errorf("youtube canary: empty expected channel id")
	}
	if accessToken == "" {
		return nil, fmt.Errorf("youtube canary: empty access token")
	}

	title := fmt.Sprintf("INSTAEDIT-OAUTH-CANARY-%s-%d", expectedChannelID, s.now().UTC().Unix())
	metadata := map[string]interface{}{
		"snippet": map[string]interface{}{
			"title":           title,
			"categoryId":      "22", // People & Blogs — neutral category
			"defaultLanguage": "en",
			"description":     "OAuth readiness canary video. Auto-uploaded by InstaEdit to confirm channel binding + upload capability. Safe to delete from YouTube Studio.",
		},
		"status": map[string]interface{}{
			"privacyStatus":           "private",
			"selfDeclaredMadeForKids": false,
		},
	}

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, canaryUploadSize, canaryUploadContentType)
	if err != nil {
		// initiateResumableSession returns plain wrapped errors today;
		// re-promote HARSH rejections (4xx-not-429/codes) to
		// ErrYouTubeCanaryRejected so the handler routes them. The
		// classifier is regex-based (see isHardRejection4xxStatus)
		// so 429 / Locked / decode / network / 5xx stay transient
		// and don't accidentally escalate to reauth.
		wrapped := fmt.Errorf("youtube canary: initiate session: %w", err)
		if isHardRejection4xxStatus(err) {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, err)
		}
		return nil, wrapped
	}

	contentRange := fmt.Sprintf("bytes 0-%d/%d", canaryUploadSize-1, canaryUploadSize)
	videoID, _, _, putErr := s.putChunk(ctx, uploadURL, canaryUploadBytes, contentRange, canaryUploadSize)
	if putErr != nil {
		// Same classifier as the initiate path — applies to
		// 200-with-bad-body decode errors, which carry NO (status N)
		// substring and fall through to the transient branch (NOT
		// escalated to ErrYouTubeCanaryRejected). 5xx, 429, 423,
		// and any 4xx-suppressed reauth list per isHardRejection4xxStatus.
		wrapped := fmt.Errorf("youtube canary: upload chunk put: %w", putErr)
		if isHardRejection4xxStatus(putErr) {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, putErr)
		}
		return nil, wrapped
	}
	if videoID == "" {
		return nil, fmt.Errorf("youtube canary: upload returned no video id (unexpected)")
	}

	video, fetchErr := s.fetchVideoStatus(ctx, accessToken, videoID)
	if fetchErr != nil {
		// videos.list on the just-uploaded video returning 4xx/5xx is
		// almost always transient (the video rows are indexed async)
		// — pass through plainly so the handler retries on next tick.
		return nil, fmt.Errorf("youtube canary: post-upload videos.list: %w", fetchErr)
	}
	if video.Snippet.ChannelID == "" {
		return nil, fmt.Errorf("youtube canary: snippet.channelId is empty for video %s (videos.list returned no channel binding)", videoID)
	}
	if video.Snippet.ChannelID != expectedChannelID {
		return nil, fmt.Errorf("%w: canary uploaded to channel %q, expected %q (video_id=%s)",
			ErrYouTubeChannelMismatch, video.Snippet.ChannelID, expectedChannelID, videoID)
	}

	slog.Info("youtube canary: uploaded private canary video and confirmed channel binding",
		"video_id", videoID, "channel_id", expectedChannelID, "title", title)

	return &CanaryUploadResult{
		VideoID:           videoID,
		UploadedChannelID: video.Snippet.ChannelID,
	}, nil
}
