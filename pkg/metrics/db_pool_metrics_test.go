package metrics

import (
	"database/sql"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDatabasePoolWaitMetrics_MapDBStats(t *testing.T) {
	databasePoolUsage.Reset()
	collectPoolGaugesFromStats(sql.DBStats{
		WaitCount:    11,
		WaitDuration: 2*time.Second + 750*time.Millisecond,
	})

	if got := testutil.ToFloat64(databasePoolUsage.WithLabelValues(PoolStateWait)); got != 11 {
		t.Errorf("database_pool_usage{state=wait}: want 11, got %v", got)
	}
	if got := testutil.ToFloat64(databasePoolWaitDurationSeconds); got != 2.75 {
		t.Errorf("database_pool_wait_duration_seconds: want 2.75, got %v", got)
	}
}
