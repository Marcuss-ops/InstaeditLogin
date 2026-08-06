package credentials

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestRefreshWindow_IsDeterministicAndBounded(t *testing.T) {
	for _, id := range []int64{1, 2, 7, 42, 1000, 1_000_000} {
		got := RefreshWindow(id)
		if got < RefreshEarlyWindow || got > RefreshEarlyWindow+RefreshEarlyJitter {
			t.Fatalf("connection %d: window %s outside [%s, %s]", id, got, RefreshEarlyWindow, RefreshEarlyWindow+RefreshEarlyJitter)
		}
		if got != RefreshWindow(id) {
			t.Fatalf("connection %d: window is not deterministic", id)
		}
	}
	if got := RefreshWindow(0); got != RefreshEarlyWindow {
		t.Fatalf("invalid connection id: window %s, want base %s", got, RefreshEarlyWindow)
	}
}

func TestRefreshWindow_DistributesSequentialGrantIDs(t *testing.T) {
	seen := make(map[time.Duration]struct{})
	for id := int64(1); id <= 128; id++ {
		seen[RefreshWindow(id)] = struct{}{}
	}
	if len(seen) < 8 {
		t.Fatalf("jitter produced only %d distinct windows for 128 sequential grants", len(seen))
	}
}

func TestVault_Renew_ConcurrentSharedOAuthConnection_ApplicationSingleflight(t *testing.T) {
	v, mock, store := newTestVault(t)
	mock.MatchExpectationsInOrder(false)
	const (
		accountA   int64 = 101
		accountB   int64 = 102
		connection int64 = 700
	)
	shared := newEncryptedToken(t, v, accountA, -time.Minute, "shared-refresh")
	shared.OAuthConnectionID = connection
	store.seedToken(shared)

	// Different platform accounts resolve to one shared grant. Both
	// initial probes must converge on the same grant-keyed flight.
	expectOauthConnLookup(mock, accountA, connection)
	expectOauthConnLookup(mock, accountB, connection)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(connection))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(connection).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOAuthClientKeyLookup(mock, connection, "youtube_pool_a")
	mock.ExpectCommit()
	// Each caller performs its own post-flight read, but neither can
	// trigger another refresh or advisory-lock transaction.
	expectOauthConnLookup(mock, accountA, connection)
	expectOauthConnLookup(mock, accountB, connection)

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		if refreshToken != "shared-refresh" {
			t.Errorf("refresh token: got %q", refreshToken)
		}
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &models.TokenData{AccessToken: "shared-fresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make([]*models.OAuthToken, 2)
	errs := make([]error, 2)
	accounts := []int64{accountA, accountB}
	var wg sync.WaitGroup
	wg.Add(len(accounts))
	enteredFlight := make(chan string, 2)
	// Start the leader first so the provider block is established before
	// launching the waiter. This makes the singleflight join deterministic
	// without adding a production-only synchronization hook.
	go func() {
		defer wg.Done()
		results[0], errs[0] = v.renew(ctx, accounts[0], models.TokenTypeBearer, refresher, func(key string) { enteredFlight <- key })
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("shared-grant refresh leader did not reach provider")
	}
	// Launch the waiter only after the leader is blocked inside the
	// refresher. It must join the active grant-keyed flight.
	go func() {
		defer wg.Done()
		results[1], errs[1] = v.renew(ctx, accounts[1], models.TokenTypeBearer, refresher, func(key string) { enteredFlight <- key })
	}()
	for i := 0; i < 2; i++ {
		select {
		case key := <-enteredFlight:
			if key != "renew:700:bearer" {
				t.Fatalf("unexpected shared-grant flight key %q", key)
			}
		case <-ctx.Done():
			t.Fatal("shared-grant caller did not reach the shared flight")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider refresh calls while shared-grant leader blocked: got %d, want 1", got)
	}
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Renew shared account[%d]: %v", i, err)
		}
		if results[i] == nil || results[i].AccessToken != "shared-fresh" {
			t.Errorf("Renew shared account[%d] result: %#v", i, results[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("shared oauth_connection_id provider calls: got %d, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestVault_Renew_ConcurrentSameGrant_ApplicationSingleflight(t *testing.T) {
	v, mock, store := newTestVault(t)
	mock.MatchExpectationsInOrder(false)
	const accountID int64 = 44
	store.seedToken(newEncryptedToken(t, v, accountID, -time.Minute, "shared-refresh"))

	// Both callers resolve the same grant and perform an expired probe.
	expectOauthConnLookup(mock, accountID, accountID)
	expectOauthConnLookup(mock, accountID, accountID)
	// Only the singleflight leader may reach the advisory-lock transaction.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOAuthClientKeyLookup(mock, accountID, "youtube_pool_a")
	mock.ExpectCommit()
	// Each waiter reads the committed row through the public Get path.
	expectOauthConnLookup(mock, accountID, accountID)
	expectOauthConnLookup(mock, accountID, accountID)

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		if refreshToken != "shared-refresh" {
			t.Errorf("refresh token: got %q", refreshToken)
		}
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &models.TokenData{AccessToken: "shared-fresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make([]*models.OAuthToken, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	enteredFlight := make(chan string, 2)
	// Start the leader first so the waiter cannot legitimately observe a
	// freshly persisted token and skip the shared flight.
	go func() {
		defer wg.Done()
		results[0], errs[0] = v.renew(ctx, accountID, models.TokenTypeBearer, refresher, func(key string) { enteredFlight <- key })
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("refresh leader did not reach provider")
	}
	// Launch the waiter only while the leader is blocked in the provider;
	// it must join the active singleflight rather than start a new refresh.
	go func() {
		defer wg.Done()
		results[1], errs[1] = v.renew(ctx, accountID, models.TokenTypeBearer, refresher, func(key string) { enteredFlight <- key })
	}()
	for i := 0; i < 2; i++ {
		select {
		case key := <-enteredFlight:
			if key != "renew:44:bearer" {
				t.Fatalf("unexpected same-grant flight key %q", key)
			}
		case <-ctx.Done():
			t.Fatal("same-grant waiter did not reach the shared flight")
		}
	}
	// If a second caller reached the provider, the count would already
	// be greater than one while the leader is blocked.
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider refresh calls while leader blocked: got %d, want 1", got)
	}
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Renew[%d]: %v", i, err)
		}
		if results[i] == nil || results[i].AccessToken != "shared-fresh" {
			t.Errorf("Renew[%d] result: %#v", i, results[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("provider refresh calls: got %d, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
