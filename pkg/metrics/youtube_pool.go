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

	// YouTubeOAuthPoolHealthyThreshold: 0–60 active grants = healthy.
	YouTubeOAuthPoolHealthyThreshold int64 = 60
	// YouTubeOAuthPoolWarningThreshold: 61–75 = warning.
	YouTubeOAuthPoolWarningThreshold int64 = 75
	// YouTubeOAuthPoolHighThreshold: 76–85 = high.
	YouTubeOAuthPoolHighThreshold int64 = 85
	// YouTubeOAuthPoolCriticalThreshold: 86–90 = critical. Active
	// grants ABOVE this threshold block new connections on that client.
	YouTubeOAuthPoolCriticalThreshold int64 = 90
)

// YouTubeOAuthPoolHealthFor maps an active-grant count to its
// youtube_oauth_pool_health level: 0=healthy, 1=warning, 2=high,
// 3=critical, 4=blocked. Mirrors services.YouTubeOAuthPoolHealthFor
// (kept in sync here because pkg/metrics must not import
// internal/services).
func YouTubeOAuthPoolHealthFor(active int64) float64 {
	switch {
	case active > YouTubeOAuthPoolCriticalThreshold:
		return 4 // blocked (no new connections)
	case active > YouTubeOAuthPoolHighThreshold:
		return 3 // critical
	case active > YouTubeOAuthPoolWarningThreshold:
		return 2 // high
	case active > YouTubeOAuthPoolHealthyThreshold:
		return 1 // warning
	default:
		return 0 // healthy
	}
}

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
	// (google_subject, pool client). Each increment means Google
	// rejected the stored refresh token (revoked, expired beyond TTL, or
	// exceeding the cap); the affected grant + sibling channels are
	// flagged reauth_required.
	youtubeOAuthInvalidGrantTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "youtube_oauth_invalid_grant_total",
			Help: "YouTube OAuth refresh attempts rejected with invalid_grant, per Google subject and pool client. Each increment flags the grant (and its sibling channels) reauth_required.",
		},
		[]string{"google_subject", "oauth_client_key"},
	)

	// youtubeOAuthPoolHealth is the load band of a pool client: the
	// worst band observed across every Google subject on that client
	// (0=healthy, 1=warning, 2=high, 3=critical, 4=blocked). Label is
	// per client so the collector can zero-fill every configured client
	// from the pool registry Keys() — a configured-but-unused client
	// emits 0 (healthy) instead of disappearing from /metrics.
	youtubeOAuthPoolHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "youtube_oauth_pool_health",
			Help: "YouTube OAuth pool client load band (worst across its Google subjects): 0 healthy (0-60 active grants), 1 warning (61-75), 2 high (76-85), 3 critical (86-90), 4 blocked (>90, no new connections).",
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

	// youtubeOAuthRefreshTotal counts every YouTube OAuth refresh
	// attempt per pool client and outcome. result ∈ {success, error};
	// the oauth_client_key label is the pool client that issued the
	// grant (the key stamped on ctx by vault.Renew) or
	// legacy_single_client for the pre-pool / non-pool path. Unlike
	// youtube_oauth_invalid_grant_total (which only counts rejected
	// grants), this counter observes the full refresh volume so the
	// operator can compute per-client success/failure rates and spot a
	// client silently failing (e.g. revoked secret, misconfigured
	// client) before invalid_grant detections stack up.
	youtubeOAuthRefreshTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "youtube_oauth_refresh_total",
			Help: "YouTube OAuth refresh attempts per pool client and result (success|error). Use rate(youtube_oauth_refresh_total{result=\"error\"}[5m]) / rate(youtube_oauth_refresh_total[5m]) for the per-client error ratio.",
		},
		[]string{"oauth_client_key", "result"},
	)
)

// YouTubeOAuthRefreshResult labels for youtube_oauth_refresh_total.
const (
	YouTubeOAuthRefreshResultSuccess = "success"
	YouTubeOAuthRefreshResultError   = "error"
)

// LegacyYouTubeOAuthClientKeyLabel is the oauth_client_key label value
// for refreshes that did NOT go through a pool client (legacy
// single-client deployments, pre-pool rows, non-vault callers). Kept
// distinct from "youtube_pool_a" so the pool distribution stays honest:
// a legacy refresh must never be attributed to a pool client that did
// not issue the grant.
const LegacyYouTubeOAuthClientKeyLabel = "legacy_single_client"

func init() {
	prometheus.MustRegister(
		youtubeOAuthRefreshTokensActive,
		youtubeOAuthPoolCapacityRemaining,
		youtubeOAuthInvalidGrantTotal,
		youtubeOAuthPoolHealth,
		youtubeOAuthReauthRequiredChannels,
		youtubeOAuthRefreshTotal,
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
// for the (google_subject, pool client) that issued the rejected
// grant. The subject is label-truncated like every other subject label;
// an empty subject is never a valid label (fail-closed: no increment).
func RecordYouTubeOAuthInvalidGrant(subject, oauthClientKey string) {
	if oauthClientKey == "" {
		oauthClientKey = "youtube_pool_a"
	}
	if subject == "" {
		return
	}
	youtubeOAuthInvalidGrantTotal.WithLabelValues(TruncateSubjectForLabel(subject), oauthClientKey).Inc()
}

// SetYouTubeOAuthPoolHealth writes the health band (0-4) for a pool
// client.
func SetYouTubeOAuthPoolHealth(oauthClientKey string, level float64) {
	youtubeOAuthPoolHealth.WithLabelValues(oauthClientKey).Set(level)
}

// ResetYouTubeOAuthPoolHealthMetrics clears ALL series on the health
// gauge. Called once per collector tick BEFORE the SET loop.
func ResetYouTubeOAuthPoolHealthMetrics() {
	youtubeOAuthPoolHealth.Reset()
}

// SetYouTubeOAuthReauthRequiredChannels writes the count of channels in
// status='reauth_required' for a Google subject.
func SetYouTubeOAuthReauthRequiredChannels(subject string, count int64) {
	youtubeOAuthReauthRequiredChannels.WithLabelValues(TruncateSubjectForLabel(subject)).Set(float64(count))
}

// RecordYouTubeOAuthRefresh increments youtube_oauth_refresh_total for
// the pool client that handled the refresh. result must be one of
// YouTubeOAuthRefreshResultSuccess / YouTubeOAuthRefreshResultError —
// an unknown result is normalized to error (fail-closed: a future
// typo must never inflate the success count). An empty client key
// (legacy single-client path) is labeled
// LegacyYouTubeOAuthClientKeyLabel — never the empty string, never a
// pool client that did not issue the grant.
func RecordYouTubeOAuthRefresh(oauthClientKey, result string) {
	if oauthClientKey == "" {
		oauthClientKey = LegacyYouTubeOAuthClientKeyLabel
	}
	if result != YouTubeOAuthRefreshResultSuccess {
		result = YouTubeOAuthRefreshResultError
	}
	youtubeOAuthRefreshTotal.WithLabelValues(oauthClientKey, result).Inc()
}

// ResetYouTubeOAuthReauthRequiredChannelsMetrics clears ALL series on
// the reauth-required gauge (see the active-grant reset contract).
func ResetYouTubeOAuthReauthRequiredChannelsMetrics() {
	youtubeOAuthReauthRequiredChannels.Reset()
}
