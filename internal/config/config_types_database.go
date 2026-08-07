package config

// DatabaseConfig holds PostgreSQL configuration.
type DatabaseConfig struct {
	// DatabaseURL for production; individual fields (DB_HOST, DB_PORT,
	// DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE) are kept for local
	// tooling. DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	// ExpectedInstallationUUID pins this process to the PostgreSQL
	// installation it was configured for. Production and staging
	// require EXPECTED_DATABASE_INSTALLATION_UUID; local dev may leave
	// it empty while the migration bootstrap creates the identity row.
	ExpectedInstallationUUID string

	// Legacy/default database/sql pool sizing. Kept for direct callers and
	// backwards-compatible configurations; process profiles below take
	// precedence when DBPoolRole is set.
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	DBConnMaxLifetimeSeconds int
	DBConnMaxIdleTimeSeconds int

	// DBPoolRole selects the process profile: api, worker, or maintenance.
	// The profiles keep API capacity isolated from background workers while
	// making the total connection budget explicit.
	DBPoolRole    string
	DBAPI         DBPoolProfile
	DBWorker      DBPoolProfile
	DBMaintenance DBPoolProfile
}

// DBPoolProfile is one explicit database/sql pool budget for a process role.
type DBPoolProfile struct {
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeSeconds int
	ConnMaxIdleTimeSeconds int
}
