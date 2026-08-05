package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// YouTube OAuth Client Pool — Prometheus surface for the fleet of
// YouTube channels under one (or more) Google manager accounts.
//
// Google caps refresh tokens at 100 per (Google account, OAuth client)
// pair, so grants are spread across pool clients (youtube_pool_a /
// youtube_pool_b — see internal/services/youtube_oauth_client_pool.go).
// The collector (collector.go::collectYouTubeOAuthPoolMetrics) refreshes
// the gauge families once per tick inside the single-flighted collect
// tx; the invalid_grant counter is incremented at the detection site
// (internal/credentials/vault_refresh.go).
//
// Label contract:
//   - google_subject    — the granter's stable Google OIDC `sub` claim,
//     truncated via TruncateSubjectForLabel() (≤64 chars, sha256 for
//     long ids) to keep series cardinality bounded and safe.
//   - oauth_client_key  — the pool client that issued the grant
//     ("youtube_pool_a" / "youtube_pool_b").
//
// Operator health bands (per client, active refresh grants):
//
//	0–60 healthy · 61–75 warning · 76–85 high · 86–90 critical ·
//	>90 blocked (no new connections) — mirrors the
//	YouTubeOAuthPool*Threshold constants in internal/services.
const (
	// YouTubeOAuthPoolRecommendedCapacity is the soft per-client ceiling
	// used to compute youtube_oauth_pool_capacity_remaining. Kept in
	// sync with services.defaultYouTubePoolCapacity (50); the collector
	// computes remaining as this constant minus the active-grant count.
	YouTubeOAuthPoolRecommendedCapacity = 50
)

var (
	// youtubeOAuthRefreshTokensActive is the number of ACTIVE refresh
	// grants per (google_subject, oauth_client_key). The count that
	// Google's 100-token cap is measured against.
	youtubeOAuthRefreshTokensActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "youtube_oauth_refresh_tokens_active",
			Help: "Active YouTube OAuth refresh grants per Google subject and pool client. Google caps refresh tokens at 100 per (account, client) pair; operator bands: 0-60 healthy, 61-75 warning, 76-85 high, 86-90 critical, above 90 blocks new connections.",
		},
		[]string{"google_subject", "oauth_client_key"},
	)

	// youtubeOAuthPoolCapacityRemaining is the theoretical headroom of a
	// pool client for a Google subject: recommended capacity (50) minus
	// active refresh grants. Negative when the soft ceiling is exceeded.
	youtubeOAuthPoolCapacityRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "youtube_oauth_pool_capacity_remaining",
			Help: "Theoretical headroom per (google_subject, oauth_client_key): recommended capacity (50) minus active refresh grants. Negative once the soft ceiling is exceeded.",
		},
		[]string{"google_subject", "oauth_client_key"},
	)

	// youtubeOAuthInvalidGrantTotal counts invalid_grant detections per
	// pool client. Each increment means Google rejected the stored
	// refresh token (revoked, expired beyond TTL, or exceeding the cap);
	// the affected grant + sibling channels are flagged reauth_required.
	youtubeOAuthInvalidGrantTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "youtube_oauth_invalid_grant_total",
			Help: "YouTube OAuth refresh attempts rejected with invalid_grant, by pool client. Each increment flags the grant (and its sibling channels) reauth_required.",
		},
		[]string{"oauth_client_key"},
	)

	// youtubeOAuthReauthRequiredChannels is the number of YouTube
	// channels currently in status='reauth_required' per Google subject.
	youtubeOAuthReauthRequiredChannels = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "youtube_oauth_reauth_required_channels",
			Help: "YouTube platform_accounts in status='reauth_required' per Google subject. Each unit is a channel the operator must reconnect before the next publish.",
		},
		[]string{"google_subject"},
	)
)

func init() {
	prometheus.MustRegister(
		youtubeOAuthRefreshTokensActive,
		youtubeOAuthPoolCapacityRemaining,
		youtubeOAuthInvalidGrantTotal,
		youtubeOAuthReauthRequiredChannels,
	)
}

// --- Record helpers ------------------------------------------------------

// SetYouTubeOAuthRefreshTokensActive writes one series for a Google
// subject + pool client. The subject is label-truncated via
// TruncateSubjectForLabel before entering the label set.
func SetYouTubeOAuthRefreshTokensActive(subject, oauthClientKey string, count int64) {
	youtubeOAuthRefreshTokensActive.WithLabelValues(TruncateSubjectForLabel(subject), oauthClientKey).Set(float64(count))
}

// ResetYouTubeOAuthRefreshTokensActiveMetrics clears ALL series on the
// gauge so a revoked grant stops emitting on the next scrape. Called
// once per collector tick BEFORE the SET loop (missing series is more
// honest than a stale count).
func ResetYouTubeOAuthRefreshTokensActiveMetrics() {
	youtubeOAuthRefreshTokensActive.Reset()
}

// SetYouTubeOAuthPoolCapacityRemaining writes the headroom series for
// a Google subject + pool client (recommended capacity − active grants).
func SetYouTubeOAuthPoolCapacityRemaining(subject, oauthClientKey string, remaining int64) {
	youtubeOAuthPoolCapacityRemaining.WithLabelValues(TruncateSubjectForLabel(subject), oauthClientKey).Set(float64(remaining))
}

// ResetYouTubeOAuthPoolCapacityRemainingMetrics clears ALL series on
// the capacity-remaining gauge (see the active-grant reset contract).
func ResetYouTubeOAuthPoolCapacityRemainingMetrics() {
	youtubeOAuthPoolCapacityRemaining.Reset()
}

// RecordYouTubeOAuthInvalidGrant increments the invalid_grant counter
// for the pool client that issued the rejected grant.
func RecordYouTubeOAuthInvalidGrant(oauthClientKey string) {
	if oauthClientKey == "" {
		oauthClientKey = "youtube_pool_a"
	}
	youtubeOAuthInvalidGrantTotal.WithLabelValues(oauthClientKey).Inc()
}

// SetYouTubeOAuthReauthRequiredChannels writes the count of channels in
// status='reauth_required' for a Google subject.
func SetYouTubeOAuthReauthRequiredChannels(subject string, count int64) {
	youtubeOAuthReauthRequiredChannels.WithLabelValues(TruncateSubjectForLabel(subject)).Set(float64(count))
}

// ResetYouTubeOAuthReauthRequiredChannelsMetrics clears ALL series on
// the reauth-required gauge (see the active-grant reset contract).
func ResetYouTubeOAuthReauthRequiredChannelsMetrics() {
	youtubeOAuthReauthRequiredChannels.Reset()
}
