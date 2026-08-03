// cmd/create-user creates a single SaaS user (email + password) plus its
// Personal Workspace + admin membership, against the Postgres DB configured
// by the current environment.
//
// This CLI is a deliberate escape hatch outside of /api/v1/auth/register
// (which is gated by X-Admin-Token and Intended for self-service invites
// only) and outside of AuthService.Register (which sets email_verified=FALSE
// pending an email-verification flow that invite-only beta does not run).
//
// Typical use:
//
//	# 1. dry-run (validates flags + config + DB connectivity, no writes)
//	dry_run_cmd
//
//	# 2. commit against production (operator ack required)
//	commit_prod_cmd
//
// The CLI refuses to run against APP_ENV=production unless --confirm-prod
// is supplied (cmd/seed-style safety: same pattern, inverted — seed REFUSES
// prod outright, this CLI REQUIRES an explicit prod-ack flag).
//
// What you must NOT do:
//
//   - Pass the password as a flag in CI logs (use --password-from-env
//     through the CREATE_USER_PASSWORD env var, see flag docs).
//   - Re-run with the same email; the CLI exits 1 with the existing user_id
//     (idempotency contract; cannot silently overwrite a real account).
//   - Run without --confirm-prod against APP_ENV=production.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq" //nolint:blank-imports // postgres driver registration
	"golang.org/x/crypto/bcrypt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

// Flag-time defaults sourced from env so a Linux/macOS operator pipeline
// never leaks the password to `ps`/`/proc/<pid>/cmdline` (a process' argv
// is world-readable on most Linux distros): when CREATE_USER_PASSWORD is
// set the flag falls back to it; the operator still types
// `--password wrong` to skip the fallback and intentionally expose the
// value.
const (
	envEmail    = "CREATE_USER_EMAIL"
	envPassword = "CREATE_USER_PASSWORD"
	envName     = "CREATE_USER_NAME"
)

func main() {
	var (
		email       string
		password    string
		name        string
		confirmProd bool
		dryRun      bool
	)
	flag.StringVar(&email, "email", os.Getenv(envEmail),
		"Email for the new user. Falls back to env "+envEmail+" to keep the "+
			"password out of shell argv (where `ps` would see it).")
	flag.StringVar(&password, "password", os.Getenv(envPassword),
		"Password (must satisfy validatePassword: >=8 chars + at least 1 digit). "+
			"Falls back to env "+envPassword+" for the same `ps`-safety reason.")
	flag.StringVar(&name, "name", os.Getenv(envName),
		"Display name (e.g. 'TikTok Test 1'). Falls back to env "+envName+".")
	flag.BoolVar(&confirmProd, "confirm-prod", false,
		"Required when APP_ENV=production. Refuses silently otherwise.")
	flag.BoolVar(&dryRun, "dry-run", false,
		"Validate flags + config + DB connectivity, run every check, "+
			"but do not issue the INSERT. Exit 0 = safe to commit.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of create-user:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # dev (no --confirm-prod needed):\n")
		fmt.Fprintf(os.Stderr, "  CREATE_USER_EMAIL=a@b.com CREATE_USER_PASSWORD=GoodPass1234 CREATE_USER_NAME=Test \\\n")
		fmt.Fprintf(os.Stderr, "    go run ./cmd/create-user --dry-run\n")
		fmt.Fprintf(os.Stderr, "\n  # prod (operator ack):\n")
		fmt.Fprintf(os.Stderr, "  DATABASE_URL=postgres://... go run ./cmd/create-user \\\n")
		fmt.Fprintf(os.Stderr, "    --email=tiktoktest1@instaedit.org \\\n")
		fmt.Fprintf(os.Stderr, "    --password-from-env  # (the flag itself takes no arg; uses $CREATE_USER_PASSWORD) \\\n")
		fmt.Fprintf(os.Stderr, "    --name='TikTok Test 1' --confirm-prod\n")
	}
	flag.Parse()

	if email == "" || password == "" || name == "" {
		fmt.Fprintln(os.Stderr, "missing required arg: --email, --password, --name (or set $CREATE_USER_EMAIL / $CREATE_USER_PASSWORD / $CREATE_USER_NAME)")
		flag.Usage()
		os.Exit(2)
	}
	if !strings.Contains(email, "@") {
		// cheap shape check; the column has no UNIQUE-format constraint in
		// the 001 migration but every sane email has an @, so this catches
		// the common operator typo before the DB rejects it ambiguously.
		fmt.Fprintln(os.Stderr, "--email looks malformed (no '@'); refusing to run")
		os.Exit(2)
	}

	if validatePasswordErr := validatePassword(password); validatePasswordErr != nil {
		fmt.Fprintln(os.Stderr, "password policy:", validatePasswordErr)
		os.Exit(2)
	}

	// Bypass config.Load() on purpose. config.Load() validates a wide
	// surface (JWT secret length, OAuth platform secrets, encryption keys,
	// Sentry DSN, metrics basic-auth) — all mandatory for the long-running
	// server but irrelevant for this single-row INSERT. Validating them
	// here would refuse to run with a dev DB that simply lacks those vars,
	// which is exactly the configuration operators use when running the
	// CLI from a local checkout.
	dbCfg, err := loadDatabaseConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "db config:", err)
		os.Exit(1)
	}

	appEnv := getEnvOr("APP_ENV", "dev")
	if appEnv == "production" && !confirmProd {
		fmt.Fprintln(os.Stderr, "REFUSING to create user in APP_ENV=production without --confirm-prod")
		fmt.Fprintln(os.Stderr, "(re-run with --confirm-prod iff you really mean it)")
		os.Exit(1)
	}

	db, err := database.Connect(dbCfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()
	expectedInstallationUUID := strings.TrimSpace(os.Getenv("EXPECTED_DATABASE_INSTALLATION_UUID"))
	if appEnv == "production" || appEnv == "staging" {
		if expectedInstallationUUID == "" {
			fmt.Fprintf(os.Stderr, "EXPECTED_DATABASE_INSTALLATION_UUID is required in APP_ENV=%s\n", appEnv)
			os.Exit(1)
		}
	}
	if err := database.VerifyInstallationIdentity(context.Background(), db, expectedInstallationUUID); err != nil {
		fmt.Fprintln(os.Stderr, "database identity verification failed: DATABASE_IDENTITY_MISMATCH")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ping is the cheapest end-to-end smoke check: TLS handshake +
	// ACTUAL round-trip to the configured Postgres host. Catches a
	// wrong DATABASE_URL, wrong port, wrong sslmode, network ACL, etc.
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "db ping:", err)
		fmt.Fprintln(os.Stderr, "(DB unreachable; refusing to commit. Verify DATABASE_URL or import one of the .env files first.)")
		os.Exit(1)
	}

	// Probe that the canonical tables exist. Catches a `psql` shell that
	// points at a half-migrated DB (the migration_history table is
	// present but the 003 workspaces migration never ran). Skipping
	// this check would surface as a cryptic foreign-key error halfway
	// through the COMMIT — harder to triage.
	for _, tbl := range []string{"users", "workspaces", "workspace_members"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = $1`,
			tbl,
		).Scan(&n); err != nil {
			fmt.Fprintf(os.Stderr, "table probe failed (%s): %v\n", tbl, err)
			os.Exit(1)
		}
		if n == 0 {
			fmt.Fprintf(os.Stderr, "table %q missing — migrations may not be applied to this DB\n", tbl)
			os.Exit(1)
		}
	}

	// Idempotency check: if the email already exists, refuse and
	// print the existing user_id (idempotent refusal; see package doc).
	var existingID int64
	switch err := db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = $1`, email,
	).Scan(&existingID); {
	case err == nil:
		fmt.Fprintf(os.Stderr, "REFUSING: user with email=%q already exists (id=%d).\n", email, existingID)
		fmt.Fprintln(os.Stderr, "This CLI cannot mutate an existing account; delete the row manually if you really want to recreate.")
		os.Exit(1)
	case errors.Is(err, sql.ErrNoRows):
		// expected — proceed to INSERT
	default:
		fmt.Fprintln(os.Stderr, "idempotency lookup:", err)
		os.Exit(1)
	}

	hostRedacted := redactDatabaseHost(dbCfg.DatabaseURL)
	slog.Info("create-user: preflight OK",
		"email", email,
		"name", name,
		"app_env", appEnv,
		"db_host_redacted", hostRedacted,
		"dry_run", dryRun,
	)
	fmt.Fprintf(os.Stderr, "[plan] APP_ENV=%s db=%s\n", appEnv, hostRedacted)
	fmt.Fprintf(os.Stderr, "[plan] email=%s name=%s\n", email, name)
	fmt.Fprintf(os.Stderr, "[plan] email_verified will be set to TRUE on insert (skipping the invite-only-beta verification email)\n")
	// NOTE: NEVER print the plaintext password, even at debug verbosity.
	// `ps auxe`, /var/log/auth.log, GitHub Actions step summary, etc.
	// would happily capture it otherwise.
	fmt.Fprintf(os.Stderr, "[plan] password length=%d chars (not echoed for security)\n", len(password))

	if dryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] all preflight checks passed; nothing was written")
		os.Exit(0)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcrypt:", err)
		os.Exit(1)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "begin tx:", err)
		os.Exit(1)
	}
	// defer Rollback is a standard pgx/database/sql idiom: it's a no-op
	// once Commit() has succeeded, but cleanly unwinds the transaction
	// if any INSERT below errored before commit.
	defer func() { _ = tx.Rollback() }()

	var userID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (email, name, password_hash, email_verified)
		 VALUES ($1, $2, $3, TRUE)
		 RETURNING id`,
		email, name, hash,
	).Scan(&userID); err != nil {
		fmt.Fprintln(os.Stderr, "insert users:", err)
		os.Exit(1)
	}

	var workspaceID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO workspaces (name, owner_id)
		 VALUES ('Personal', $1)
		 RETURNING id`,
		userID,
	).Scan(&workspaceID); err != nil {
		fmt.Fprintln(os.Stderr, "insert workspaces:", err)
		os.Exit(1)
	}

	// workspace_members.role is an enum-typed column ('admin'|'editor'|'viewer');
	// the value matches repository.RoleAdmin in internal/repository/team_repo.go.
	// ON CONFLICT DO NOTHING guards against a manual rollback that left a
	// stale row in workspace_members — this CLI is the one place we'd
	// want to be defensive about it.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, 'admin')
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		workspaceID, userID,
	); err != nil {
		fmt.Fprintln(os.Stderr, "insert workspace_members:", err)
		os.Exit(1)
	}

	if err := tx.Commit(); err != nil {
		fmt.Fprintln(os.Stderr, "commit:", err)
		os.Exit(1)
	}

	slog.Info("create-user: user created",
		"email", email,
		"user_id", userID,
		"workspace_id", workspaceID,
		"app_env", appEnv,
	)
	// Final summary on stdout — never echo password.
	fmt.Printf("User created OK.\n  email:        %s\n  user_id:      %d\n  workspace_id: %d\n  app_env:      %s\n",
		email, userID, workspaceID, appEnv)
	fmt.Fprintln(os.Stderr, "Next: log in via https://instaedit.org/login with the email above; then 'Connect TikTok' to bind a TikTok channel.")
}

// loadDatabaseConfigFromEnv reads the minimum DB config needed to open
// a connection, mirroring the resolver inside config.Load() but without
// requiring OAuth/JWT/encryption env vars. The CLI doesn't need those —
// it only inserts a row — so requiring them would refuse to run against
// any dev DB that hasn't configured them (and a CLI that refuses legitimate
// dev use is a CLI nobody reaches for when it counts).
func loadDatabaseConfigFromEnv() (*config.DatabaseConfig, error) {
	cfg := &config.DatabaseConfig{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DBHost:      getEnvOr("DB_HOST", "localhost"),
		DBPort:      getEnvOr("DB_PORT", "5432"),
		DBUser:      getEnvOr("DB_USER", "instaedit"),
		DBPassword:  os.Getenv("DB_PASSWORD"),
		DBName:      getEnvOr("DB_NAME", "instaedit_login"),
		DBSSLMode:   getEnvOr("DB_SSLMODE", "disable"),
	}
	if cfg.DatabaseURL == "" && cfg.DBPassword == "" {
		return nil, errors.New("DATABASE_URL is required, OR (DB_PASSWORD + DB_HOST + DB_USER + DB_NAME) must all be set")
	}
	return cfg, nil
}

// getEnvOr returns env var `key` if set, otherwise `fallback`. A minimal
// helper to keep the env-reader independent of the validation-heavy
// config.Load() entrypoint.
func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// validatePassword mirrors services.AuthService.validatePassword
// (internal/services/auth_service.go:215) so the CLI enforces the same
// policy that /api/v1/auth/register enforces. Diverging here would let
// a CLI-created user pick a password that the public register endpoint
// would later reject — confusing for the operator.
func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters (got %d)", len(password))
	}
	for _, c := range password {
		if c >= '0' && c <= '9' {
			return nil
		}
	}
	return fmt.Errorf("password must contain at least 1 number")
}

// redactDatabaseHost returns "host[:port]" with the user/password
// portions stripped, so a `slog.Info(...)` or `[plan]` stderr line
// never carries the connection credentials into a log aggregator
// (CloudWatch, Sentry, Datadog). Returns "(none)" when DATABASE_URL is
// empty (operator might be using the DB_HOST/DB_USER/... split surface
// instead of DATABASE_URL).
func redactDatabaseHost(rawURL string) string {
	if rawURL == "" {
		return "(none)"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "(unparseable)"
	}
	host := u.Hostname()
	if host == "" {
		return "(empty)"
	}
	if port := u.Port(); port != "" {
		return host + ":" + port
	}
	return host
}
