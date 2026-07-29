package services

import (
	"strings"
	"testing"
	"time"
)

// youtubeUploadScope mirrors the canonical scope the YouTube OAuth
// flow requests: video uploads via the chunked-PUT resumable upload
// protocol. Mirrored from the youtubeOAuthScopes constant in
// youtube_oauth.go::180 so a future rename surfaces here as a test
// failure (test-side VCS guard for the cross-file invariant).
const youtubeUploadScope = "https://www.googleapis.com/auth/youtube.upload"

// TestIsOAuthConnectionReadyForPublish pins the 3-condition invariant
// documented in internal/database/migrations/043_oauth_connections.sql:
//
//  1. oauth_connections.status = 'active'
//  2. oauth_connections.expires_at > now()
//  3. granted_scopes contains the publish-required scope
//
// (with two defensive fail-closed guards from the helper's godoc that
// the suite also locks in).
//
// This is the worker-side pre-flight that the publish path SHOULD
// consult before each publish to surface stale / expired / scope-
// missing grants as BLOCKED_AUTH — equivalent to the network-side
// YouTubeOAuthService.GetTokenInfo reports but at the DB row layer
// (faster, no HTTP round-trip, drift-detected before the platform is
// contacted). Each subtest name maps 1:1 to the rule it's pinning so
// the failure log is self-explanatory.
//
// The matrix is exhaustive over the orthogonal failure axes:
//   - status (4 values: active + 3 non-active)
//   - expires_at relative to now (3 brackets: future / exactly now / past / zero)
//   - scopes content (3 shapes: contains required / missing / empty)
//
// Total: 11 subcases + the 2 negative-defensive cases below.
func TestIsOAuthConnectionReadyForPublish(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	futureExpiry := now.Add(1 * time.Hour)
	pastExpiry := now.Add(-1 * time.Hour)
	happyScopes := []string{
		"openid",
		"https://www.googleapis.com/auth/email",
		youtubeUploadScope,
		"https://www.googleapis.com/auth/youtube.force-ssl",
	}

	cases := []struct {
		name          string
		status        string
		expiresAt     time.Time
		scopes        []string
		requiredScope string
		nowParam      time.Time
		want          bool
	}{
		// Happy path: every condition holds → ready.
		{
			name:          "active_status_+_future_expiry_+_present_scope",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          true,
		},

		// Status axis: any non-active value → not ready, regardless
		// of expires_at / scopes (orthogonal axes verified elsewhere).
		{
			name:          "expired_status_rejects_active_predicate",
			status:        "expired",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "revoked_status_rejects_active_predicate",
			status:        "revoked",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "disconnected_status_rejects_active_predicate",
			status:        "disconnected",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},

		// Expiry axis: anything <= now → not ready.
		{
			name:          "past_expiry_rejects_active_predicate",
			status:        "active",
			expiresAt:     pastExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "exact_now_expiry_rejects_active_predicate",
			status:        "active",
			expiresAt:     now, // strict `>` semantics
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "zero_expiry_rejects_active_predicate",
			status:        "active",
			expiresAt:     time.Time{}, // migration NOT NULL but defensive guard
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},

		// Scope axis: missing required scope → not ready, even when
		// other conditions hold (orthogonal — every other axis is OK).
		{
			name:          "missing_youtube_upload_scope_rejects",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        []string{"openid", "https://www.googleapis.com/auth/youtube.readonly", "https://www.googleapis.com/auth/youtube.force-ssl"},
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "empty_scopes_rejects",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        []string{},
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false,
		},
		{
			name:          "short_alias_youtube_upload_NOT_matched",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        []string{"youtube.upload"}, // NOT the canonical full URL
			requiredScope: youtubeUploadScope,
			nowParam:      now,
			want:          false, // full-string match rule
		},

		// Defensive fail-closed guards.
		{
			name:          "zero_now_rejects",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: youtubeUploadScope,
			nowParam:      time.Time{},
			want:          false,
		},
		{
			name:          "empty_required_scope_rejects",
			status:        "active",
			expiresAt:     futureExpiry,
			scopes:        happyScopes,
			requiredScope: "",
			nowParam:      now,
			want:          false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := IsOAuthConnectionReadyForPublish(
				tc.status,
				tc.expiresAt,
				tc.scopes,
				tc.requiredScope,
				tc.nowParam,
			)
			if got != tc.want {
				t.Errorf("IsOAuthConnectionReadyForPublish(%q, %s, %v, %q, now) = %v, want %v",
					tc.status,
					tc.expiresAt.Format(time.RFC3339Nano),
					tc.scopes,
					tc.requiredScope,
					got,
					tc.want,
				)
			}
		})
	}
}

// TestIsOAuthConnectionReadyForPublish_YouTubeCanonicalScope is a
// document-style guard: re-asserts that the test scope constant matches
// the canonical YouTube OAuth scope URL exported in
// youtube_oauth.go. If a future maintainer edits the canonical scope
// (a deliberate blast-radius event — touches every cross-repo Go
// file that ships the consent screen) THIS test must be updated in the
// SAME commit. The compiler-side constant `youtubeOAuthScopes` cannot be
// imported here because it's an unexported const; the substring match
// is the next-best VCS guard.
func TestIsOAuthConnectionReadyForPublish_YouTubeCanonicalScope(t *testing.T) {
	mustContain := []string{
		"https://www.googleapis.com/auth/youtube.upload",
		"https://www.googleapis.com/auth/youtube.readonly",
		"https://www.googleapis.com/auth/youtube.force-ssl",
		"openid",
		"email",
	}
	for _, want := range mustContain {
		if !strings.Contains(youtubeOAuthScopes, want) {
			t.Errorf("youtubeOAuthScopes must contain %q (drift between this test scope and the production consent-screen scope would surface as 4xx from Google's tokeninfo endpoint); got %q",
				want, youtubeOAuthScopes)
		}
	}
	if !strings.HasPrefix(youtubeUploadScope, "https://www.googleapis.com/auth/") {
		t.Errorf("youtubeUploadScope test constant %q must start with the canonical auth host prefix; this is the string the helper's full-match rule compares against", youtubeUploadScope)
	}
}
