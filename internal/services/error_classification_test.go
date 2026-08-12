package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestClassifyError_Table(t *testing.T) {
	cause := errors.New("bug sentinel")
	cases := []struct {
		name       string
		err        error
		kind       ErrorKind
		retryable  bool
		code       string
		status     int
		retryAfter time.Duration
	}{
		{
			name: "provider 429 always rate limited",
			err: &ProviderError{
				Code: ErrorCodeRateLimited, Platform: "youtube", StatusCode: http.StatusTooManyRequests,
				Retryable: false, RetryAfter: 42 * time.Second,
			},
			kind: ErrorKindRateLimited, retryable: true, code: "rate_limited", status: http.StatusTooManyRequests, retryAfter: 42 * time.Second,
		},
		{
			name: "provider 503 transient even when hint is absent",
			err: &ProviderError{
				Code: ErrorCodeProviderUnavailable, Platform: "google_drive", StatusCode: http.StatusServiceUnavailable,
				Retryable: false,
			},
			kind: ErrorKindTransient, retryable: true, code: "provider_unavailable", status: http.StatusServiceUnavailable,
		},
		{
			name: "provider auth 401",
			err: &ProviderError{
				Code: ErrorCodeAuthenticationError, Platform: "youtube", StatusCode: http.StatusUnauthorized,
				Retryable: true,
			},
			kind: ErrorKindAuth, retryable: false, code: "authentication_error", status: http.StatusUnauthorized,
		},
		{
			name: "provider permission 403",
			err: &ProviderError{
				Code: ErrorCodePermissionMissing, Platform: "youtube", StatusCode: http.StatusForbidden,
			},
			kind: ErrorKindAuth, retryable: false, code: "permission_missing", status: http.StatusForbidden,
		},
		{
			name: "provider validation permanent",
			err:  &ProviderError{Code: ErrorCodeValidationError, Platform: "youtube", StatusCode: http.StatusBadRequest},
			kind: ErrorKindPermanent, retryable: false, code: "validation_error", status: http.StatusBadRequest,
		},
		{
			name: "legacy youtube 429",
			err:  &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "redacted"},
			kind: ErrorKindRateLimited, retryable: true, code: "rate_limited", status: http.StatusTooManyRequests,
		},
		{
			name: "legacy youtube 5xx",
			err:  &YouTubeAPIError{StatusCode: http.StatusBadGateway, Category: "server_error", Message: "redacted"},
			kind: ErrorKindTransient, retryable: true, code: "provider_unavailable", status: http.StatusBadGateway,
		},
		{
			name: "legacy youtube auth",
			err:  &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "redacted"},
			kind: ErrorKindAuth, retryable: false, code: "authentication_error", status: http.StatusUnauthorized,
		},
		{
			name: "legacy status 503",
			err:  fmt.Errorf("nvidia returned HTTP 503"),
			kind: ErrorKindTransient, retryable: true, code: "provider_unavailable", status: http.StatusServiceUnavailable,
		},
		{
			name: "legacy rate limit",
			err:  &RateLimitError{RetryAfter: 9 * time.Second},
			kind: ErrorKindRateLimited, retryable: true, code: "rate_limited", status: http.StatusTooManyRequests, retryAfter: 9 * time.Second,
		},
		{
			name: "permanent upload sentinel",
			err:  fmt.Errorf("upload failed: %w", ErrPermanentUpload),
			kind: ErrorKindPermanent, retryable: false, code: "permanent",
		},
		{
			name: "deadline is transient",
			err:  context.DeadlineExceeded,
			kind: ErrorKindTransient, retryable: true, code: "timeout",
		},
		{
			name: "cancellation is not retryable",
			err:  context.Canceled,
			kind: ErrorKindTransient, retryable: false, code: "context_canceled",
		},
		{
			name: "unknown network message is transient",
			err:  errors.New("network reset while calling provider"),
			kind: ErrorKindTransient, retryable: true, code: "transient",
		},
		{
			name: "unknown programming error is permanent",
			err:  cause,
			kind: ErrorKindPermanent, retryable: false, code: "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.err)
			if got == nil {
				t.Fatal("ClassifyError returned nil")
			}
			if got.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.kind)
			}
			if got.Retryable != tc.retryable {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tc.retryable)
			}
			if got.Code != tc.code {
				t.Errorf("Code = %q, want %q", got.Code, tc.code)
			}
			if got.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus = %d, want %d", got.HTTPStatus, tc.status)
			}
			if tc.retryAfter > 0 && got.RetryAfter != tc.retryAfter {
				t.Errorf("RetryAfter = %s, want %s", got.RetryAfter, tc.retryAfter)
			}
			if !errors.Is(got, tc.err) {
				t.Errorf("normalized error does not preserve cause: got %v", got)
			}
		})
	}
}

func TestClassifyError_ProviderRateLimitOverridesRetryableFlag(t *testing.T) {
	err := &ProviderError{Code: ErrorCodeRateLimited, Retryable: false, RetryAfter: time.Minute}
	if !IsErrorKind(err, ErrorKindRateLimited) {
		t.Fatal("rate_limited code must remain rate_limited even when Retryable=false")
	}
	if IsErrorKind(err, ErrorKindPermanent) {
		t.Fatal("rate_limited must never be permanent")
	}
}

func TestClassifyError_PermanentPublishError(t *testing.T) {
	cause := errors.New("provider rejected the publish")
	err := NewPermanentPublishError(cause)
	got := ClassifyError(err)
	if got.Kind != ErrorKindPermanent || got.Retryable {
		t.Fatalf("got kind=%q retryable=%v, want permanent/false", got.Kind, got.Retryable)
	}
	if !errors.Is(got, cause) {
		t.Fatal("permanent wrapper cause was not preserved")
	}
}
