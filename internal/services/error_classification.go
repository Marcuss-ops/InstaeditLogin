package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorKind is the provider-independent decision a worker must make after
// an operation fails. Workers should branch on Kind/Retryable, never on an
// HTTP status or provider-specific error string.
type ErrorKind string

const (
	ErrorKindTransient   ErrorKind = "transient"
	ErrorKindRateLimited ErrorKind = "rate_limited"
	ErrorKindAuth        ErrorKind = "auth"
	ErrorKindPermanent   ErrorKind = "permanent"
)

// NormalizedError is the shared failure contract between providers and
// durable workers. Cause remains available for errors.Is/errors.As, while
// Code and HTTPStatus are safe, stable diagnostics for persistence/metrics.
type NormalizedError struct {
	Provider   string
	Operation  string
	Kind       ErrorKind
	Code       string
	HTTPStatus int
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *NormalizedError) Error() string {
	if e == nil {
		return "<nil normalized error>"
	}
	if e.Code != "" {
		return fmt.Sprintf("%s error: %s", e.Kind, e.Code)
	}
	return fmt.Sprintf("%s error", e.Kind)
}

func (e *NormalizedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ClassifyErrorFor enriches the shared result with caller-owned context while
// keeping the classification rules in one place. Provider/operation are
// diagnostic labels; they never change the retry decision.
func ClassifyErrorFor(provider, operation string, err error) *NormalizedError {
	classified := ClassifyError(err)
	if classified == nil {
		return nil
	}
	if classified.Provider == "" {
		classified.Provider = provider
	}
	classified.Operation = operation
	return classified
}

// IsErrorKind reports whether err normalizes to the requested worker kind.
func IsErrorKind(err error, kind ErrorKind) bool {
	classified := ClassifyError(err)
	return classified != nil && classified.Kind == kind
}

// ClassifyHTTPStatus normalizes an HTTP response when a provider returned
// no richer typed error. A successful response returns nil; callers should
// handle 2xx success before routing failures. The mapping is shared by
// workers such as webhook delivery that receive an http.Response directly.
func ClassifyHTTPStatus(provider, operation string, status int, retryAfter time.Duration) *NormalizedError {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return nil
	}

	kind := ErrorKindPermanent
	code := string(MapHTTPStatus(status))
	retryable := false
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly:
		kind, code, retryable = ErrorKindTransient, "timeout", true
	case status == http.StatusTooManyRequests:
		kind, code, retryable = ErrorKindRateLimited, "rate_limited", true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind, code = ErrorKindAuth, "authentication_error"
	case status >= http.StatusInternalServerError && status < 600:
		kind, code, retryable = ErrorKindTransient, "provider_unavailable", true
	case status < 100:
		kind, code = ErrorKindPermanent, "unknown"
	}
	return &NormalizedError{
		Provider: provider, Operation: operation, Kind: kind, Code: code,
		HTTPStatus: status, Retryable: retryable, RetryAfter: retryAfter,
		Cause: fmt.Errorf("%s %s returned HTTP %d", provider, operation, status),
	}
}

// AuthenticationError is a provider-neutral wrapper for auth failures whose
// concrete sentinel belongs to another package (for example credentials.ErrInvalidGrant).
// It lets workers preserve typed auth semantics without creating an import cycle.
type AuthenticationError struct {
	Code  string
	Cause error
}

func (e *AuthenticationError) Error() string {
	if e == nil || e.Code == "" {
		return "authentication error"
	}
	return e.Code
}

func (e *AuthenticationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewAuthenticationError adapts a package-owned auth sentinel to the shared
// classifier while retaining errors.Is/errors.As access to the original cause.
func NewAuthenticationError(code string, cause error) error {
	return &AuthenticationError{Code: code, Cause: cause}
}

// ClassifyError converts the repository's provider-specific and legacy error
// shapes into one worker decision. Explicit transport/timeouts are transient;
// unknown ordinary errors are permanent so programming and validation bugs do
// not become unbounded retry loops.
func ClassifyError(err error) *NormalizedError {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return normalizedFrom(err, "", "context_canceled", 0, ErrorKindTransient, false, 0)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return normalizedFrom(err, "", "timeout", 0, ErrorKindTransient, true, retryAfterFromCarrier(err))
	}

	var authenticationErr *AuthenticationError
	if errors.As(err, &authenticationErr) {
		code := authenticationErr.Code
		if code == "" {
			code = "authentication_error"
		}
		return normalizedFrom(err, "", code, 0, ErrorKindAuth, false, 0)
	}
	if pe, ok := IsProviderError(err); ok {
		return classifyProviderError(err, pe)
	}
	var rateLimit *RateLimitError
	if errors.As(err, &rateLimit) {
		return normalizedFrom(err, "", "rate_limited", http.StatusTooManyRequests, ErrorKindRateLimited, true, rateLimit.RetryAfter)
	}
	var youtubeErr *YouTubeAPIError
	if errors.As(err, &youtubeErr) {
		return classifyHTTPShape(err, "youtube", youtubeErr.StatusCode, youtubeErr.Category, youtubeErr.Transient(), retryAfterFromCarrier(err))
	}
	var liveErr *YouTubeLiveError
	if errors.As(err, &liveErr) {
		return classifyYouTubeLiveError(err, liveErr)
	}
	var revokeErr *OAuthGrantRevocationError
	if errors.As(err, &revokeErr) {
		kind := ErrorKindPermanent
		if revokeErr.IsTransient() {
			kind = ErrorKindTransient
		}
		return normalizedFrom(err, "oauth", "oauth_revocation", revokeErr.StatusCode, kind, kind == ErrorKindTransient, revokeErr.RetryAfter)
	}

	// RetryAfterError is intentionally checked before the generic carrier:
	// an HTTP 503 wrapped with Retry-After is transient, not rate-limited.
	var retryAfterErr *RetryAfterError
	if errors.As(err, &retryAfterErr) {
		return normalizedFrom(err, "", "retry_after", 0, ErrorKindTransient, true, retryAfterErr.Delay)
	}
	var transient interface{ IsTransient() bool }
	if errors.As(err, &transient) && transient.IsTransient() {
		return normalizedFrom(err, "", "transient", 0, ErrorKindTransient, true, retryAfterFromCarrier(err))
	}
	var permanent interface{ IsPermanent() bool }
	if errors.As(err, &permanent) && permanent.IsPermanent() {
		return normalizedFrom(err, "", "permanent", 0, ErrorKindPermanent, false, 0)
	}
	if errors.Is(err, ErrPermanentUpload) || errors.Is(err, ErrPublishTerminal) || errors.Is(err, ErrPublishPermanent) {
		return normalizedFrom(err, "", "permanent", 0, ErrorKindPermanent, false, 0)
	}

	if status := statusFromLegacyError(err.Error()); status != 0 {
		code := MapHTTPStatus(status)
		return normalizedFrom(err, providerFromMessage(err.Error()), string(code), status, kindFromProviderCode(code), isRetryableProviderCode(code), retryAfterFromCarrier(err))
	}

	if netErr, ok := unwrapNetError(err); ok && (netErr.Timeout() || netErr.Temporary()) {
		return normalizedFrom(err, "", "network", 0, ErrorKindTransient, true, retryAfterFromCarrier(err))
	}
	if looksLikeTransientMessage(err.Error()) {
		return normalizedFrom(err, providerFromMessage(err.Error()), "transient", 0, ErrorKindTransient, true, retryAfterFromCarrier(err))
	}

	// Unknown errors are not silently treated as retryable. Providers should
	// return a typed error or wrap a transport error; an unrecognized value is
	// safer as permanent than an unbounded retry loop around a programming or
	// validation bug.
	return normalizedFrom(err, providerFromMessage(err.Error()), "unknown", 0, ErrorKindPermanent, false, retryAfterFromCarrier(err))
}

func classifyProviderError(err error, pe *ProviderError) *NormalizedError {
	kind := ErrorKindPermanent
	retryable := false
	switch pe.Code {
	case ErrorCodeRateLimited:
		kind, retryable = ErrorKindRateLimited, true
	case ErrorCodeAuthenticationError, ErrorCodePermissionMissing, ErrorCodeReauthenticationRequired:
		kind = ErrorKindAuth
	case ErrorCodeProviderUnavailable, ErrorCodeMediaProcessingFailed, ErrorCodeInternalError:
		kind, retryable = ErrorKindTransient, true
	case ErrorCodeValidationError, ErrorCodeContentRejected, ErrorCodeQuotaExceeded:
		kind = ErrorKindPermanent
	default:
		if pe.Retryable {
			kind, retryable = ErrorKindTransient, true
		}
	}
	return normalizedFrom(err, pe.Platform, string(pe.Code), pe.StatusCode, kind, retryable, pe.RetryAfter)
}

func classifyYouTubeLiveError(err error, liveErr *YouTubeLiveError) *NormalizedError {
	kind := ErrorKindPermanent
	retryable := false
	switch liveErr.Code {
	case YouTubeLiveRateLimited:
		kind, retryable = ErrorKindRateLimited, true
	case YouTubeLiveTransientUpstream:
		kind, retryable = ErrorKindTransient, true
	case YouTubeLiveAuthRequired, YouTubeLiveInsufficientScope, YouTubeLivePermissionBlocked:
		kind = ErrorKindAuth
	}
	if liveErr.StatusCode >= 500 {
		kind, retryable = ErrorKindTransient, true
	}
	return normalizedFrom(err, "youtube", string(liveErr.Code), liveErr.StatusCode, kind, retryable, liveErr.RetryAfter)
}

func classifyHTTPShape(err error, provider string, status int, category string, transient bool, retryAfter time.Duration) *NormalizedError {
	if status == http.StatusTooManyRequests || category == "rate_limit" {
		return normalizedFrom(err, provider, "rate_limited", status, ErrorKindRateLimited, true, retryAfter)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || category == "auth" {
		return normalizedFrom(err, provider, "authentication_error", status, ErrorKindAuth, false, retryAfter)
	}
	if transient || status >= 500 || category == "network" {
		return normalizedFrom(err, provider, "provider_unavailable", status, ErrorKindTransient, true, retryAfter)
	}
	return normalizedFrom(err, provider, "permanent", status, ErrorKindPermanent, false, retryAfter)
}

func normalizedFrom(cause error, provider, code string, status int, kind ErrorKind, retryable bool, retryAfter time.Duration) *NormalizedError {
	return &NormalizedError{Provider: provider, Code: code, HTTPStatus: status, Kind: kind, Retryable: retryable, RetryAfter: retryAfter, Cause: cause}
}

func retryAfterFromCarrier(err error) time.Duration {
	var carrier interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &carrier) {
		return carrier.RetryAfterDuration()
	}
	return 0
}

func isRetryableProviderCode(code ProviderErrorCode) bool {
	switch code {
	case ErrorCodeRateLimited, ErrorCodeProviderUnavailable, ErrorCodeMediaProcessingFailed, ErrorCodeInternalError:
		return true
	default:
		return false
	}
}

func kindFromProviderCode(code ProviderErrorCode) ErrorKind {
	switch code {
	case ErrorCodeRateLimited:
		return ErrorKindRateLimited
	case ErrorCodeAuthenticationError, ErrorCodePermissionMissing, ErrorCodeReauthenticationRequired:
		return ErrorKindAuth
	case ErrorCodeProviderUnavailable, ErrorCodeMediaProcessingFailed, ErrorCodeInternalError:
		return ErrorKindTransient
	default:
		return ErrorKindPermanent
	}
}

var (
	legacyStatusPattern   = regexp.MustCompile(`(?i)(?:status|http|returned)\s*[:=]?\s*(\d{3})`)
	bareHTTPStatusPattern = regexp.MustCompile(`\b([45]\d{2})\b`)
)

func statusFromLegacyError(message string) int {
	match := legacyStatusPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		match = bareHTTPStatusPattern.FindStringSubmatch(message)
	}
	if len(match) != 2 {
		return 0
	}
	status, err := strconv.Atoi(match[1])
	if err != nil || status < 100 || status > 599 {
		return 0
	}
	return status
}

func providerFromMessage(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "youtube"):
		return "youtube"
	case strings.Contains(lower, "drive") || strings.Contains(lower, "googleapis"):
		return "google_drive"
	case strings.Contains(lower, "nvidia"):
		return "nvidia"
	case strings.Contains(lower, "s3") || strings.Contains(lower, "minio"):
		return "storage"
	default:
		return ""
	}
}

func unwrapNetError(err error) (net.Error, bool) {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr, true
	}
	return nil, false
}

func looksLikeTransientMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"timeout", "timed out", "temporary", "temporarily", "network",
		"connection reset", "connection refused", "broken pipe", "eof",
		"unavailable", "transport", "i/o timeout",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
