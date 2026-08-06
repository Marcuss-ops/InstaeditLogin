package config

import (
	"strings"
	"testing"
)

func TestLoad_DatabasePoolSettingsRoundTrip(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DB_MAX_OPEN_CONNS", "17")
	t.Setenv("DB_MAX_IDLE_CONNS", "6")
	t.Setenv("DB_CONN_MAX_LIFETIME_SECONDS", "901")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_SECONDS", "73")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := cfg.Database.DBMaxOpenConns; got != 17 {
		t.Errorf("DBMaxOpenConns: want 17, got %d", got)
	}
	if got := cfg.Database.DBMaxIdleConns; got != 6 {
		t.Errorf("DBMaxIdleConns: want 6, got %d", got)
	}
	if got := cfg.Database.DBConnMaxLifetimeSeconds; got != 901 {
		t.Errorf("DBConnMaxLifetimeSeconds: want 901, got %d", got)
	}
	if got := cfg.Database.DBConnMaxIdleTimeSeconds; got != 73 {
		t.Errorf("DBConnMaxIdleTimeSeconds: want 73, got %d", got)
	}
}

func TestLoad_DatabasePoolSettingsDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONN_MAX_LIFETIME_SECONDS", "")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Database.DBMaxOpenConns != 25 || cfg.Database.DBMaxIdleConns != 5 {
		t.Errorf("pool defaults: got open=%d idle=%d", cfg.Database.DBMaxOpenConns, cfg.Database.DBMaxIdleConns)
	}
	if cfg.Database.DBConnMaxLifetimeSeconds != 1800 || cfg.Database.DBConnMaxIdleTimeSeconds != 300 {
		t.Errorf("pool duration defaults: got lifetime=%d idle=%d", cfg.Database.DBConnMaxLifetimeSeconds, cfg.Database.DBConnMaxIdleTimeSeconds)
	}
}

func TestLoad_DatabasePoolProfilesRoundTrip(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DB_POOL_ROLE", "worker")
	t.Setenv("DB_API_MAX_OPEN_CONNS", "15")
	t.Setenv("DB_WORKER_MAX_OPEN_CONNS", "9")
	t.Setenv("DB_WORKER_MAX_IDLE_CONNS", "4")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	profile, ok := cfg.Database.Profile()
	if !ok || profile.MaxOpenConns != 9 || profile.MaxIdleConns != 4 {
		t.Fatalf("worker profile: got (%+v, %v)", profile, ok)
	}
	if cfg.Database.DBAPI.MaxOpenConns != 15 {
		t.Fatalf("API profile: want 15, got %d", cfg.Database.DBAPI.MaxOpenConns)
	}
}

func TestValidate_DatabasePoolRoleRejectsUnknown(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.Database.DBPoolRole = "unknown"
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "DB_POOL_ROLE") {
		t.Fatalf("validate(): want DB_POOL_ROLE error, got %v", err)
	}
}

func TestValidate_DatabasePoolProfileRejectsOversizedIdle(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.Database.DBAPI = DBPoolProfile{MaxOpenConns: 2, MaxIdleConns: 3, ConnMaxLifetimeSeconds: 60, ConnMaxIdleTimeSeconds: 60}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "DB_API_MAX_IDLE_CONNS") {
		t.Fatalf("validate(): want DB_API_MAX_IDLE_CONNS error, got %v", err)
	}
}

func TestValidate_DatabasePoolSettingsRejectInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DatabaseConfig)
		want   string
	}{
		{"open connections", func(c *DatabaseConfig) { c.DBMaxOpenConns = -1 }, "DB_MAX_OPEN_CONNS"},
		{"idle exceeds open", func(c *DatabaseConfig) { c.DBMaxOpenConns, c.DBMaxIdleConns = 2, 3 }, "DB_MAX_IDLE_CONNS"},
		{"lifetime", func(c *DatabaseConfig) { c.DBConnMaxLifetimeSeconds = -1 }, "DB_CONN_MAX_LIFETIME_SECONDS"},
		{"idle time", func(c *DatabaseConfig) { c.DBConnMaxIdleTimeSeconds = -1 }, "DB_CONN_MAX_IDLE_TIME_SECONDS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			tc.mutate(&cfg.Database)
			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate(): want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
