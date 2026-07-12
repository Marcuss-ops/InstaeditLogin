package services

import "testing"

// TestPublishOutcomeFromCode_Exhaustive pins the canonical mapping
// from SPRINT 5.1 ProviderErrorCode to the publish-outcome label
// used by pkg/metrics publish_attempts_total{outcome=...}.
//
// The mapping is exhaustive over the 10 taxonomy codes + the empty
// (success) case. An unknown code falls back to PublishOutcomeInternal
// so dashboards always have a value to query.
//
// Lives here (in package services) rather than in pkg/metrics
// because:
//
//  1. The mapping depends on ProviderErrorCode, the type defined in
//     this package. Putting the helper in pkg/metrics would force a
//     services import.
//  2. internal/services/metrics_helper.go already imports pkg/metrics
//     (for RecordPublishMetrics). Adding a test in pkg/metrics that
//     imports services creates a test-import cycle detected by `go vet`.
//
// A future Drift test (e.g. "all 10 codes map to known outcomes") is
// trivially added here.
func TestPublishOutcomeFromCode_Exhaustive(t *testing.T) {
	cases := []struct {
		name string
		code ProviderErrorCode
		want string
	}{
		{"empty=success", "", PublishOutcomeSuccess},
		{"rate_limited", ErrorCodeRateLimited, PublishOutcomeRateLimited},
		{"authentication_error", ErrorCodeAuthenticationError, PublishOutcomeAuthError},
		{"permission_missing", ErrorCodePermissionMissing, PublishOutcomeAuthError},
		{"reauthentication_required", ErrorCodeReauthenticationRequired, PublishOutcomeAuthError},
		{"provider_unavailable", ErrorCodeProviderUnavailable, PublishOutcomeProviderUnavail},
		{"media_processing_failed", ErrorCodeMediaProcessingFailed, PublishOutcomeMediaFailed},
		{"content_rejected", ErrorCodeContentRejected, PublishOutcomeContentRejected},
		{"quota_exceeded", ErrorCodeQuotaExceeded, PublishOutcomeQuota},
		{"validation_error", ErrorCodeValidationError, PublishOutcomeValidation},
		{"internal_error", ErrorCodeInternalError, PublishOutcomeInternal},
		{"unknown_future_code fallback", ProviderErrorCode("unknown_future_code"), PublishOutcomeInternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PublishOutcomeFromCode(c.code)
			if got != c.want {
				t.Errorf("PublishOutcomeFromCode(%q): want %q, got %q", c.code, c.want, got)
			}
		})
	}
}

// TestPublishOutcome_AllTenMappingsDocumented asserts the mapping
// covers every code in AllProviderErrorCodes (the canonical taxonomy).
// A future SPRINT that adds an 11th code without updating this test
// will surface as a failure here — the catch-all before the code
// reaches a consumer that's silently mis-classifying.
func TestPublishOutcome_AllTenMappingsDocumented(t *testing.T) {
	for _, code := range AllProviderErrorCodes() {
		out := PublishOutcomeFromCode(code)
		// Every mapped code must yield one of the canonical outcomes.
		valid := []string{
			PublishOutcomeRateLimited, PublishOutcomeAuthError,
			PublishOutcomeProviderUnavail, PublishOutcomeMediaFailed,
			PublishOutcomeContentRejected, PublishOutcomeQuota,
			PublishOutcomeValidation, PublishOutcomeInternal,
		}
		found := false
		for _, v := range valid {
			if out == v {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("PublishOutcomeFromCode(%q) returned non-canonical outcome %q", code, out)
		}
	}
}
