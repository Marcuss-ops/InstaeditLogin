// Package services — IsOAuthConnectionReadyForPublish is the
// canonical predicate helper that pins the 3 conditions for an
// OAuth-connection row to be considered publish-ready. It is the
// unit-tested spec the worker-side publish path should consult before
// each publish to surface stale / expired / scope-missing grants as a
// BLOCKED_AUTH signal instead of failing later on the platform API.
//
// The 3 conditions are taken verbatim from the canonical schema in
// internal/database/migrations/043_oauth_connections.sql:
//
//  1. oauth_connections.status = 'active'
//     The schema default is 'active'; the gateway flips the row to
//     'expired' / 'revoked' / 'disconnected' on terminal lifecycle
//     events (see internal/services/channel_authorization.go's
//     AuthorizeChannel + the admin reauth pipeline).
//
//  2. oauth_connections.expires_at > now()
//     The schema TIMESTAMPTZ column tracks the access-token's
//     material expiry (the vault stores the same value on tokens
//     via migration 053's FK lineage; the row-side value is the
//     authoritative cross-check). Strict `>`: an expires_at exactly
//     equal to now() is treated as expired (defensive edge case —
//     pro-rating the boundary into a TTL refresh the next tick).
//
//  3. granted_scopes contains the publish-required scope
//     Schema mapping: oauth_connections.scopes is a TEXT[] (a Go []string
//     round-trips via pq.Array). The publish-required scope is
//     parameterised via the requiredScope arg so this helper is
//     reusable across providers (e.g. facebook.pages_manage_posts,
//     tiktok.video.upload); the YouTube bind is
//     "https://www.googleapis.com/auth/youtube.upload" and is exposed
//     via youtubeOAuthScopes in youtube_oauth.go.
//
// The `now` argument is a clock parameter (NOT a time.Now call) so the
// helper stays pure and the unit tests in
// oauth_connection_validator_test.go can pin the boundary semantics
// deterministically.
//
// Workers should treat (status != 'active' || expires_at <= now() ||
// !contains(requiredScope, scopes)) as BLOCKED_AUTH-equivalent: it is
// the same canonical failure mode that the network-side
// YouTubeOAuthService.GetTokenInfo reports, just defended one layer
// earlier (before any HTTP round-trip to the platform).
//
// Currently published only for the platform_accounts side; the
// worker integration is tracked as a follow-up (the publish path
// already reads token freshness via vault.Renew, but the row-side
// cross-check is the canonical drift detector for grants that the
// vault hasn't refreshed yet).
package services

import "time"

// IsOAuthConnectionReadyForPublish returns true when an OAuth-connection
// row satisfies the canonical schema-inferred publish-ready invariant:
//
//  1. status == "active"
//  2. expires_at > now
//  3. scopes contains requiredScope (full-match; no URL-prefix matching)
//  4. now is non-zero (defensive — a zero now reads as "no expiry check
//     available" and we fail-closed to false)
//
// Returns false on any single-condition failure. The single false exit
// lets callers compose the predicate OR'd into larger eligibility
// gates without needing to consult three different signals.
//
// The requiredScope argument is the FULL canonical scope URL (e.g.
// "https://www.googleapis.com/auth/youtube.upload"). The match is
// exact-string; "youtube.upload" will NOT match
// "https://www.googleapis.com/auth/youtube.upload" — callers should
// pass the canonical form (matches OAuth providers' on-the-wire shape
// + youtubeOAuthScopes in youtube_oauth.go).
//
// Time semantics:
//
//   - now == zero  : treated as "missing" → false (fail-closed). A
//     future caller may want optimistic mode; today's
//     semantics are conservative because every test in
//     oauth_connection_validator_test.go would
//     otherwise collapse to true.
//   - expiresAt == zero : treated as "no expiry recorded" → false.
//     The migration is NOT NULL on writes but DEFAULTS
//     are unsafe to read; this guard rejects the
//     degenerate case explicitly.
//   - expiresAt == now : false (strict `>` semantics — see godoc).
func IsOAuthConnectionReadyForPublish(status string, expiresAt time.Time, scopes []string, requiredScope string, now time.Time) bool {
	if status != "active" {
		return false
	}
	if now.IsZero() {
		return false
	}
	if expiresAt.IsZero() {
		return false
	}
	if !expiresAt.After(now) {
		return false
	}
	if requiredScope == "" {
		// Defensive: an unparameterised required scope would silently
		// accept every row. The caller is misusing the helper; fail
		// closed instead of pretending the predicate is satisfied.
		return false
	}
	for _, s := range scopes {
		if s == requiredScope {
			return true
		}
	}
	return false
}
