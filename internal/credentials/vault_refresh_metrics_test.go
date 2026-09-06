package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// refreshMetricValue sums the gathered default-registry series for the
// given metric/label set (deltas keep assertions immune to other tests).
// Mirrors the cross-package pattern in internal/services
// (youtube_oauth_refresh_metric_test.go) since the metric vars are
// unexported in pkg/metrics.
func refreshMetricValue(t *testing.T, name string, wantLabels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			match := true
			for k, v := range wantLabels {
				if labels[k] != v {
					match = false
					break
				}
			}
			if match {
				total += m.GetCounter().GetValue()
				total += m.GetGauge().GetValue()
				total += float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return total
}

// Tests for the vault refresh observability wiring (C6). Contract:
//
//  1. Every slow-path renewal (singleflight leader) increments
//     vault_refresh_flights_total exactly once.
//  2. Every completed renewal observes wall-clock latency in
//     vault_refresh_slow_path_duration_seconds with a bounded outcome
//     label: success | error | cancelled — never provider error text.
//
// The SQL mock sequences mirror TestVault_Renew_SlowPath_* (the
// proven slow-path contract): BEGIN → connection lock → advisory lock →
// client-key lookup → COMMIT/ROLLBACK.

func TestVault_Renew_Metrics_SuccessOutcome(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 71
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOAuthClientKeyLookup(mock, accountID, "youtube_pool_a")
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID) // final v.Get

	flightsBefore := refreshMetricValue(t, "vault_refresh_flights_total", map[string]string{"token_type": models.TokenTypeBearer})
	successBefore := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "success", "token_type": models.TokenTypeBearer})

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return &models.TokenData{AccessToken: "fresh-access", TokenType: "bearer", ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if got := refreshMetricValue(t, "vault_refresh_flights_total", map[string]string{"token_type": models.TokenTypeBearer}); got != flightsBefore+1 {
		t.Errorf("flights_total delta = %v, want 1", got-flightsBefore)
	}
	if got := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "success", "token_type": models.TokenTypeBearer}); got != successBefore+1 {
		t.Errorf("slow_path success observations delta = %v, want 1", got-successBefore)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestVault_Renew_Metrics_ErrorOutcome(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 72
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOAuthClientKeyLookup(mock, accountID, "youtube_pool_a")
	mock.ExpectRollback()

	errorBefore := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "error", "token_type": models.TokenTypeBearer})

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return nil, errors.New("simulated platform 500")
	})
	if err == nil {
		t.Fatal("expected error from failing refresher, got nil")
	}
	if got := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "error", "token_type": models.TokenTypeBearer}); got != errorBefore+1 {
		t.Errorf("slow_path error observations delta = %v, want 1", got-errorBefore)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestVault_Renew_Metrics_CancelledOutcome(t *testing.T) {
	v, _, store := newTestVault(t)
	const accountID int64 = 73
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)
	// NOTE: no expectOauthConnLookup — the pre-cancelled context exits at
	// the top of renew, before the grant lookup runs.

	cancelledBefore := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "cancelled", "token_type": models.TokenTypeBearer})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := v.Renew(ctx, accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return nil, errors.New("refresher must not run on cancelled ctx")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Renew err = %v, want context.Canceled", err)
	}
	if got := refreshMetricValue(t, "vault_refresh_slow_path_duration_seconds", map[string]string{"outcome": "cancelled", "token_type": models.TokenTypeBearer}); got != cancelledBefore+1 {
		t.Errorf("slow_path cancelled observations delta = %v, want 1", got-cancelledBefore)
	}
}

func TestOutcomeForRenew(t *testing.T) {
	if got := outcomeForRenew(nil); got != "success" {
		t.Errorf("outcomeForRenew(nil) = %q, want success", got)
	}
	if got := outcomeForRenew(errors.New("provider down")); got != "error" {
		t.Errorf("outcomeForRenew(err) = %q, want error (bounded label, no provider text)", got)
	}
}
