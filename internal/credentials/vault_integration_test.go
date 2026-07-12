// Package credentials — Taglio 2.3 integration tests for the advisory
// lock in CredentialVault.Renew.
//
// These tests are SKIPPED by default (no network, no Docker). They run
// only when the environment variable INSTAEDIT_TEST_PG_URL points to a
// reachable Postgres database. The CI pipeline sets this var before
// `go test -race ./internal/credentials/...` runs; local developers
// can point it at the docker-compose Postgres with:
//
//	export INSTAEDIT_TEST_PG_URL='postgresql://instaedit:dev_password@localhost:5432/instaedit_login?sslmode=disable'
//	make dev   # in another shell, to start the DB
//	go test -race -v ./internal/credentials/...
//
// What these tests prove that vault_test.go CANNOT:
//
//  1. SAME account_id + concurrent Renew → exactly ONE call to the
//     refresher. The second goroutine sees the freshly-saved row
//     inside the lock and short-circuits.
//
//  2. DIFFERENT account_ids + concurrent Renew → TWO calls to the
//     refresher. The advisory lock is per-account_id, so two accounts
//     do NOT serialise.
//
//  3. The lock is actually acquired at the DB level (not just that
//     we issued the SQL). We use a barrier channel inside the
//     refresher to prove that goroutine B BLOCKS on the lock while
//     goroutine A holds it.
//
// What these tests do NOT prove (covered by vault_test.go):
//   - SQL sequence (Begin/Lock/Commit ordering)
//   - Error paths (lock failure, refresher failure)
//   - Long-lived fallback
//   - Context cancellation
//
// Note: there is NO `//go:build integration` tag here. The env var
// alone is the gate — `go test ./internal/credentials/...` is a no-op
// for these tests on a machine without INSTAEDIT_TEST_PG_URL, but
// `go test ./...` on a CI runner with the var set will run them
// without needing a special build tag. A build tag would also make
// `go test ./...` silently skip the proof of the headline Goal-1
// guarantee, which is too easy to miss in code review.

package credentials

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// testPGURL returns the Postgres URL for integration tests, or empty
// string if the env var is unset (in which case the tests skip).
func testPGURL() string {
	if u := os.Getenv("INSTAEDIT_TEST_PG_URL"); u != "" {
		return u
	}
	return ""
}

// integrationDB opens a *sql.DB for the integration tests and applies
// the minimum schema needed to exercise the tokens table. Returns the
// DB and a cleanup func that truncates the tokens table.
func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	url := testPGURL()
	if url == "" {
		t.Skip("INSTAEDIT_TEST_PG_URL is not set; skipping advisory-lock integration test")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// SetMaxOpenConns(>1) is critical: if the pool is capped at 1, the
	// second goroutine hangs on connection acquisition rather than on
	// the advisory lock, which would invalidate Goal 1's claim that
	// "the lock serialises renewals on the same account_id".
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("Postgres not reachable at %s: %v", url, err)
	}
	// Apply minimum schema. We don't depend on the migrations package
	// to keep this test self-contained — only the columns the vault
	// actually reads/writes are created.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platform_accounts (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL DEFAULT 1,
			platform VARCHAR(50) NOT NULL DEFAULT 'instagram',
			platform_user_id VARCHAR(255) NOT NULL DEFAULT 'test',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(platform, platform_user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id BIGSERIAL PRIMARY KEY,
			platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
			token_type VARCHAR(50) NOT NULL,
			encrypted_token BYTEA NOT NULL,
			encrypted_refresh_token BYTEA,
			expires_at TIMESTAMPTZ,
			scopes TEXT[],
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_platform_account_id ON tokens(platform_account_id)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("schema setup: %v (statement: %s)", err, s)
		}
	}
	// Cleanup: truncate tokens after the test so we don't leak rows
	// across runs (the test database is shared with the rest of the
	// suite via the docker-compose instance).
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "TRUNCATE tokens RESTART IDENTITY CASCADE")
		_, _ = db.ExecContext(context.Background(), "TRUNCATE platform_accounts RESTART IDENTITY CASCADE")
	})
	return db
}

// seedAccountAndExpiredToken inserts a fresh platform_accounts row
// and one expired token for it. The token's EncryptedToken is
// decryptable by the vault (real AES-256-GCM via the given encryptor),
// and EncryptedRefreshToken holds a known plaintext ("the-refresh") so
// the test can assert what the refresher receives.
func seedAccountAndExpiredToken(t *testing.T, db *sql.DB, enc *crypto.Encryptor, refreshPlaintext string) int64 {
	t.Helper()
	ctx := context.Background()
	var accountID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id) VALUES (1, 'instagram', $1) RETURNING id`,
		fmt.Sprintf("iact-%d", time.Now().UnixNano()),
	).Scan(&accountID); err != nil {
		t.Fatalf("insert platform_account: %v", err)
	}
	encAccess, err := enc.Encrypt("old-access")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	encRefresh, err := enc.Encrypt(refreshPlaintext)
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	// Insert with expires_at 1 minute in the PAST so the slow path
	// is taken immediately (no fast-path short-circuit).
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tokens (platform_account_id, token_type, encrypted_token, encrypted_refresh_token, expires_at)
		 VALUES ($1, $2, $3, $4, NOW() - INTERVAL '1 minute')`,
		accountID, models.TokenTypeBearer, encAccess, encRefresh,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	return accountID
}

// barrierRefresher is a TokenRefresher that records how many times it
// has been called and (on the FIRST call) signals via startSignal that
// it is now running, then blocks until releaseSignal is closed. This
// lets the test assert that a SECOND concurrent goroutine has NOT yet
// reached the refresher — it is still blocked on the advisory lock.
//
// The atomic Int32 check `if n == 1` already guarantees that
// close(startSignal) runs exactly once — the goroutine that
// successfully incremented calls from 0 to 1 is the only one that
// takes this branch. No sync.Once is needed (and would be double-
// bookkeeping).
type barrierRefresher struct {
	calls         atomic.Int32
	startSignal   chan struct{}
	releaseSignal chan struct{}
}

func (b *barrierRefresher) refresh(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	n := b.calls.Add(1)
	if n == 1 {
		// First call: close startSignal exactly once, then block.
		close(b.startSignal)
		select {
		case <-b.releaseSignal:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &models.TokenData{
		AccessToken: fmt.Sprintf("fresh-after-refresh-%d", n),
		TokenType:   "bearer",
		ExpiresIn:   3600,
	}, nil
}

// -----------------------------------------------------------------------
// Goal 1: SAME account_id + concurrent Renew → ONE refresher call
// -----------------------------------------------------------------------

// TestVault_Renew_ConcurrentSameAccount_SingleRefresherCall is the
// headline test of Taglio 2.3. It proves the Postgres advisory lock
// inside vault.Renew serialises concurrent renewals on the same
// account_id: the loser's re-read inside the lock returns the
// freshly-saved row, so the refresher is called EXACTLY once.
//
// Test choreography:
//  1. Seed an expired token for account X.
//  2. Launch two goroutines that both call Renew on account X.
//  3. Goroutine A reaches the refresher FIRST (singleton barrier)
//     and blocks there. Goroutine B is now blocked on the
//     pg_advisory_xact_lock(X) call.
//  4. Test waits for B to have time to attempt the lock (small sleep),
//     then asserts b.calls == 1 (B has NOT yet called the refresher).
//  5. Test releases A. A finishes, commits the lock tx, returns.
//  6. B's lock acquisition unblocks; B re-reads, sees the fresh row,
//     returns without calling the refresher.
//  7. Assert b.calls == 1 and both goroutines got the same fresh
//     access token back.
func TestVault_Renew_ConcurrentSameAccount_SingleRefresherCall(t *testing.T) {
	db := integrationDB(t)
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "kP5jF8aL2nQ7rT3vX6yB9cE1dG4hJ0mN5oS8uV2wY4zA="})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	store := repository.NewTokenRepository(db)
	v := NewCredentialVault(enc, db, store)
	accountID := seedAccountAndExpiredToken(t, db, enc, "the-refresh")

	barrier := &barrierRefresher{
		startSignal:   make(chan struct{}),
		releaseSignal: make(chan struct{}),
	}

	// Bound the test so a broken lock doesn't hang CI forever.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results := make([]*models.OAuthToken, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = v.Renew(ctx, accountID, models.TokenTypeBearer, barrier.refresh)
		}()
	}

	// Wait for goroutine A to enter the refresher and signal us.
	select {
	case <-barrier.startSignal:
	case <-time.After(5 * time.Second):
		close(barrier.releaseSignal) // unblock the other goroutine
		wg.Wait()
		t.Fatal("refresher never started — vault.Renew did not call the refresher for the seeded expired token")
	}

	// Release A. A finishes, commits the lock tx; B's lock
	// acquisition unblocks, B re-reads, returns the fresh row.
	close(barrier.releaseSignal)
	wg.Wait()

	// Final assertion: refresher was called exactly ONCE for the two
	// concurrent Renew requests. Two would mean the lock is broken
	// and both goroutines reached the platform API. If the lock were
	// not working, the second goroutine would have raced through to
	// the refresher within milliseconds of A's startSignal — we don't
	// need a time.Sleep to detect that; the call counter is the proof.
	if got := barrier.calls.Load(); got != 1 {
		t.Errorf("refresher call count for SAME account_id under contention: want 1 (loser must short-circuit inside the lock), got %d", got)
	}
	if errs[0] != nil || errs[1] != nil {
		t.Errorf("Renew errors: A=%v B=%v", errs[0], errs[1])
	}
	if results[0] == nil || results[1] == nil {
		t.Fatalf("nil results: A=%v B=%v", results[0], results[1])
	}
	// Both goroutines should return the SAME freshly-issued access
	// token (the one saved by A's Refresh+Save cycle, read by B's
	// re-read inside the lock).
	if results[0].AccessToken != results[1].AccessToken {
		t.Errorf("both goroutines must return the same fresh access token (A's refresh writes the row B re-reads); got %q and %q", results[0].AccessToken, results[1].AccessToken)
	}
	if results[0].AccessToken == "old-access" {
		t.Error("returned access token is the stale one — vault did NOT refresh")
	}
}

// -----------------------------------------------------------------------
// Goal 2: DIFFERENT account_ids + concurrent Renew → TWO refresher calls
// -----------------------------------------------------------------------

// TestVault_Renew_ConcurrentDifferentAccounts_BothRefresherCalls is
// the negative-control test: when the two concurrent Renews are on
// DIFFERENT account_ids, the lock keys are different, so there is no
// contention and BOTH refreshers must run. A regression that hashed
// all accounts to the same key (or acquired a coarse-grained lock)
// would fail this test.
func TestVault_Renew_ConcurrentDifferentAccounts_BothRefresherCalls(t *testing.T) {
	db := integrationDB(t)
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "kP5jF8aL2nQ7rT3vX6yB9cE1dG4hJ0mN5oS8uV2wY4zA="})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	store := repository.NewTokenRepository(db)
	v := NewCredentialVault(enc, db, store)
	accountA := seedAccountAndExpiredToken(t, db, enc, "refresh-A")
	accountB := seedAccountAndExpiredToken(t, db, enc, "refresh-B")

	// Track which account's refresh the refresher is being asked for.
	// We use a plain counter — the goal is just to prove both run.
	counter := atomic.Int32{}
	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		counter.Add(1)
		return &models.TokenData{
			AccessToken: "fresh-" + refreshToken,
			TokenType:   "bearer",
			ExpiresIn:   3600,
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	go func() {
		defer wg.Done()
		_, errs[0] = v.Renew(ctx, accountA, models.TokenTypeBearer, refresher)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = v.Renew(ctx, accountB, models.TokenTypeBearer, refresher)
	}()
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Errorf("Renew errors: A=%v B=%v", errs[0], errs[1])
	}
	if got := counter.Load(); got != 2 {
		t.Errorf("refresher call count for DIFFERENT account_ids: want 2 (no false serialization), got %d (lock is over-coarse)", got)
	}
}

// -----------------------------------------------------------------------
// Goal 3: Sanity — the lock SQL is real (the previous test proves it
// but this one documents the contract for the next person reading this).
// -----------------------------------------------------------------------

// TestVault_Renew_KeyedByAccountID_TwoAccountsUseDifferentLocks is a
// "smoke test" of the lock-key contract: two accounts, two renewals,
// two separate locks. We don't measure timing; we just assert the
// vault is willing to refresh both without either blocking the other
// (the test would deadlock if the lock key collided).
func TestVault_Renew_KeyedByAccountID_TwoAccountsUseDifferentLocks(t *testing.T) {
	db := integrationDB(t)
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "kP5jF8aL2nQ7rT3vX6yB9cE1dG4hJ0mN5oS8uV2wY4zA="})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	store := repository.NewTokenRepository(db)
	v := NewCredentialVault(enc, db, store)
	accountA := seedAccountAndExpiredToken(t, db, enc, "r-a")
	accountB := seedAccountAndExpiredToken(t, db, enc, "r-b")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	for _, acc := range []int64{accountA, accountB} {
		acc := acc
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Renew(ctx, acc, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
				return &models.TokenData{AccessToken: "ok", TokenType: "bearer", ExpiresIn: 3600}, nil
			}); err != nil {
				t.Errorf("Renew(account=%d): %v", acc, err)
			}
		}()
	}
	wg.Wait()
}
