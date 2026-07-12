package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ---------------------------------------------------------------------------
// SPRINT 5.1 (P1#9) — Provider error taxonomy.
//
// Every provider's Publish / Upload / Refresh / OAuth callback returns
// a *ProviderError on non-2xx responses. The taxonomy is FIXED at 10
// codes so the worker (and the API layer that maps errors to HTTP
// status) can act on a stable contract without inspecting platform-
// specific error shapes. Platforms translate their HTTP status +
// response body + response headers into the canonical type via
// NewProviderError; callers then use errors.As to detect and act.
// ---------------------------------------------------------------------------

// ProviderErrorCode is the canonical taxonomy of provider errors.
// Each code maps to a stable retry/UX decision so callers don't
// need to inspect platform-specific error shapes.
type ProviderErrorCode string

const (
	ErrorCodeValidationError         ProviderErrorCode = "validation_error"
	ErrorCodeAuthenticationError     ProviderErrorCode = "authentication_error"
	ErrorCodePermissionMissing       ProviderErrorCode = "permission_missing"
	ErrorCodeReauthenticationRequired ProviderErrorCode = "reauthentication_required"
	ErrorCodeRateLimited             ProviderErrorCode = "rate_limited"
	ErrorCodeProviderUnavailable     ProviderErrorCode = "provider_unavailable"
	ErrorCodeMediaProcessingFailed   ProviderErrorCode = "media_processing_failed"
	ErrorCodeContentRejected         ProviderErrorCode = "content_rejected"
	ErrorCodeQuotaExceeded           ProviderErrorCode = "quota_exceeded"
	ErrorCodeInternalError           ProviderErrorCode = "internal_error"
)

// AllProviderErrorCodes returns the full taxonomy as a slice. Used
// for validation in the API layer (rejecting unknown codes from
// clients) and for docs generation.
func AllProviderErrorCodes() []ProviderErrorCode {
	return []ProviderErrorCode{
		ErrorCodeValidationError,
		ErrorCodeAuthenticationError,
		ErrorCodePermissionMissing,
		ErrorCodeReauthenticationRequired,
		ErrorCodeRateLimited,
		ErrorCodeProviderUnavailable,
		ErrorCodeMediaProcessingFailed,
		ErrorCodeContentRejected,
		ErrorCodeQuotaExceeded,
		ErrorCodeInternalError,
	}
}

// IsValidProviderErrorCode reports whether code is one of the 10
// canonical taxonomy codes.
func IsValidProviderErrorCode(code ProviderErrorCode) bool {
	switch code {
	case ErrorCodeValidationError, ErrorCodeAuthenticationError,
		ErrorCodePermissionMissing, ErrorCodeReauthenticationRequired,
		ErrorCodeRateLimited, ErrorCodeProviderUnavailable,
		ErrorCodeMediaProcessingFailed, ErrorCodeContentRejected,
		ErrorCodeQuotaExceeded, ErrorCodeInternalError:
		return true
	}
	return false
}

// ProviderError is the canonical typed error returned by every
// provider's Publish / Upload / Refresh / OAuth-callback calls.
//
// Fields are exported so internal callers can read/write freely —
// this is a struct (not a sealed type) for ergonomics. The tradeoff:
// a misbehaving caller CAN mutate Code / Platform / StatusCode after
// construction (e.g. on the way to a retry annotation). The
// documented contract is "construct via NewProviderError or
// NewRateLimitError, then pass through"; code that mutates after
// construction is responsible for not breaking downstream assertions.
// No enforcement (lock or unexport) — would cost more than it saves.
//
// Field semantics:
//   - Code: the canonical taxonomy code (one of the 10 above).
//   - Platform: the platform name (e.g. "tiktok", "instagram").
//   - Retryable: hint for the worker — true if a retry MAY succeed
//     (rate_limited, provider_unavailable, media_processing_failed).
//   - RetryAfter: 0 = unknown (caller uses default backoff). Non-zero
//     means "come back in N". Extracted from Retry-After, X-RateLimit-Reset,
//     or platform-specific body fields (e.g. Meta error_data).
//   - ProviderCode: the platform-specific code (e.g. TikTok "invalid_params",
//     Meta "190:4", YouTube "quotaExceeded", Twitter "89", LinkedIn "401").
//     NOT one of our taxonomy codes — this is the upstream code, kept
//     for debug + correlation with platform dashboards.
//   - SafeMessage: NEVER the raw provider body. Format: "{platform}
//     operation failed (status {code}): {category}". Safe for both
//     logs and user-facing API responses.
//   - RequestID: the platform's request id (Meta x-fb-trace-id, Twitter
//     x-request-id, TikTok x-tt-logid, etc.). Used to correlate with
//     platform support dashboards.
//   - Cause: the underlying network/parse error, for debug. NEVER in
//     Error() output — SafeMessage is what callers see.
//   - StatusCode: the HTTP status (0 if not applicable, e.g. for
//     transport-layer errors).
type ProviderError struct {
	Code         ProviderErrorCode
	Platform     string
	Retryable    bool
	RetryAfter   time.Duration
	ProviderCode string
	SafeMessage  string
	RequestID    string
	Cause        error
	StatusCode   int
}

// Error implements the error interface. The message is SafeMessage
// + Code, NEVER the raw provider body. Use Cause (via errors.Unwrap)
// for the unwrapped raw error in debug logs.
func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil ProviderError>"
	}
	if e.SafeMessage == "" {
		return fmt.Sprintf("%s: %s", e.Platform, e.Code)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Platform, e.Code, e.SafeMessage)
}

// Unwrap returns Cause so errors.Is / errors.As work through the
// wrapping. ProviderError chains are inspectable end-to-end.
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsProviderError reports whether err is (or wraps) a *ProviderError.
// Returns the *ProviderError + true on success, (nil, false) otherwise.
// Convenience wrapper for the canonical `errors.As(err, &pe)` pattern.
func IsProviderError(err error) (*ProviderError, bool) {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// NewRateLimitError produces a canonical rate_limited ProviderError
// for backward-compat flows. Existing call sites that build a
// *RateLimitError directly (e.g. inside a provider that detects a
// 429 BEFORE making the HTTP call) can use this to get a typed
// ProviderError in one line. For HTTP responses, prefer
// NewProviderError which auto-extracts RetryAfter from headers.
func NewRateLimitError(platform string, retryAfter time.Duration) *ProviderError {
	return &ProviderError{
		Code:        ErrorCodeRateLimited,
		Platform:    platform,
		Retryable:   true,
		RetryAfter:  retryAfter,
		SafeMessage: buildSafeMessage(platform, ErrorCodeRateLimited, http.StatusTooManyRequests),
		StatusCode:  http.StatusTooManyRequests,
	}
}

// MapHTTPStatus converts an HTTP status code to a ProviderErrorCode.
// The mapping is platform-agnostic — Meta-specific overrides (e.g.
// error_subcode 4 → rate_limited) are applied in NewProviderError
// AFTER this initial mapping.
//
// Catch-all: any unrecognized 4xx → validation_error (callers
// inspect the body for the actual reason); any 5xx →
// provider_unavailable. 2xx and 3xx are NOT mapped here — they
// should not be passed to this function.
func MapHTTPStatus(statusCode int) ProviderErrorCode {
	switch statusCode {
	case http.StatusBadRequest:
		return ErrorCodeValidationError
	case http.StatusUnauthorized:
		return ErrorCodeAuthenticationError
	case http.StatusPaymentRequired:
		return ErrorCodeQuotaExceeded
	case http.StatusForbidden:
		return ErrorCodePermissionMissing
	case http.StatusNotFound, http.StatusGone:
		return ErrorCodeContentRejected
	case http.StatusTooManyRequests:
		return ErrorCodeRateLimited
	}
	if statusCode >= 500 && statusCode < 600 {
		return ErrorCodeProviderUnavailable
	}
	if statusCode >= 400 && statusCode < 500 {
		return ErrorCodeValidationError // catch-all 4xx
	}
	return ErrorCodeInternalError
}

// ParseThrottleHeaders extracts the canonical retry_after from the
// standard set of throttling response headers, returning 0 if no
// header is present or all values are unparseable. The worker
// falls back to the decorrelated-jitter backoff in that case.
//
// Header preference order (canonical first, per RFC 7231 + common
// platform conventions):
//
//	1. Retry-After                (RFC 7231 §7.1.3, all platforms)
//	2. X-RateLimit-Reset          (epoch seconds — GitHub, Stripe, YouTube)
//	3. X-Rate-Limit-Reset         (alt spelling — some gateways)
//	4. X-Rate-Limit-Reset-After   (some CDN providers)
//	5. x-rate-limit-reset         (lowercase — Twitter v2 convention)
//
// Reuses ParseRetryAfter (provider.go) which handles delta-seconds,
// Go duration strings, RFC 1123 dates, and epoch seconds.
func ParseThrottleHeaders(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	now := time.Now()
	for _, name := range []string{
		"Retry-After",
		"X-RateLimit-Reset",
		"X-Rate-Limit-Reset",
		"X-Rate-Limit-Reset-After",
		"x-rate-limit-reset", // Twitter v2 lowercase convention
	} {
		if v := h.Get(name); v != "" {
			if d := ParseRetryAfter(v, now); d > 0 {
				return d
			}
		}
	}
	return 0
}

// NewProviderError is the canonical constructor for *ProviderError.
// Providers call this with the platform, HTTP status, response body,
// response headers, and the underlying cause (the network/parse
// error if any). The function maps (status, body, headers) → *ProviderError.
//
// body is typically the result of io.ReadAll(resp.Body); the
// constructor reads it to extract structured fields but does NOT
// include any of it in the returned error's Error() output
// (SafeMessage is what callers see).
func NewProviderError(platform string, status int, body string, headers http.Header, cause error) *ProviderError {
	code := MapHTTPStatus(status)
	var providerCode, requestID string
	var extraRetryAfter time.Duration
	var metaIsRateLimit bool

	// Per-platform body parsers — extract ProviderCode, RequestID,
	// any platform-specific RetryAfter, AND any platform-specific
	// code override (Meta error_subcode 4 = rate-limited).
	switch platform {
	case models.PlatformTikTok:
		providerCode, requestID, extraRetryAfter = parseTikTokErrorBody(body, headers)
	case models.PlatformYouTube:
		providerCode, requestID, extraRetryAfter = parseYouTubeErrorBody(body, headers)
	case models.PlatformTwitter:
		providerCode, requestID, extraRetryAfter = parseTwitterErrorBody(body, headers)
	case models.PlatformLinkedIn:
		providerCode, requestID, extraRetryAfter = parseLinkedInErrorBody(body, headers)
	case models.PlatformInstagram, models.PlatformFacebook, models.PlatformThreads:
		providerCode, requestID, extraRetryAfter, metaIsRateLimit = parseMetaErrorBody(body, headers)
	}

	// Meta-specific override: Meta wraps rate-limit responses in 400
	// with error_subcode=4 (Application request limit reached). The
	// status-only mapping misclassifies as validation_error; the
	// subcode says it's actually a rate limit. Override to
	// rate_limited so the worker's rate-limit handler picks it up.
	if metaIsRateLimit {
		code = ErrorCodeRateLimited
	}

	// Preserve the raw body in Cause for debug logs when caller
	// passes nil cause. The body is NOT leaked via Error() (that uses
	// SafeMessage); it's only accessible via errors.Unwrap + debug
	// log lines, which is the documented behavior. When the caller
	// passes a non-nil cause, that cause is preserved VERBATIM —
	// the body wrapping is suppressed so the caller can attach their
	// own richer context (e.g. "twitter 2/tweets: body=...").
	if cause == nil && body != "" {
		cause = fmt.Errorf("provider body (truncated): %s", truncateForLog(body, 256))
	}

	retryAfter := ParseThrottleHeaders(headers)
	if retryAfter == 0 && extraRetryAfter > 0 {
		retryAfter = extraRetryAfter
	}

	return &ProviderError{
		Code:         code,
		Platform:     platform,
		Retryable:    isRetryable(code),
		RetryAfter:   retryAfter,
		ProviderCode: providerCode,
		SafeMessage:  buildSafeMessage(platform, code, status),
		RequestID:    requestID,
		Cause:        cause,
		StatusCode:   status,
	}
}

// isRetryable reports whether the canonical code allows the worker
// to retry without operator intervention. Rate-limited and provider-
// unavailable errors are transient; quota-exceeded is retryable only
// after a long cooldown (the worker checks RetryAfter); auth /
// permission / content-rejected are NOT retryable — they need user
// or operator action.
func isRetryable(code ProviderErrorCode) bool {
	switch code {
	case ErrorCodeRateLimited,
		ErrorCodeProviderUnavailable,
		ErrorCodeMediaProcessingFailed,
		ErrorCodeInternalError:
		return true
	}
	return false
}

// buildSafeMessage returns the canonical SafeMessage for the
// given platform + code + status. The message:
//
//   - NEVER includes the raw provider body (which may have user
//     PII, internal tokens, etc.).
//   - Is short, single-line, safe for both logs and user-facing
//     API responses.
//   - Includes the platform name, HTTP status, and a human-readable
//     category derived from the taxonomy code (e.g. "rate_limited"
//     → "rate limited").
func buildSafeMessage(platform string, code ProviderErrorCode, status int) string {
	category := strings.ReplaceAll(string(code), "_", " ")
	return fmt.Sprintf("%s operation failed (status %d): %s", platform, status, category)
}

// ---------------------------------------------------------------------------
// Per-platform body parsers.
//
// Each parser takes the raw response body + response headers, and
// returns (providerCode, requestID, extraRetryAfter). On JSON
// parse failure or missing fields, returns "" / "" / 0 — the
// caller still has a working *ProviderError, just with less detail.
// ---------------------------------------------------------------------------

// parseMetaErrorBody extracts the Meta Graph API error fields:
//   - error.code (numeric — 190 = invalid token, 4 = app rate limit, etc.)
//   - error.error_subcode (e.g. 4 inside 190 = app-level rate limit)
//   - x-fb-trace-id header (the platform's request id)
//   - x-fb-debug header (the platform's debug id — secondary signal)
//
// Format: "code:subcode" (e.g. "190:4"). When subcode is 0, just "code".
// The fourth return value is true iff error_subcode == 4, which
// signals a Meta rate-limit response (the worker wants to handle
// this even when the HTTP status is 400). The caller applies the
// override; the parser doesn't mutate any upstream state.
//
// No body-level retry_after extraction today (Meta doesn't surface
// it reliably); the throttling header path is the canonical signal.
func parseMetaErrorBody(body string, h http.Header) (string, string, time.Duration, bool) {
	requestID := firstNonEmpty(h.Get("x-fb-trace-id"), h.Get("x-fb-debug"), h.Get("X-FB-Trace-Id"))
	var parsed struct {
		Error struct {
			Code         int `json:"code"`
			ErrorSubcode int `json:"error_subcode"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	providerCode := strconv.Itoa(parsed.Error.Code)
	if parsed.Error.ErrorSubcode != 0 {
		providerCode += ":" + strconv.Itoa(parsed.Error.ErrorSubcode)
	}
	return providerCode, requestID, 0, parsed.Error.ErrorSubcode == 4
}

// parseYouTubeErrorBody extracts the YouTube Data API error fields:
//   - error.errors[0].reason (e.g. "quotaExceeded", "channelClosed",
//     "processingFailed", "uploadLimitExceeded")
//   - x-request-id header (the platform's request id)
//
// The `reason` is the actionable code; the top-level error.message
// is human-readable but we don't surface it in SafeMessage (it can
// contain user-supplied content from upload metadata).
func parseYouTubeErrorBody(body string, h http.Header) (string, string, time.Duration) {
	requestID := firstNonEmpty(h.Get("x-request-id"), h.Get("X-Request-Id"))
	var parsed struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	providerCode := ""
	if len(parsed.Error.Errors) > 0 {
		providerCode = parsed.Error.Errors[0].Reason
	}
	return providerCode, requestID, 0
}

// parseTwitterErrorBody extracts the Twitter v2 error fields:
//   - errors[0].code (numeric — 89 = invalid token, 32 = not authenticated,
//     88 = rate limit, 326 = temporarily locked, 64 = account suspended)
//   - x-request-id header (the platform's request id)
//   - x-rate-limit-reset header (lowercase — Twitter-specific throttling
//     signal; also handled by ParseThrottleHeaders but extracted here
//     for the request-scoped RetryAfter)
func parseTwitterErrorBody(body string, h http.Header) (string, string, time.Duration) {
	requestID := firstNonEmpty(h.Get("x-request-id"), h.Get("X-Request-Id"))
	var parsed struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	providerCode := ""
	if len(parsed.Errors) > 0 {
		providerCode = strconv.Itoa(parsed.Errors[0].Code)
	}
	// Twitter's x-rate-limit-reset is also picked up by
	// ParseThrottleHeaders (case-insensitive lookup), so we don't
	// need to duplicate the value here.
	return providerCode, requestID, 0
}

// parseLinkedInErrorBody extracts the LinkedIn API error fields:
//   - serviceErrorCode (numeric — 401 = invalid token, 403 = insufficient
//     scope, 429 = throttle, 500 = server error)
//   - x-li-id header (the platform's request id — LinkedIn's UUID-ish
//     correlation id)
//   - status field (HTTP-equivalent status, redundant with the actual
//     status code, kept for reference)
func parseLinkedInErrorBody(body string, h http.Header) (string, string, time.Duration) {
	requestID := firstNonEmpty(h.Get("x-li-id"), h.Get("X-LI-Id"), h.Get("x-request-id"))
	var parsed struct {
		ServiceErrorCode int `json:"serviceErrorCode"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	return strconv.Itoa(parsed.ServiceErrorCode), requestID, 0
}

// parseTikTokErrorBody extracts the TikTok Display API error fields:
//   - error.code (string — "invalid_params", "spam_risk_too_many_posts",
//     "rate_limit_exceeded", "unauthorized", "forbidden", etc.)
//   - x-tt-logid header (the platform's log id — TikTok's request id)
//   - x-tt-error-code header (sometimes set separately from the body)
func parseTikTokErrorBody(body string, h http.Header) (string, string, time.Duration) {
	requestID := firstNonEmpty(h.Get("x-tt-logid"), h.Get("X-TT-Logid"))
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	providerCode := parsed.Error.Code
	if providerCode == "" {
		// Header fallback — some TikTok endpoints set the code
		// header but not the body field.
		providerCode = h.Get("x-tt-error-code")
	}
	return providerCode, requestID, 0
}

// firstNonEmpty returns the first non-empty string among the
// arguments. Used to pick the most-authoritative request-id
// header across multiple possible spellings.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
