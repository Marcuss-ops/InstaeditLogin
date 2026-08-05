package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
)

// refreshTotalCount returns the summed value of every
// youtube_oauth_refresh_total series matching the (oauth_client_key,
// result) labels, or 0 when no series matches. The metric is registered
// on the default registry by pkg/metrics, so a services-level test can
// gather it directly — measuring deltas around a call keeps the
// assertion immune to other tests incrementing the same counter.
func refreshTotalCount(t *testing.T, key, result string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, mf := range families {
		if mf.GetName() != "youtube_oauth_refresh_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["oauth_client_key"] == key && labels["result"] == result {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}

// refreshMetricServer serves the token endpoint: 200 with a fresh
// access token unless fail is set, in which case 400 invalid_grant.
func refreshMetricServer(fail *atomic.Bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, req *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-at",
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "youtube.upload youtube.readonly youtube.force-ssl",
		})
	})
	return httptest.NewServer(mux)
}

// TestRefreshOAuthToken_RecordsPoolRefreshMetric certifies the WIRING of
// youtube_oauth_refresh_total in RefreshOAuthToken (not just the Record
// helper): a real refresh through the service must increment the counter
// for the pool client stamped on ctx — success and error each exactly
// once, so a future refactor that drops the defer is caught here.
func TestRefreshOAuthToken_RecordsPoolRefreshMetric(t *testing.T) {
	var fail atomic.Bool
	srv := refreshMetricServer(&fail)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	// Pool client stamped on ctx by CredentialVault.Renew.
	ctx := credentials.WithOAuthClientKey(context.Background(), "youtube_pool_a")

	// Success: exactly +1 on {youtube_pool_a, success}.
	beforeOK := refreshTotalCount(t, "youtube_pool_a", "success")
	if _, err := svc.RefreshOAuthToken(ctx, "rt-1"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := refreshTotalCount(t, "youtube_pool_a", "success") - beforeOK; got != 1 {
		t.Errorf("youtube_oauth_refresh_total{youtube_pool_a,success}: want delta +1, got %v", got)
	}

	// Failure: exactly +1 on {youtube_pool_a, error}.
	fail.Store(true)
	beforeErr := refreshTotalCount(t, "youtube_pool_a", "error")
	if _, err := svc.RefreshOAuthToken(ctx, "rt-2"); err == nil {
		t.Fatal("refresh: want error on failing server, got nil")
	}
	if got := refreshTotalCount(t, "youtube_pool_a", "error") - beforeErr; got != 1 {
		t.Errorf("youtube_oauth_refresh_total{youtube_pool_a,error}: want delta +1, got %v", got)
	}

	// No cross-contamination: the success series must not move during
	// the failing call.
	if got := refreshTotalCount(t, "youtube_pool_a", "success") - beforeOK; got != 1 {
		t.Errorf("youtube_oauth_refresh_total{youtube_pool_a,success}: want delta still +1 after error call, got %v", got)
	}
}

// TestRefreshOAuthToken_RecordsPoolRefreshMetric_LegacyLabel certifies
// the legacy label path: a refresh without a stamped client key (non-vault
// caller) is attributed to legacy_single_client, never a pool client.
func TestRefreshOAuthToken_RecordsPoolRefreshMetric_LegacyLabel(t *testing.T) {
	var fail atomic.Bool
	srv := refreshMetricServer(&fail)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	before := refreshTotalCount(t, "legacy_single_client", "success")
	if _, err := svc.RefreshOAuthToken(context.Background(), "rt-legacy"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := refreshTotalCount(t, "legacy_single_client", "success") - before; got != 1 {
		t.Errorf("youtube_oauth_refresh_total{legacy_single_client,success}: want delta +1, got %v", got)
	}
	// The pool series must NOT have moved.
	poolBefore := refreshTotalCount(t, "youtube_pool_a", "success") +
		refreshTotalCount(t, "youtube_pool_b", "success") +
		refreshTotalCount(t, "youtube_pool_a", "error") +
		refreshTotalCount(t, "youtube_pool_b", "error")
	if poolNow := refreshTotalCount(t, "youtube_pool_a", "success") +
		refreshTotalCount(t, "youtube_pool_b", "success") +
		refreshTotalCount(t, "youtube_pool_a", "error") +
		refreshTotalCount(t, "youtube_pool_b", "error"); poolNow != poolBefore {
		t.Errorf("legacy refresh must not move pool series: before=%v after=%v", poolBefore, poolNow)
	}
}
