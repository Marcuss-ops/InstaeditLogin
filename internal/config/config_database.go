package config

import "fmt"

const (
	DBPoolRoleAPI         = "api"
	DBPoolRoleWorker      = "worker"
	DBPoolRoleMaintenance = "maintenance"
)

func (p DBPoolProfile) isZero() bool {
	return p.MaxOpenConns == 0 && p.MaxIdleConns == 0 &&
		p.ConnMaxLifetimeSeconds == 0 && p.ConnMaxIdleTimeSeconds == 0
}

// Profile resolves the selected role's pool profile. An unset role keeps
// the legacy DB_* settings, preserving compatibility for tools and tests.
func (c *DatabaseConfig) Profile() (DBPoolProfile, bool) {
	if c == nil {
		return DBPoolProfile{}, false
	}
	switch c.DBPoolRole {
	case DBPoolRoleAPI:
		return c.DBAPI, true
	case DBPoolRoleWorker:
		return c.DBWorker, true
	case DBPoolRoleMaintenance:
		return c.DBMaintenance, true
	default:
		return DBPoolProfile{}, false
	}
}

// DSN returns the PostgreSQL connection string.
func (c *DatabaseConfig) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}
