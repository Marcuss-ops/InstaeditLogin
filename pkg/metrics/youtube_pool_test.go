package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gatherFamily returns the MetricFamily for name from the default
// registry, or nil when absent.
func gatherFamily(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

// TestYouTubeOAuthPoolMetrics_RefreshTokensActive_RegisteredAndLabeled
// pins the registration + label contract of the active-grant gauge: a
// Set call must materialize one series carrying the truncated subject
// and the pool client key, with the given value.
func TestYouTubeOAuthPoolMetrics_RefreshTokensActive_RegisteredAndLabeled(t *testing.T) {
	ResetYouTubeOAuthRefreshTokensActiveMetrics()
	SetYouTubeOAuthRefreshTokensActive("google-subject-1", "youtube_pool_b", 43)

	family := gatherFamily(t, "youtube_oauth_refresh_tokens_active")
	if family == nil {
		t.Fatal("youtube_oauth_refresh_tokens_active: not present in gatherer after Set call")
	}
	if family.GetType() != dto.MetricType_GAUGE {
		t.Errorf("youtube_oauth_refresh_tokens_active: want gauge type, got %v", family.GetType())
	}
	metrics := family.GetMetric()
	if len(metrics) != 1 {
		t.Fatalf("youtube_oauth_refresh_tokens_active: want exactly 1 series, got %d", len(metrics))
	}
	if got := metrics[0].GetGauge().GetValue(); got != 43 {
		t.Errorf("youtube_oauth_refresh_tokens_active value: want 43, got %v", got)
	}
	labels := map[string]string{}
	for _, lp := range metrics[0].GetLabel() {
		labels[lp.GetName()] = lp.GetValue()
	}
	if labels["google_subject"] != "google-subject-1" {
		t.Errorf("google_subject label: want google-subject-1, got %q", labels["google_subject"])
	}
	if labels["oauth_client_key"] != "youtube_pool_b" {
		t.Errorf("oauth_client_key label: want youtube_pool_b, got %q", labels["oauth_client_key"])
	}
}

// TestYouTubeOAuthPoolMetrics_CapacityRemaining_ComputedValue pins the
// capacity gauge setter (recommended capacity minus active grants is
// computed by the collector, not the setter — the setter stores what
// it is given, including negative headroom).
func TestYouTubeOAuthPoolMetrics_CapacityRemaining_RegisteredAndValue(t *testing.T) {
	ResetYouTubeOAuthPoolCapacityRemainingMetrics()
	SetYouTubeOAuthPoolCapacityRemaining("google-subject-1", "youtube_pool_a", -5)

	family := gatherFamily(t, "youtube_oauth_pool_capacity_remaining")
	if family == nil {
		t.Fatal("youtube_oauth_pool_capacity_remaining: not present in gatherer after Set call")
	}
	metrics := family.GetMetric()
	if len(metrics) != 1 {
		t.Fatalf("youtube_oauth_pool_capacity_remaining: want exactly 1 series, got %d", len(metrics))
	}
	if got := metrics[0].GetGauge().GetValue(); got != -5 {
		t.Errorf("youtube_oauth_pool_capacity_remaining value: want -5, got %v", got)
	}
}

// TestYouTubeOAuthPoolMetrics_InvalidGrantTotal_Counter pins the
// counter family: each Record call increments the per-client series.
func TestYouTubeOAuthPoolMetrics_InvalidGrantTotal_Counter(t *testing.T) {
	youtubeOAuthInvalidGrantTotal.Reset()
	RecordYouTubeOAuthInvalidGrant("youtube_pool_b")
	RecordYouTubeOAuthInvalidGrant("youtube_pool_b")
	RecordYouTubeOAuthInvalidGrant("")

	family := gatherFamily(t, "youtube_oauth_invalid_grant_total")
	if family == nil {
		t.Fatal("youtube_oauth_invalid_grant_total: not present in gatherer after Record calls")
	}
	if family.GetType() != dto.MetricType_COUNTER {
		t.Errorf("youtube_oauth_invalid_grant_total: want counter type, got %v", family.GetType())
	}
	values := map[string]float64{}
	for _, m := range family.GetMetric() {
		key := ""
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "oauth_client_key" {
				key = lp.GetValue()
			}
		}
		values[key] = m.GetCounter().GetValue()
	}
	if values["youtube_pool_b"] != 2 {
		t.Errorf("youtube_oauth_invalid_grant_total{youtube_pool_b}: want 2, got %v", values["youtube_pool_b"])
	}
	if values["youtube_pool_a"] != 1 {
		t.Errorf("youtube_oauth_invalid_grant_total with empty key must default to youtube_pool_a: want 1, got %v", values["youtube_pool_a"])
	}
}

// TestYouTubeOAuthPoolMetrics_ReauthRequiredChannels_Registered pins the
// reauth-required gauge registration + value.
func TestYouTubeOAuthPoolMetrics_ReauthRequiredChannels_Registered(t *testing.T) {
	ResetYouTubeOAuthReauthRequiredChannelsMetrics()
	SetYouTubeOAuthReauthRequiredChannels("google-subject-1", 3)

	family := gatherFamily(t, "youtube_oauth_reauth_required_channels")
	if family == nil {
		t.Fatal("youtube_oauth_reauth_required_channels: not present in gatherer after Set call")
	}
	metrics := family.GetMetric()
	if len(metrics) != 1 {
		t.Fatalf("youtube_oauth_reauth_required_channels: want exactly 1 series, got %d", len(metrics))
	}
	if got := metrics[0].GetGauge().GetValue(); got != 3 {
		t.Errorf("youtube_oauth_reauth_required_channels value: want 3, got %v", got)
	}
}

// TestYouTubeOAuthPoolMetrics_ResetRemovesSeries pins the reset
// contract: after Reset, a revoked grant's series disappears from the
// next scrape. An empty GaugeVec emits no family at all from the
// client_golang gatherer (metrics only exist for live label children),
// which is exactly the "missing series is more honest than a stale
// count" contract.
func TestYouTubeOAuthPoolMetrics_ResetRemovesSeries(t *testing.T) {
	SetYouTubeOAuthRefreshTokensActive("google-subject-x", "youtube_pool_a", 12)
	ResetYouTubeOAuthRefreshTokensActiveMetrics()

	family := gatherFamily(t, "youtube_oauth_refresh_tokens_active")
	if family != nil && len(family.GetMetric()) != 0 {
		t.Errorf("youtube_oauth_refresh_tokens_active: want 0 series after Reset, got %d", len(family.GetMetric()))
	}
}
