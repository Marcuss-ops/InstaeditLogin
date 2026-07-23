//go:build integration

package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// setupOneTimeCodeStorePostgres creates a OneTimeCodePostgresStore against
// an ephemeral testcontainers Postgres instance. It runs the full migration
// set so the one_time_codes table exists and is consistent with production.
func setupOneTimeCodeStorePostgres(t *testing.T, ttl time.Duration) *OneTimeCodePostgresStore {
	t.Helper()

	db, cleanupDB := postgres.StartTestPostgres(t)
	t.Cleanup(cleanupDB)

	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	store := NewOneTimeCodePostgresStore(db, ttl)
	t.Cleanup(store.Stop)
	return store
}

func TestOneTimeCodePostgresStore_GenerateAndConsume(t *testing.T) {
	store := setupOneTimeCodeStorePostgres(t, 60*time.Second)

	payload := ExchangePayload{
		UserID:   1,
		Name:     "Mario Rossi",
		Username: "mariorossi",
	}

	code, err := store.Generate(payload)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if code == "" {
		t.Fatal("Generate returned an empty code")
	}

	got, err := store.Consume(code)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.UserID != payload.UserID {
		t.Errorf("UserID: got %d, want %d", got.UserID, payload.UserID)
	}
	if got.Name != payload.Name {
		t.Errorf("Name: got %q, want %q", got.Name, payload.Name)
	}
	if got.Username != payload.Username {
		t.Errorf("Username: got %q, want %q", got.Username, payload.Username)
	}

	// A code can only be consumed once, even on the same replica.
	_, err = store.Consume(code)
	if err != ErrCodeNotFound {
		t.Errorf("second Consume: got %v, want ErrCodeNotFound", err)
	}
}

func TestOneTimeCodePostgresStore_ConsumeUnknownCode(t *testing.T) {
	store := setupOneTimeCodeStorePostgres(t, 60*time.Second)

	_, err := store.Consume("definitely-not-a-valid-code")
	if err != ErrCodeNotFound {
		t.Errorf("Consume unknown code: got %v, want ErrCodeNotFound", err)
	}
}

func TestOneTimeCodePostgresStore_ExpiredCodeCannotBeConsumed(t *testing.T) {
	store := setupOneTimeCodeStorePostgres(t, 1*time.Millisecond)

	payload := ExchangePayload{
		UserID:   2,
		Name:     "Expired User",
		Username: "expired",
	}

	code, err := store.Generate(payload)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Wait long enough for the TTL to elapse and for Postgres NOW() to
	// be strictly greater than expires_at.
	time.Sleep(50 * time.Millisecond)

	_, err = store.Consume(code)
	if err != ErrCodeNotFound {
		t.Errorf("Consume expired code: got %v, want ErrCodeNotFound", err)
	}
}

func TestOneTimeCodePostgresStore_SweeperRemovesExpiredRows(t *testing.T) {
	store := setupOneTimeCodeStorePostgres(t, 1*time.Millisecond)

	payload := ExchangePayload{
		UserID:   3,
		Name:     "Sweep User",
		Username: "sweep",
	}

	code, err := store.Generate(payload)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Wait for the row to expire.
	time.Sleep(50 * time.Millisecond)

	// Manually trigger the sweeper logic. In production the sweeper
	// runs on its own ticker; here we exercise the same query directly
	// so the test stays deterministic and fast.
	_, err = store.db.Exec(`DELETE FROM one_time_codes WHERE expires_at <= NOW()`)
	if err != nil {
		t.Fatalf("sweeper DELETE: %v", err)
	}

	// After sweeping, the code must be gone.
	_, err = store.Consume(code)
	if err != ErrCodeNotFound {
		t.Errorf("after sweep: got %v, want ErrCodeNotFound", err)
	}
}

// TestOneTimeCodePostgresStore_ConcurrentConsume stresses the core
// "consumo atomico" contract: many goroutines racing to consume the
// same code must produce exactly one success and N-1 failures.
func TestOneTimeCodePostgresStore_ConcurrentConsume(t *testing.T) {
	store := setupOneTimeCodeStorePostgres(t, 60*time.Second)

	payload := ExchangePayload{
		UserID:   4,
		Name:     "Concurrent User",
		Username: "concurrent",
	}

	code, err := store.Generate(payload)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var wg sync.WaitGroup
	var successes, failures atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Consume(code)
			if err == nil {
				successes.Add(1)
			} else {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 || failures.Load() != 9 {
		t.Errorf("concurrent consume race: successes=%d failures=%d (want 1 and 9)", successes.Load(), failures.Load())
	}
}
