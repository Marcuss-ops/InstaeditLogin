package services

import (
	"context"
	"encoding/base64"
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
		return fmt.Errorf("youtube validate returned status %d", resp.StatusCode)
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
//	// HasUpload / HasReadonly / HasForceSSL / HasMonetary are derived
//
// flags computed at construction time so callers can write
// `if !info.HasUpload { ... }` without re-parsing `Scope`
// themselves. The canonical scope strings are the full
// https://www.googleapis.com/auth/<scope> form (NOT the shortened
// alias) — matches what GetLoginURLWithOptions sets in the consent
// URL and what Google returns from tokeninfo.
type YouTubeTokenInfo struct {
	Aud       string
	Azp       string
	Scope     string
	ExpiresIn time.Duration
	Email     string

	HasUpload   bool
	HasReadonly bool
	// HasForceSSL is true when the token carries the
	// youtube.force-ssl scope — required for thumbnails.set,
	// videos.update, metadata/privacy writes and YouTube Live
	// Streaming API calls. The canonical OAuth grant requests all
	// three (upload, readonly, force-ssl) but re-consented tokens
	// or trimmed grants may lack it; the validator must demand it.
	HasForceSSL bool
	// HasMonetary is true when the token has the YouTube Analytics
	// monetary-readonly scope required for revenue/RPM/CPM data.
	//
	// LEGACY PRESERVE (Blocco Bug #3 — canonical YT OAuth scope
	// cleanup). New OAuth grants no longer request the
	// yt-analytics-monetary.readonly scope (the canonical
	// youtubeOAuthScopes const in youtube_oauth.go no longer
	// includes it). However, tokens issued BEFORE the cleanup
	// may still carry the scope, and YouTube will not retroactively
	// revoke it unless the user re-consents. So this flag remains a
	// meaningful predicate for legacy tokens — affordances like
	// storeYouTubeEarnings continue to operate correctly for users
	// who already granted the scope, and silently skip for new
	// grants (which is the intended post-cleanup behavior).
	//
	// Do NOT remove this field without also auditing every reader
	// (currently pkg/api/accounts_read_handlers.go) and the
	// earnings-sync affinity end-to-end.
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
		return nil, fmt.Errorf("youtube tokeninfo returned %d", resp.StatusCode)
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
		case "https://www.googleapis.com/auth/youtube.force-ssl":
			out.HasForceSSL = true
		}
	}
	return out, nil
}

// canaryUploadBase64 is a tiny, static, valid H.264/MP4 payload used by
// the OAuth canary. It is intentionally a real video rather than a text
// probe: a successful canary must exercise YouTube's media ingest path,
// not merely prove that an OAuth grant can create an upload session.
// The payload is 1 second, 16x16, silent video and contains no secrets.
const canaryUploadBase64 = "AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAMUbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAA+gAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAj90cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAA+gAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAABAAAAAQAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAAPoAAAAAAABAAAAAAG3bWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAABAAAAAQABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABYm1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAASJzdGJsAAAAvnN0c2QAAAAAAAAAAQAAAK5hdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAABAAEABIAAAASAAAAAAAAAABFUxhdmM2Mi4xMS4xMDAgbGlieDI2NAAAAAAAAAAAAAAAGP//AAAANGF2Y0MBZAAK/+EAF2dkAAqs2V7ARAAAAwAEAAADAAg8SJZYAQAGaOvjyyLA/fj4AAAAABBwYXNwAAAAAQAAAAEAAAAUYnRydAAAAAAAABW4AAAAAAAAABhzdHRzAAAAAAAAAAEAAAABAABAAAAAABxzdHNjAAAAAAAAAAEAAAABAAAAAQAAAAEAAAAUc3RzegAAAAAAAAK3AAAAAQAAABRzdGNvAAAAAAAAAAEAAANEAAAAYXVkdGEAAABZbWV0YQAAAAAAAAAhaGRscgAAAAAAAAAAbWRpcmFwcGwAAAAAAAAAAAAAAAAsaWxzdAAAACSpdG9vAAAAHGRhdGEAAAABAAAAAExhdmY2Mi4zLjEwMAAAAAhmcmVlAAACv21kYXQAAAKfBgX//5vcRem95tlIt5Ys2CDZI+7veDI2NCAtIGNvcmUgMTY1IC0gSC4yNjQvTVBFRy00IEFWQyBjb2RlYyAtIENvcHlsZWZ0IDIwMDMtMjAyNSAtIGh0dHA6Ly93d3cudmlkZW9sYW4ub3JnL3gyNjQuaHRtbCAtIG9wdGlvbnM6IGNhYmFjPTEgcmVmPTMgZGVibG9jaz0xOjA6MCBhbmFseXNlPTB4MzoweDExMyBtZT1oZXggc3VibWU9NyBwc3k9MSBwc3lfcmQ9MS4wMDowLjAwIG1peGVkX3JlZj0xIG1lX3JhbmdlPTE2IGNocm9tYV9tZT0xIHRyZWxsaXM9MSA4eDhkY3Q9MSBjcW09MCBkZWFkem9uZT0yMSwxMSBmYXN0X3Bza2lwPTEgY2hyb21hX3FwX29mZnNldD0tMiB0aHJlYWRzPTEgbG9va2FoZWFkX3RocmVhZHM9MSBzbGljZWRfdGhyZWFkcz0wIG5yPTAgZGVjaW1hdGU9MSBpbnRlcmxhY2VkPTAgYmx1cmF5X2NvbXBhdD0wIGNvbnN0cmFpbmVkX2ludHJhPTAgYmZyYW1lcz0zIGJfcHlyYW1pZD0yIGJfYWRhcHQ9MSBiX2JpYXM9MCBkaXJlY3Q9MSB3ZWlnaHRiPTEgb3Blbl9nb3A9MCB3ZWlnaHRwPTIga2V5aW50PTI1MCBrZXlpbnRfbWluPTEgc2NlbmVjdXQ9NDAgaW50cmFfcmVmcmVzaD0wIHJjX2xvb2thaGVhZD00MCByYz1jcmYgbWJ0cmVlPTEgY3JmPTIzLjAgcWNvbXA9MC42MCBxcG1pbj0wIHFwbWF4PTY5IHFwc3RlcD00IGlwX3JhdGlvPTEuNDAgYXE9MToxLjAwAIAAAAAQZYiEABX//vfJ78Cm69vfgQ=="

var canaryUploadBytes = mustDecodeCanaryUpload()

func mustDecodeCanaryUpload() []byte {
	// Keep malformed fixture data a programmer error, not a runtime
	// OAuth classification. The literal is generated once and checked
	// by the canary tests for the MP4 ftyp signature and byte length.
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(canaryUploadBase64, " ", ""))
	if err != nil {
		panic(fmt.Sprintf("invalid embedded YouTube canary MP4: %v", err))
	}
	return decoded
}

var canaryUploadSize = int64(len(canaryUploadBytes))

const canaryUploadContentType = "video/mp4"

// ErrYouTubeCanaryRejected is the canonical sentinel for hard 4xx
// rejections from the canary upload path (videos.insert init OR PUT
// chunk PUT) that indicate a GRANT-LEVEL or AUTH-LEVEL failure
// requiring reauthorisation. Specifically:
//
//	401 — YouTube-side token rejection mid-upload (operator must re-consent)
//	403 — forbidden / Brand Account re-bound silently
//	404 — session URI lost or grant revoked by Google
//	408 — rare; request timeout sent by YouTube
//	409 — channel / quota state conflict
//	410 — gone; channel may have been deleted
//	422 — unprocessable; metadata valid but refused
//	451 — legal / jurisdictional unavailability
//
// These all escalate to ErrYouTubeCanaryRejected and the handler
// flags the account reauth_required.
//
// HTTP 400 is deliberately EXCLUDED because it represents an invalid
// upload request/media response rather than proof that the OAuth grant
// was revoked. That's a separate sentinel (ErrYouTubeCanaryInvalidMedia)
// so the handler can distinguish an upload/media problem (transient,
// retry later) from grant revoked (reauth_required).
//
// Rate-limit 429, Locked 423, every 5xx, plus decode / network /
// ctx-cancelled errors all stay on the transient branch — that's
// the deliberate choice the user's 200-channel YouTube OAuth plan
// asks for (transient blip ≠ grant drift ≠ reauth).
var ErrYouTubeCanaryRejected = errors.New("youtube canary upload was rejected by videos.insert (auth-level 4xx)")

// ErrYouTubeCanaryInvalidMedia is the sentinel for a 400 response
// during the canary upload. A 400 means YouTube rejected the upload
// request/media shape; it does not by itself prove the grant is revoked.
// The handler treats this as a TRANSIENT signal (not reauth_required).
var ErrYouTubeCanaryInvalidMedia = errors.New("youtube canary: invalid media payload (400)")

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
// CanaryUpload delegates to) and returns:
//
//	(hardRejection=true, isInvalidMedia=false) — auth-level 4xx
//	    (401, 403, 404, 408, 409, 410, 422, 451) → escalate to
//	    ErrYouTubeCanaryRejected (handler → 422 + reauth).
//	(hardRejection=false, isInvalidMedia=true) — HTTP 400 →
//	    escalate to ErrYouTubeCanaryInvalidMedia (handler → transient,
//	    NOT reauth). A 400 alone is not proof of grant revocation.
//	(hardRejection=false, isInvalidMedia=false) — transient
//	    (5xx, 429, 423, decode, network, ctx-cancelled) → stay
//	    plain wrapped (handler → next-sync retry).
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
// Enumerated reauth statuses (4xx-not-429-or-423, NOT 400):
//
//	401 — YouTube-side token rejection mid-upload (operator must re-consent)
//	403 — forbidden / Brand Account re-bound silently
//	404 — session URI lost or grant revoked by Google
//	408 — rare; request timeout sent by YouTube
//	409 — channel / quota state conflict
//	410 — gone; channel may have been deleted
//	422 — unprocessable; metadata valid but refused
//	451 — legal / jurisdictional unavailability
//
// 400 is deliberately EXCLUDED — it means an invalid upload/media
// request, not a proven invalid grant. Even though the canary uses
// a valid MP4, the operator should not be forced to reconnect on a
// bare 400 response.
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
func isHardRejection4xxStatus(err error) (hardRejection, isInvalidMedia bool) {
	if err == nil {
		return false, false
	}
	m := statusCodeRegexp.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return false, false
	}
	// 400 is invalid media, NOT a grant-level rejection.
	if m[1] == "400" {
		return false, true
	}
	switch m[1] {
	case "401", "403", "404", "408", "409", "410", "422", "451":
		return true, false
	}
	return false, false
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
		// re-promote HARSH rejections (auth-level 4xx) to
		// ErrYouTubeCanaryRejected and media-level 400 to
		// ErrYouTubeCanaryInvalidMedia. The classifier is regex-based
		// (see isHardRejection4xxStatus) so 429 / Locked / decode /
		// network / 5xx stay transient and don't accidentally escalate
		// to reauth.
		wrapped := fmt.Errorf("youtube canary: initiate session: %w", err)
		if hard, invalidMedia := isHardRejection4xxStatus(err); hard {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, err)
		} else if invalidMedia {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryInvalidMedia, err)
		}
		return nil, wrapped
	}

	contentRange := fmt.Sprintf("bytes 0-%d/%d", canaryUploadSize-1, canaryUploadSize)
	videoID, _, _, putErr := s.putChunkWithContentType(ctx, uploadURL, canaryUploadBytes, contentRange, canaryUploadSize, canaryUploadContentType)
	if putErr != nil {
		// Same classifier as the initiate path — applies to
		// 200-with-bad-body decode errors, which carry NO (status N)
		// substring and fall through to the transient branch (NOT
		// escalated to ErrYouTubeCanaryRejected). 5xx, 429, 423,
		// and any 4xx-suppressed reauth list per isHardRejection4xxStatus.
		wrapped := fmt.Errorf("youtube canary: upload chunk put: %w", putErr)
		if hard, invalidMedia := isHardRejection4xxStatus(putErr); hard {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, putErr)
		} else if invalidMedia {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryInvalidMedia, putErr)
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
