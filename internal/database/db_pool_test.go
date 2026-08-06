package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

func TestNormalizePoolSettings_AppliesAllExplicitValues(t *testing.T) {
	settings := normalizePoolSettings(&config.DatabaseConfig{
		DBMaxOpenConns:           7,
		DBMaxIdleConns:           3,
		DBConnMaxLifetimeSeconds: 41,
		DBConnMaxIdleTimeSeconds: 19,
	})
	if settings.maxOpen != 7 || settings.maxIdle != 3 {
		t.Fatalf("pool limits: want open=7 idle=3, got open=%d idle=%d", settings.maxOpen, settings.maxIdle)
	}
	if settings.maxLifetime != 41*time.Second || settings.maxIdleTime != 19*time.Second {
		t.Fatalf("pool durations: want lifetime=41s idle=19s, got lifetime=%v idle=%v", settings.maxLifetime, settings.maxIdleTime)
	}
}

func TestNormalizePoolSettings_ClampsIdleToOpen(t *testing.T) {
	settings := normalizePoolSettings(&config.DatabaseConfig{
		DBMaxOpenConns: 2,
		DBMaxIdleConns: 9,
	})
	if settings.maxIdle != 2 {
		t.Fatalf("max idle: want clamp to 2, got %d", settings.maxIdle)
	}
}

func TestNormalizePoolSettings_UsesWorkerProfile(t *testing.T) {
	settings := normalizePoolSettings(&config.DatabaseConfig{
		DBMaxOpenConns: 25,
		DBMaxIdleConns: 5,
		DBPoolRole:     "worker",
		DBWorker:       config.DBPoolProfile{MaxOpenConns: 9, MaxIdleConns: 4, ConnMaxLifetimeSeconds: 60, ConnMaxIdleTimeSeconds: 30},
	})
	if settings.maxOpen != 9 || settings.maxIdle != 4 {
		t.Fatalf("worker profile limits: want open=9 idle=4, got open=%d idle=%d", settings.maxOpen, settings.maxIdle)
	}
	if settings.maxLifetime != 60*time.Second || settings.maxIdleTime != 30*time.Second {
		t.Fatalf("worker profile durations: want lifetime=60s idle=30s, got lifetime=%v idle=%v", settings.maxLifetime, settings.maxIdleTime)
	}
}

func TestNormalizePoolSettings_UsesMaintenanceProfile(t *testing.T) {
	settings := normalizePoolSettings(&config.DatabaseConfig{
		DBMaxOpenConns: 25,
		DBMaxIdleConns: 5,
		DBPoolRole:     config.DBPoolRoleMaintenance,
		DBMaintenance:  config.DBPoolProfile{MaxOpenConns: 3, MaxIdleConns: 1, ConnMaxLifetimeSeconds: 120, ConnMaxIdleTimeSeconds: 45},
	})
	if settings.maxOpen != 3 || settings.maxIdle != 1 || settings.maxLifetime != 120*time.Second || settings.maxIdleTime != 45*time.Second {
		t.Fatalf("maintenance profile: got %+v", settings)
	}
}

func TestConfigurePool_AppliesNormalizedMaxOpen(t *testing.T) {
	db, err := sql.Open(instrumentedPostgresDriverName, "host=127.0.0.1 port=1 user=test dbname=test sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	configurePool(db, &config.DatabaseConfig{DBMaxOpenConns: 7, DBMaxIdleConns: 3})
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections: want 7, got %d", got)
	}
}
