// Package repository_test — integration tests for
// RefreshSweepRepository.ListDormantRefreshGrants against a real
// PostgreSQL database.
//
// These tests are SKIPPED by default (no network, no Docker). They run
// only when INSTAEDIT_TEST_PG_URL points to a reachable Postgres — the
// same gate as internal/credentials/vault_integration_test.go:
//
//	export INSTAEDIT_TEST_PG_URL='postgresql://instaedit:dev_password@localhost:5432/instaedit_login?sslmode=disable'
//	make dev   # in another shell, to start the DB
//	go test -v ./internal/repository/ -run TestRefreshSweep_Integration
//
// What these prove that the sqlmock tests (refresh_sweep_repo_test.go)
// CANNOT:
//
//  1. The selection predicate behaves correctly against REAL rows:
//     dormant grants (never-refreshed with old created_at, stale
//     last_refresh_at, or provider TTL inside the 7-day window) are
//     selected; fresh / reauth_required / revoked / borderline grants
//     are NOT.
//  2. The horizonDays parameter actually changes the selection.
//  3. A multi-channel grant (one oauth_connection backing N
//     platform_accounts) returns one row per account — the worker's
//     per-oauth_connection dedup is the caller-side responsibility.
//  4. The replica single-flight (pg_try_advisory_xact_lock with
//     RefreshSweepLockID) really is cluster-wide: while a concurrent
//     transaction holds the lock, ListDormantRefreshGrantsSingleFlighted
//     reports won=false and runs NO selection; after the holder
//     commits, the same repository wins and returns the grants.
//
// There is deliberately NO `//go:build integration` tag: the env var
// is the gate, mirroring the credentials package's integration tests
// (see the note in vault_integration_test.go for the rationale).
//
// CAVEAT: internal/credentials/vault_integration_test.go truncates
// the SAME tables on the SAME shared database. The two packages are
// only safe to run TOGETHER when the runner serialises packages
// (e.g. `go test ./internal/repository/` and
// `go test ./internal/credentials/` separately, or a single package
// at a time) — a parallel `go test ./...` with INSTAEDIT_TEST_PG_URL
// set could race truncate/seed on the shared instance.
package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// sweepIntegrationDB opens the integration DB and guarantees the
// columns ListDormantRefreshGrants needs exist. The shared test
// database may carry either the full migration schema OR the minimal
// schema created by credentials/vault_integration_test.go (which lacks
// oauth_connections.expires_at / reauth_required_at and
// platform_accounts.oauth_connection_id) — CREATE IF NOT EXISTS +
// ALTER ADD COLUMN IF NOT EXISTS makes this test self-contained under
// both. Cleanup truncates oauth_connections CASCADE (which cascades to
// platform_accounts and tokens via their FKs).
func sweepIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("INSTAEDIT_TEST_PG_URL")
	if url == "" {
		t.Skip("INSTAEDIT_TEST_PG_URL is not set; skipping refresh-sweep integration test")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("Postgres not reachable at %s: %v", url, err)
	}

	// The shared test DB can carry THREE schema variants depending on
	// which package's integration test created the tables first: the
	// full migration schema, the minimal schema from
	// credentials/vault_integration_test.go (which lacks expires_at /
	// reauth_required_at / oauth_connection_id), or a fresh DB. The
	// CREATE IF NOT EXISTS below defines a SUPERSET of all variants
	// (granted_scopes + last_refresh_error included because the
	// credentials Renew path WRITES them via updateGrantScopesTx /
	// updateOAuthConnectionStatus), so whichever package runs first,
	// every integration test keeps working. ALTER ADD COLUMN IF NOT
	// EXISTS covers the vault-minimal variant that predates the newer
	// columns.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS oauth_connections (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL DEFAULT 1,
			provider VARCHAR(50) NOT NULL,
			provider_subject_id TEXT NOT NULL DEFAULT '',
			provider_resource_id VARCHAR(255) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			scopes TEXT[] NOT NULL DEFAULT '{}',
			granted_scopes TEXT[] NOT NULL DEFAULT '{}',
			last_refresh_error TEXT,
			expires_at TIMESTAMPTZ,
			last_validated_at TIMESTAMPTZ,
			last_refresh_at TIMESTAMPTZ,
			reauth_required_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(user_id, provider, provider_resource_id)
		)`,
		`ALTER TABLE oauth_connections ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`ALTER TABLE oauth_connections ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ`,
		`ALTER TABLE oauth_connections ADD COLUMN IF NOT EXISTS granted_scopes TEXT[] NOT NULL DEFAULT '{}'`,
		`ALTER TABLE oauth_connections ADD COLUMN IF NOT EXISTS last_refresh_error TEXT`,
		`CREATE TABLE IF NOT EXISTS platform_accounts (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL DEFAULT 1,
			platform VARCHAR(50) NOT NULL,
			platform_user_id VARCHAR(255) NOT NULL,
			username VARCHAR(255),
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			oauth_connection_id BIGINT,
			workspace_id BIGINT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(platform, platform_user_id)
		)`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS oauth_connection_id BIGINT`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id BIGSERIAL PRIMARY KEY,
			platform_account_id BIGINT REFERENCES platform_accounts(id) ON DELETE CASCADE,
			oauth_connection_id BIGINT NOT NULL REFERENCES oauth_connections(id) ON DELETE CASCADE,
			token_type VARCHAR(50) NOT NULL,
			encrypted_access_token BYTEA,
			encrypted_token BYTEA NOT NULL,
			encrypted_refresh_token BYTEA,
			access_token_expires_at TIMESTAMPTZ,
			expires_at TIMESTAMPTZ,
			refresh_token_expires_at TIMESTAMPTZ,
			scopes TEXT[],
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("schema setup: %v (statement: %s)", err, s)
		}
	}
	// Cleanup truncates the three tables EXPLICITLY (mirroring
	// credentials/vault_integration_test.go) so the destructive scope
	// is visible. Each TRUNCATE ... CASCADE also cascades to any table
	// referencing them (e.g. livestreams via platform_accounts on the
	// full migration schema) — the same surface the vault test already
	// exercises on the shared dev DB.
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "TRUNCATE tokens RESTART IDENTITY CASCADE")
		_, _ = db.ExecContext(context.Background(), "TRUNCATE platform_accounts RESTART IDENTITY CASCADE")
		_, _ = db.ExecContext(context.Background(), "TRUNCATE oauth_connections RESTART IDENTITY CASCADE")
	})
	return db
}

// timePtr returns a pointer to t (nil when t is nil), for the seed
// spec's optional timestamps.
func timePtr(t time.Time) *time.Time { return &t }

// sweepSeed describes one oauth_connection (+ linked platform_account)
// to seed. Zero values mean NULL / defaults.
type sweepSeed struct {
	provider       string
	status         string
	reauthRequired bool          // sets reauth_required_at = now-1h
	lastRefresh    *time.Time    // nil = never refreshed
	createdAgo     time.Duration // created_at = now - createdAgo
	expiresAt      *time.Time    // nil = no provider TTL
	accounts       int           // platform_accounts to link (default 1)
}

// seedSweepGrant inserts one oauth_connection + N platform_accounts
// and returns the oauth_connection id. Provider resource ids are made
// unique per seed so the UNIQUE(user_id, provider, provider_resource_id)
// constraint is never hit across tests.
func seedSweepGrant(t *testing.T, db *sql.DB, seq *int, s sweepSeed) int64 {
	t.Helper()
	*seq++
	provider := s.provider
	if provider == "" {
		provider = models.PlatformYouTube
	}
	status := s.status
	if status == "" {
		status = "active"
	}
	accounts := s.accounts
	if accounts <= 0 {
		accounts = 1
	}
	now := time.Now()
	createdAt := now.Add(-s.createdAgo)
	var reauthAt *time.Time
	if s.reauthRequired {
		reauthAt = timePtr(now.Add(-time.Hour))
	}
	resourceID := fmt.Sprintf("sweep-%s-%d", provider, *seq)

	ctx := context.Background()
	var connID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, status, reauth_required_at, last_refresh_at, expires_at, created_at)
		 VALUES (1, $1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		provider, resourceID, status, reauthAt, s.lastRefresh, s.expiresAt, createdAt,
	).Scan(&connID); err != nil {
		t.Fatalf("insert oauth_connection: %v", err)
	}
	for i := 0; i < accounts; i++ {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO platform_accounts (user_id, platform, platform_user_id, oauth_connection_id)
			 VALUES (1, $1, $2, $3)`,
			provider, fmt.Sprintf("%s-acc-%d", resourceID, i), connID,
		); err != nil {
			t.Fatalf("insert platform_account: %v", err)
		}
	}
	return connID
}

// selectedIDs runs the sweep query and returns the selected
// oauth_connection ids keyed by provider, so assertions can check
// exactly which grants came back.
func selectedIDs(t *testing.T, db *sql.DB, horizonDays int) map[int64]string {
	t.Helper()
	repo := repository.NewRefreshSweepRepository(db)
	grants, err := repo.ListDormantRefreshGrants(context.Background(), horizonDays)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrants: %v", err)
	}
	out := make(map[int64]string, len(grants))
	for _, g := range grants {
		out[g.OAuthConnectionID] = g.Provider
	}
	return out
}

// TestRefreshSweep_Integration_SelectsDormantAndSkipsFresh is the
// headline integration test: one row per grant, covering every branch
// of the selection predicate against REAL Postgres rows.
func TestRefreshSweep_Integration_SelectsDormantAndSkipsFresh(t *testing.T) {
	db := sweepIntegrationDB(t)
	seq := 0
	now := time.Now()

	// Selected branches.
	neverRefreshedOld := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:   models.PlatformYouTube,
		createdAgo: 200 * 24 * time.Hour, // connected once, never published
	})
	dormantRefresh := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformGoogleDrive,
		lastRefresh: timePtr(now.Add(-200 * 24 * time.Hour)), // went quiet ~6.5 months ago
	})
	providerTTL := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-24 * time.Hour)),
		expiresAt:   timePtr(now.Add(24 * time.Hour)), // TTL inside the 7-day window
	})
	alreadyExpired := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:  models.PlatformGoogleDrive,
		expiresAt: timePtr(now.Add(-24 * time.Hour)), // TTL already past — the most urgent cohort
	})

	// NOT selected branches.
	recentRefresh := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-10 * 24 * time.Hour)),
	})
	revoked := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		status:      "revoked",
		lastRefresh: timePtr(now.Add(-200 * 24 * time.Hour)),
	})
	reauthRequired := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:       models.PlatformYouTube,
		reauthRequired: true, // status still 'active' — needs a human, not a refresh
		lastRefresh:    timePtr(now.Add(-200 * 24 * time.Hour)),
	})
	createdRecently := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:   models.PlatformGoogleDrive,
		createdAgo: 30 * 24 * time.Hour, // never refreshed but too new to be dormant
	})

	selected := selectedIDs(t, db, 120)

	// All four selected branches MUST come back.
	for name, id := range map[string]int64{
		"never refreshed, old created_at": neverRefreshedOld,
		"dormant refresh 200d ago":        dormantRefresh,
		"provider TTL within 7 days":      providerTTL,
		"provider TTL already past":       alreadyExpired,
	} {
		if _, ok := selected[id]; !ok {
			t.Errorf("grant %q (oc=%d) NOT selected — predicate branch missed", name, id)
		}
	}
	// All four negative branches MUST stay out.
	for name, id := range map[string]int64{
		"recent refresh 10d ago":          recentRefresh,
		"revoked status":                  revoked,
		"active + reauth_required_at set": reauthRequired,
		"never refreshed, new created_at": createdRecently,
	} {
		if _, ok := selected[id]; ok {
			t.Errorf("grant %q (oc=%d) selected but must be EXCLUDED", name, id)
		}
	}
	// No unexpected grants came back.
	if got, want := len(selected), 4; got != want {
		t.Errorf("selected grant count: want %d, got %d (full set: %v)", want, got, selected)
	}
}

// TestRefreshSweep_Integration_MultiChannelGrant_ReturnsAllAccounts
// pins the contract the worker's per-oauth_connection dedup relies on:
// one grant backing N platform_accounts yields N rows (the worker
// renews once per oauth_connection and skips the duplicates).
func TestRefreshSweep_Integration_MultiChannelGrant_ReturnsAllAccounts(t *testing.T) {
	db := sweepIntegrationDB(t)
	seq := 0
	now := time.Now()

	connID := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-200 * 24 * time.Hour)),
		accounts:    2, // a YouTube grant spanning two channels
	})

	repo := repository.NewRefreshSweepRepository(db)
	grants, err := repo.ListDormantRefreshGrants(context.Background(), 120)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrants: %v", err)
	}
	var rowsForConn int
	for _, g := range grants {
		if g.OAuthConnectionID == connID {
			rowsForConn++
		}
	}
	if rowsForConn != 2 {
		t.Errorf("multi-channel grant (oc=%d): want 2 rows (one per platform_account), got %d — worker dedup expects N rows", connID, rowsForConn)
	}
	// The two rows share the connection id but must carry distinct
	// platform account ids (the worker renews per account id).
	seenAccounts := map[int64]bool{}
	for _, g := range grants {
		if g.OAuthConnectionID == connID {
			seenAccounts[g.PlatformAccountID] = true
		}
	}
	if len(seenAccounts) != 2 {
		t.Errorf("multi-channel grant: want 2 distinct platform_account_ids, got %d", len(seenAccounts))
	}
}

// TestRefreshSweep_Integration_HorizonParameter_ChangesSelection pins
// that horizonDays is not decorative: a grant last refreshed 100 days
// ago is OUTSIDE the 120-day horizon but INSIDE a 90-day horizon.
func TestRefreshSweep_Integration_HorizonParameter_ChangesSelection(t *testing.T) {
	db := sweepIntegrationDB(t)
	seq := 0
	now := time.Now()

	connID := seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-100 * 24 * time.Hour)),
	})

	if _, ok := selectedIDs(t, db, 120)[connID]; ok {
		t.Errorf("grant refreshed 100d ago must NOT be selected with horizon=120 days")
	}
	if _, ok := selectedIDs(t, db, 90)[connID]; !ok {
		t.Errorf("grant refreshed 100d ago MUST be selected with horizon=90 days")
	}
}

// TestRefreshSweep_Integration_EmptyResult pins the no-dormant-grants
// contract: with only fresh grants seeded, the query returns an empty
// slice (the worker skips the pass).
func TestRefreshSweep_Integration_EmptyResult(t *testing.T) {
	db := sweepIntegrationDB(t)
	seq := 0
	now := time.Now()

	seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-24 * time.Hour)),
	})

	selected := selectedIDs(t, db, 120)
	if len(selected) != 0 {
		t.Errorf("fresh-only DB: want 0 selected grants, got %v", selected)
	}
}

// TestRefreshSweep_Integration_SingleFlightLock_OnlyWinnerSelects
// proves the replica single-flight against REAL Postgres advisory
// locks (which are cluster-wide per database):
//
//  1. An OUTER transaction acquires the sweep lock (simulating a
//     replica that is mid-pass).
//  2. ListDormantRefreshGrantsSingleFlighted on the SAME database
//     (different pool connection) reports won=false and selects
//     NOTHING — the loser must not run the SELECT.
//  3. The holder commits (lock auto-released by the xact lock).
//  4. The same call now wins (won=true) and returns the seeded
//     dormant grant — proving the mechanism isn't stuck.
//
// The integration DB pool is sized >1 (SetMaxOpenConns(5)) so the
// second connection can actually contend instead of queueing for the
// holder's connection.
func TestRefreshSweep_Integration_SingleFlightLock_OnlyWinnerSelects(t *testing.T) {
	db := sweepIntegrationDB(t)
	seq := 0
	now := time.Now()
	seedSweepGrant(t, db, &seq, sweepSeed{
		provider:    models.PlatformYouTube,
		lastRefresh: timePtr(now.Add(-200 * 24 * time.Hour)),
	})
	repo := repository.NewRefreshSweepRepository(db)

	// Step 1: an outer tx holds the sweep lock (a replica mid-pass).
	holder, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin lock holder tx: %v", err)
	}
	var acquired bool
	if err := holder.QueryRowContext(context.Background(), repository.SQLRefreshSweepSingleFlightLock, repository.RefreshSweepLockID).Scan(&acquired); err != nil {
		_ = holder.Rollback()
		t.Fatalf("holder acquire lock: %v", err)
	}
	if !acquired {
		_ = holder.Rollback()
		t.Fatal("holder could not acquire the sweep lock")
	}

	// Step 2: the repository (a different connection) must LOSE the
	// tick — no selection, won=false.
	grants, won, err := repo.ListDormantRefreshGrantsSingleFlighted(context.Background(), 120)
	if err != nil {
		_ = holder.Rollback()
		t.Fatalf("ListDormantRefreshGrantsSingleFlighted while lock held: %v", err)
	}
	if won {
		_ = holder.Rollback()
		t.Errorf("want won=false while another tx holds the sweep lock, got won=true")
	}
	if len(grants) != 0 {
		_ = holder.Rollback()
		t.Errorf("want 0 grants while the lock is held (SELECT must be skipped), got %d", len(grants))
	}

	// Step 3: holder commits — the xact lock auto-releases.
	if err := holder.Commit(); err != nil {
		t.Fatalf("commit lock holder: %v", err)
	}

	// Step 4: the same repository now wins the tick and selects the
	// seeded dormant grant.
	grants, won, err = repo.ListDormantRefreshGrantsSingleFlighted(context.Background(), 120)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrantsSingleFlighted after lock release: %v", err)
	}
	if !won {
		t.Error("want won=true after the lock is released, got false")
	}
	if len(grants) != 1 {
		t.Errorf("want 1 selected grant after lock release, got %d", len(grants))
	}
}
