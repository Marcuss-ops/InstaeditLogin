package credentials

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------
// Mocks
// -----------------------------------------------------------------------

// mockTokenStore implements the credentials.TokenStore contract (3 methods)
// using function fields AND an internal per-(accountID, tokenType) state
// map. The state map is what makes the slow-path Renew test work: when
// vault.Renew persists the refreshed token via SaveToken, the subsequent
// Get must see the FRESH row, not the originally-seeded expired one.
// Without state tracking, every FindLatestToken call would return the
// stale token and the final Get would surface the "expired at ..." error.
//
// The function fields remain for tests that need to inject errors or
// custom behaviour. Tests that just want to seed the initial state
// should use seedToken (does NOT increment saveCalls).
//
// state is NESTED (map[int64]map[string]*models.Token) so the delete
// path can match account_id with exact int64 equality. A flat
// "accountID:tokenType" string-key would break the moment two account
// ids share a digit prefix (e.g. 1 vs 10, 100 vs 1000) — HasPrefix
// would silently delete the wrong account's tokens.
type mockTokenStore struct {
	saveTokenFn  func(*models.Token) error
	findLatestFn func(int64, string) (*models.Token, error)
	deleteAllFn  func(int64) error
	saveCalls    atomic.Int32
	findCalls    atomic.Int32
	deleteCalls  atomic.Int32

	// state[accountID][tokenType] = *models.Token. The two-level map
	// matches the production SQL's `WHERE platform_account_id = $1`
	// equality semantics: deleting for account 10 only removes
	// account 10's tokens, never account 1's.
	state map[int64]map[string]*models.Token
}

// seedToken pre-populates the internal state for (t.PlatformAccountID,
// t.TokenType) WITHOUT calling SaveToken (so saveCalls is not inflated
// and the Save→Get roundtrip is observable). Used by every test that
// needs to start from a known initial token.
func (m *mockTokenStore) seedToken(t *models.Token) {
	if m.state == nil {
		m.state = make(map[int64]map[string]*models.Token)
	}
	bucket, ok := m.state[t.PlatformAccountID]
	if !ok {
		bucket = make(map[string]*models.Token)
		m.state[t.PlatformAccountID] = bucket
	}
	bucket[t.TokenType] = t
}

func (m *mockTokenStore) SaveToken(t *models.Token) error {
	m.saveCalls.Add(1)
	if m.saveTokenFn != nil {
		return m.saveTokenFn(t)
	}
	t.ID = int64(m.saveCalls.Load())
	t.CreatedAt = time.Now()
	m.seedToken(t)
	return nil
}

func (m *mockTokenStore) FindLatestToken(platformAccountID int64, tokenType string) (*models.Token, error) {
	m.findCalls.Add(1)
	if m.findLatestFn != nil {
		return m.findLatestFn(platformAccountID, tokenType)
	}
	if bucket, ok := m.state[platformAccountID]; ok {
		if t, ok := bucket[tokenType]; ok {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockTokenStore) DeleteAllTokensForPlatformAccount(platformAccountID int64) error {
	m.deleteCalls.Add(1)
	if m.deleteAllFn != nil {
		return m.deleteAllFn(platformAccountID)
	}
	// Exact int64 match — mirrors the production SQL
	// `DELETE FROM tokens WHERE platform_account_id = $1`. A nested
	// map makes this trivially safe against account-id prefix overlap
	// (1 vs 10, 100 vs 1000, etc.).
	delete(m.state, platformAccountID)
	return nil
}

// newTestVault wires a CredentialVault with a real sqlmock-backed *sql.DB
// and a mockTokenStore. The returned cleanup closes the DB.
//
// Taglio 2.3: these tests lock in the SQL sequence of vault.Renew, in
// particular that pg_advisory_xact_lock is acquired on the SLOW path
// (token within the 60s grace window) and NOT on the fast path (fresh
// token). The integration test in vault_integration_test.go locks in the
// concurrent behaviour with a real Postgres.
func newTestVault(t *testing.T) (*CredentialVault, sqlmock.Sqlmock, *mockTokenStore) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 32-byte base64-encoded key for AES-256-GCM (deterministic for tests).
	// Decoded to: 32 ASCII bytes "0123456789abcdef0123456789abcdef"
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}
	store := &mockTokenStore{}
	return NewCredentialVault(enc, db, store), mock, store
}

// newEncryptedToken returns a Token whose EncryptedToken field is a real
// AES-256-GCM ciphertext (decryptable by the vault's encryptor). Used by
// tests that exercise the slow path: the token's ExpiresAt is set within
// the 60s grace window so the vault must refresh.
func newEncryptedToken(t *testing.T, v *CredentialVault, accountID int64, expiresIn time.Duration, refreshToken string) *models.Token {
	t.Helper()
	encAccess, err := v.encryptor.Encrypt("old-access-token")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	tok := &models.Token{
		PlatformAccountID: accountID,
		TokenType:         models.TokenTypeBearer,
		EncryptedToken:    encAccess,
		ExpiresAt:         ptrTime(time.Now().Add(expiresIn)),
	}
	if refreshToken != "" {
		encRefresh, err := v.encryptor.Encrypt(refreshToken)
		if err != nil {
			t.Fatalf("encrypt refresh: %v", err)
		}
		tok.EncryptedRefreshToken = encRefresh
	}
	return tok
}

func ptrTime(t time.Time) *time.Time { return &t }

// -----------------------------------------------------------------------
// Fast-path: fresh token → no SQL lock
// -----------------------------------------------------------------------

// TestVault_Renew_FastPath_FreshToken_NoLockAcquisition is the fast-path
// test: a token with ExpiresAt > 60s in the future must short-circuit
// WITHOUT opening a transaction or issuing pg_advisory_xact_lock. This
// is the hot path for the 99% of renewals that are called when the
// token is still valid; the lock must not be paid for in the common case.
func TestVault_Renew_FastPath_FreshToken_NoLockAcquisition(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 10
	// ExpiresAt 5 minutes in the future — well outside the 60s grace window.
	fresh := newEncryptedToken(t, v, accountID, 5*time.Minute, "old-refresh")
	store.seedToken(fresh)

	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		t.Fatal("refresher must NOT be called on fast path (token is fresh)")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got == nil || got.AccessToken == "" {
		t.Fatal("Renew returned nil/empty token on fast path")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (fast path must NOT issue BEGIN or pg_advisory_xact_lock)", err)
	}
	// FindLatestToken was called once (the fast path's read). Save and
	// Delete must NOT have been called.
	if store.saveCalls.Load() != 0 {
		t.Errorf("SaveToken calls: want 0 on fast path, got %d", store.saveCalls.Load())
	}
	if store.deleteCalls.Load() != 0 {
		t.Errorf("DeleteAllTokensForPlatformAccount calls: want 0 on fast path, got %d", store.deleteCalls.Load())
	}
}

// -----------------------------------------------------------------------
// Slow-path: expired / within-grace → acquire lock, refresh, commit
// -----------------------------------------------------------------------

// TestVault_Renew_SlowPath_ExpiredToken_AcquiresLockAndCommits is the
// SQL-sequence test for the slow path. It uses go-sqlmock with strict
// expectation ordering to assert:
//
//  1. The vault opens a transaction (BEGIN) BEFORE issuing the lock SQL.
//  2. pg_advisory_xact_lock is called with the EXACT account_id as the
//     sole argument (i.e. the lock key IS the platform_account_id).
//  3. The lock transaction is COMMITTED, not rolled back, on success.
//
// A regression that re-ordered the steps, dropped the lock SQL, or
// rolled back the lock tx on the happy path would all fail this test.
func TestVault_Renew_SlowPath_ExpiredToken_AcquiresLockAndCommits(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 42
	// ExpiresAt in the past — must trigger the slow path. seedToken
	// (not findLatestFn) is the right primitive here because the vault
	// will call SaveToken after refresh and then Get — the final Get
	// must see the FRESH row written by SaveToken, not the expired row.
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)

	// SQL sequence (strict order):
	//   BEGIN
	//   SELECT pg_advisory_xact_lock(42)
	//   COMMIT
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	var refreshCalled atomic.Int32
	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		refreshCalled.Add(1)
		if refreshToken != "old-refresh" {
			t.Errorf("refresher received refresh token: want %q, got %q", "old-refresh", refreshToken)
		}
		return &models.TokenData{
			AccessToken: "fresh-access",
			TokenType:   "bearer",
			ExpiresIn:   3600,
		}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got == nil || got.AccessToken != "fresh-access" {
		t.Errorf("returned access token: want fresh-access, got %q", got.AccessToken)
	}
	if refreshCalled.Load() != 1 {
		t.Errorf("refresher call count: want 1, got %d", refreshCalled.Load())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (slow path must issue BEGIN, pg_advisory_xact_lock($1), COMMIT in that order)", err)
	}
	// The lock transaction committed; FindLatestToken was called twice
	// (fast-path probe + re-check inside the lock); SaveToken was called
	// once (persist the refreshed token).
	if store.findCalls.Load() != 4 {
		// The slow path triggers 4 FindLatestToken calls: Get#1 (fast-path
		// probe), Get#2 (re-check inside the lock), v.store.FindLatestToken
		// (for extractRefreshMaterial), Get#3 (final return after Save).
		// The first three see the expired row; the fourth must see the
		// freshly-saved row — which is the whole point of the state map.
		t.Errorf("FindLatestToken calls: want 4 (3 via Get + 1 direct for extractRefreshMaterial), got %d", store.findCalls.Load())
	}
	if store.saveCalls.Load() != 1 {
		t.Errorf("SaveToken calls: want 1 (persist refreshed token), got %d", store.saveCalls.Load())
	}
}

// TestVault_Renew_SlowPath_WithinGraceWindow_AcquiresLock covers the
// second slow-path trigger: the token is NOT yet expired but it IS
// within the 60s grace window. The vault must still acquire the lock
// and refresh.
func TestVault_Renew_SlowPath_WithinGraceWindow_AcquiresLock(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 7
	// ExpiresAt 30s in the future — INSIDE the 60s grace window.
	soonExpiring := newEncryptedToken(t, v, accountID, 30*time.Second, "old-refresh")
	store.seedToken(soonExpiring)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return &models.TokenData{AccessToken: "fresh", TokenType: "bearer", ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (within-grace must also acquire the lock)", err)
	}
}

// -----------------------------------------------------------------------
// Error paths: lock SQL fails / refresher fails / save fails
// -----------------------------------------------------------------------

// TestVault_Renew_LockAcquisitionFails_RollsBack proves the lock
// transaction is rolled back (not committed) when the pg_advisory_xact_lock
// SQL itself errors. A leak here would hold the lock until connection
// death; the rollback releases it immediately.
func TestVault_Renew_LockAcquisitionFails_RollsBack(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 99
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "r")
	store.seedToken(expired)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnError(errors.New("simulated lock acquisition failure"))
	mock.ExpectRollback()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		t.Fatal("refresher must NOT be called when the lock SQL fails")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error from failed lock acquisition, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (lock-failure path must ROLLBACK, not COMMIT)", err)
	}
	if store.saveCalls.Load() != 0 {
		t.Errorf("SaveToken must NOT be called when lock fails; got %d", store.saveCalls.Load())
	}
}

// TestVault_Renew_RefresherFails_PropagatesAndRollsBack proves the
// refresher error surfaces to the caller AND the lock tx is rolled
// back. A regression that committed the tx on a refresher error
// would release the lock prematurely (the next caller would see the
// stale row and re-refresh, wasting API budget).
func TestVault_Renew_RefresherFails_PropagatesAndRollsBack(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 11
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)

	refresherErr := errors.New("simulated platform 500")
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return nil, refresherErr
	})
	if err == nil {
		t.Fatal("expected error from failing refresher, got nil")
	}
	if !errors.Is(err, refresherErr) {
		t.Errorf("refresher error must be wrapped (errors.Is): got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (refresher-failure must ROLLBACK the lock tx)", err)
	}
}

// -----------------------------------------------------------------------
// Meta long-lived fallback: no refresh token → use access token
// -----------------------------------------------------------------------

// TestVault_Renew_LongLivedToken_UsesAccessTokenAsRefreshMaterial covers
// the Meta-style "no refresh token issued; re-exchange the long-lived
// access token via fb_exchange_token" path. When TokenType is
// LongLived and EncryptedRefreshToken is empty, the vault must decrypt
// the access token and pass IT to the refresher.
func TestVault_Renew_LongLivedToken_UsesAccessTokenAsRefreshMaterial(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 3
	// Long-lived token with NO refresh token. EncryptedToken is the
	// current long-lived access token, which is what fb_exchange_token
	// expects as input.
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "")
	expired.TokenType = models.TokenTypeLongLived
	store.seedToken(expired)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	var capturedRefresh string
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeLongLived, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		capturedRefresh = refreshToken
		return &models.TokenData{AccessToken: "new-ll", TokenType: models.TokenTypeLongLived, ExpiresIn: 60 * 24 * 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if capturedRefresh != "old-access-token" {
		t.Errorf("Meta long-lived fallback: refresher must receive the decrypted access token; want %q, got %q", "old-access-token", capturedRefresh)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestVault_Renew_NonLongLivedToken_NoRefreshToken_Errors covers the
// non-Meta case: a Bearer token with no refresh token AND outside the
// long-lived path must return a descriptive error. The vault cannot
// refresh without either a refresh token OR the long-lived fallback;
// silently returning the expired token would lead to publish failures
// downstream. The error must come from extractRefreshMaterial (the
// "no refresh token available" path), the lock tx must roll back,
// and the refresher must NOT be called.
func TestVault_Renew_NonLongLivedToken_NoRefreshToken_Errors(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 5
	// Expired Bearer token with no refresh token. The fast path returns
	// "expired at ..." which is matched by isExpiryError, so the slow
	// path is taken. The slow path opens the lock tx, acquires the
	// advisory lock, re-reads (still expired), then calls
	// extractRefreshMaterial — which returns the descriptive error
	// because the token is Bearer (not LongLived) and has no refresh.
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "")
	store.seedToken(expired)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		t.Fatal("refresher must NOT be called when no refresh material is available")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error when token is expired, non-long-lived, and has no refresh token")
	}
	if !strings.Contains(err.Error(), "no refresh token available") {
		t.Errorf("error must mention 'no refresh token available' (extractRefreshMaterial is the source); got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v (this path must ROLLBACK the lock tx — no Save was attempted)", err)
	}
	if store.saveCalls.Load() != 0 {
		t.Errorf("SaveToken must NOT be called when refresh material is unavailable; got %d", store.saveCalls.Load())
	}
}

// -----------------------------------------------------------------------
// Context cancellation
// -----------------------------------------------------------------------

// TestVault_Renew_ContextAlreadyCancelled_FastPath asserts that a
// pre-cancelled context short-circuits Renew with the context error
// (or, on the slow path, before any DB work).
func TestVault_Renew_ContextAlreadyCancelled(t *testing.T) {
	v, _, _ := newTestVault(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := v.Renew(ctx, 1, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		t.Fatal("refresher must NOT be called when context is already cancelled")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	// Use a typed compare to allow either context.Canceled or its
	// wrapping. The vault returns the raw ctx.Err() on the fast path.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error must wrap context.Canceled; got %v", err)
	}
}

// -----------------------------------------------------------------------
// Save / Get / Revoke — non-renewal paths get a sanity check
// -----------------------------------------------------------------------

// TestVault_Save_Get_Revoke_RoundTrip is a quick happy-path for the
// non-Renew vault methods, so a regression in their signature doesn't
// get masked by Renew-specific tests.
func TestVault_Save_Get_Revoke_RoundTrip(t *testing.T) {
	v, _, store := newTestVault(t)
	ctx := context.Background()
	const accountID int64 = 1

	// Save
	if err := v.Save(ctx, accountID, &models.TokenData{
		AccessToken:  "the-access",
		RefreshToken: "the-refresh",
		TokenType:    "bearer",
		ExpiresIn:    3600,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.saveCalls.Load() != 1 {
		t.Errorf("SaveToken calls: want 1, got %d", store.saveCalls.Load())
	}

	// Get — the mock's default FindLatestToken reads the just-saved
	// row from its state map, so no findLatestFn override is needed.
	// The Get path will also check the stored ExpiresAt; Save sets it
	// to NOW + ExpiresIn = NOW + 1h, which is fresh.
	got, err := v.Get(ctx, accountID, models.TokenTypeBearer)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "the-access" {
		t.Errorf("Get returned access token: want %q, got %q", "the-access", got.AccessToken)
	}

	// Revoke
	if err := v.Revoke(ctx, accountID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.deleteCalls.Load() != 1 {
		t.Errorf("DeleteAllTokensForPlatformAccount calls: want 1, got %d", store.deleteCalls.Load())
	}
	// After Revoke, Get must return a "no token" error (state cleared).
	if _, err := v.Get(ctx, accountID, models.TokenTypeBearer); err == nil {
		t.Error("Get after Revoke must return an error (state cleared)")
	}
}

// TestVault_Revoke_NotFound_TreatedAsSuccess proves Revoke is
// idempotent: deleting from an account that has no tokens must
// return nil (not propagate the "not found" error from the store).
func TestVault_Revoke_NotFound_TreatedAsSuccess(t *testing.T) {
	v, _, store := newTestVault(t)
	store.deleteAllFn = func(int64) error {
		return errors.New("token not found: platform_account_id=1")
	}
	if err := v.Revoke(context.Background(), 1); err != nil {
		t.Errorf("Revoke must swallow 'token not found' (idempotent disconnect): got %v", err)
	}
}
