package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRequestStats_AccumulatesSQLWork(t *testing.T) {
	stats := NewRequestStats()
	ctx := WithRequestStats(context.Background(), stats)
	ObserveSQL(ctx, 12*time.Millisecond)
	ObserveSQL(ctx, 8*time.Millisecond)

	if got := stats.SQLQueries(); got != 2 {
		t.Fatalf("SQLQueries: want 2, got %d", got)
	}
	if got := stats.SQLDuration(); got != 20*time.Millisecond {
		t.Fatalf("SQLDuration: want 20ms, got %s", got)
	}
}

func TestRecordGoogleAPICall_ClassifiesResult(t *testing.T) {
	operation := "measurement_test_google"
	metric := googleAPICallsTotal.WithLabelValues(operation, "success")
	before := testutil.ToFloat64(metric)

	RecordGoogleAPICall(operation, 200, 25*time.Millisecond)

	if got := testutil.ToFloat64(metric); got != before+1 {
		t.Fatalf("google_api_calls_total success: want %v, got %v", before+1, got)
	}
}

func TestMediaProcessGauge_IsBalanced(t *testing.T) {
	process := "measurement_test_media"
	metric := mediaProcessesActive.WithLabelValues(process)
	before := testutil.ToFloat64(metric)

	StartMediaProcess(process)
	if got := testutil.ToFloat64(metric); got != before+1 {
		t.Fatalf("active media processes after start: want %v, got %v", before+1, got)
	}
	EndMediaProcess(process)
	if got := testutil.ToFloat64(metric); got != before {
		t.Fatalf("active media processes after end: want %v, got %v", before, got)
	}
}

func TestDatabasePoolConfigured_RecordsProfileLimits(t *testing.T) {
	profile := "measurement_test_profile"
	maxOpen := databasePoolConfigured.WithLabelValues(profile, "max_open")
	maxIdle := databasePoolConfigured.WithLabelValues(profile, "max_idle")
	beforeOpen := testutil.ToFloat64(maxOpen)
	beforeIdle := testutil.ToFloat64(maxIdle)

	SetDatabasePoolConfigured(profile, 12, 4)
	if got := testutil.ToFloat64(maxOpen); got != 12 {
		t.Fatalf("configured max_open: want 12, got %v (before %v)", got, beforeOpen)
	}
	if got := testutil.ToFloat64(maxIdle); got != 4 {
		t.Fatalf("configured max_idle: want 4, got %v (before %v)", got, beforeIdle)
	}
}
