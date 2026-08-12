package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestClassifyUploadError_UsesNormalizedProviderKinds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "429",
			err: &services.ProviderError{
				Code: services.ErrorCodeRateLimited, Platform: "youtube", StatusCode: 429,
				RetryAfter: time.Minute,
			},
			want: "rate_limited",
		},
		{
			name: "5xx",
			err: &services.ProviderError{
				Code: services.ErrorCodeProviderUnavailable, Platform: "youtube", StatusCode: 503,
			},
			want: "youtube_error",
		},
		{
			name: "auth",
			err: &services.ProviderError{
				Code: services.ErrorCodeAuthenticationError, Platform: "youtube", StatusCode: 401,
			},
			want: "auth_error",
		},
		{
			name: "timeout",
			err:  errors.New("context deadline exceeded: timeout"),
			want: "timeout",
		},
		{
			name: "permanent artifact contract",
			err:  PermanentError{Code: CodeArtifactMIMEMismatch, Message: "wrong mime"},
			want: "permanent_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyUploadError(tc.err); got != tc.want {
				t.Fatalf("classifyUploadError() = %q, want %q", got, tc.want)
			}
		})
	}
}
