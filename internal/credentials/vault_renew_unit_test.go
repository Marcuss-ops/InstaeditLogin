package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestVault_Renew_FastPath_FreshToken_NoLockAcquisition(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 10
	// ExpiresAt 5 minutes in the future — well outside the 60s grace window.
	fresh := newEncryptedToken(t, v, accountID, 5*time.Minute, "old-refresh")
	store.seedToken(fresh)
	// P0#3: vault resolves oauth_connection_id via the DB on every
	// Renew probe (Lookup → Get fast path). The fast-path advisory
	// lock contract is unchanged: still no BEGIN, still no
	// pg_advisory_xact_lock — just one extra SELECT before Get.
	expectOauthConnLookup(mock, accountID, accountID)

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
		t.Errorf("DeleteAllTokensForOAuthConnection calls: want 0 on fast path, got %d", store.deleteCalls.Load())
	}
}

func TestVault_Renew_SlowPath_ExpiredToken_AcquiresLockAndCommits(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 42
	// ExpiresAt in the past — must trigger the slow path. seedToken
	// (not findLatestFn) is the right primitive here because the vault
	// will call SaveToken after refresh and then Get — the final Get
	// must see the FRESH row written by SaveToken, not the expired row.
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)
	// P0#3: fast-path probe issues the oauth_connection_id lookup.
	// (accountID=42 maps identity → oauthConnectionID=42 for the
	// advisory lock key in the BEGIN block below.)
	expectOauthConnLookup(mock, accountID, accountID)

	// SQL sequence (strict order):
	//   BEGIN                       (lockTx)
	//   SELECT oauth_connection_id  (lookup inside lockTx)
	//   SELECT pg_advisory_xact_lock(42)
	//   COMMIT
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

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
	// The lock transaction committed; FindLatestToken was called three
	// times total: (1) fast-path probe via v.Get, (2) in-tx re-read
	// directly via v.store.FindLatestToken (which avoids running the
	// lazy-re-encrypt path on the loser-of-race row that the pre-P0#3
	// code triggered — see vault.go::toOAuthToken godoc for the
	// contract), (3) final return via v.Get after Save. SaveToken was
	// called once (persist the refreshed token). The first two calls
	// see the expired row; the third must see the freshly-saved row
	// — which is the whole point of the state map.
	if store.findCalls.Load() != 3 {
		t.Errorf("FindLatestToken calls: want 3 (probe-via-Get + in-tx-via-store + final-via-Get), got %d", store.findCalls.Load())
	}
	if store.saveCalls.Load() != 1 {
		t.Errorf("SaveToken calls: want 1 (persist refreshed token), got %d", store.saveCalls.Load())
	}
}

func TestVault_Renew_SlowPath_WithinGraceWindow_AcquiresLock(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 7
	// ExpiresAt 30s in the future — INSIDE the 60s grace window.
	soonExpiring := newEncryptedToken(t, v, accountID, 30*time.Second, "old-refresh")
	store.seedToken(soonExpiring)
	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

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

func TestVault_Renew_LockAcquisitionFails_RollsBack(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 99
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "r")
	store.seedToken(expired)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
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

func TestVault_Renew_RefresherFails_PropagatesAndRollsBack(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 11
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	store.seedToken(expired)
	expectOauthConnLookup(mock, accountID, accountID)

	refresherErr := errors.New("simulated platform 500")
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
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

func TestVault_Renew_LongLivedToken_UsesAccessTokenAsRefreshMaterial(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 3
	// Long-lived token with NO refresh token. EncryptedToken is the
	// current long-lived access token, which is what fb_exchange_token
	// expects as input.
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "")
	expired.TokenType = models.TokenTypeLongLived
	store.seedToken(expired)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

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
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
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

// TestClassifyRefreshFailure_InvalidGrantTypedErrors pins the P1
// classification: both the domain sentinel and the provider's typed
// OAuthTokenError map to reauth_required; an untyped string that merely
// mentions invalid_grant does not.
func TestClassifyRefreshFailure_InvalidGrantTypedErrors(t *testing.T) {
	status, code := classifyRefreshFailure(fmt.Errorf("wrapped: %w", ErrInvalidGrant))
	if status != models.AccountStatusReauthRequired || code != "invalid_grant" {
		t.Errorf("sentinel: want (reauth_required, invalid_grant), got (%q, %q)", status, code)
	}
	providerErr := &OAuthTokenError{
		StatusCode:  400,
		Code:        "invalid_grant",
		Description: "Token has been expired or revoked.",
	}
	status, code = classifyRefreshFailure(providerErr)
	if status != models.AccountStatusReauthRequired || code != "invalid_grant" {
		t.Errorf("typed provider error: want (reauth_required, invalid_grant), got (%q, %q)", status, code)
	}
	status, code = classifyRefreshFailure(errors.New("oauth2.googleapis.com: invalid_grant"))
	if status != "error" || code != "refresh_failed" {
		t.Errorf("untyped string: want (error, refresh_failed), got (%q, %q)", status, code)
	}
	status, code = classifyRefreshFailure(errors.New("upstream 500"))
	if status != "error" || code != "refresh_failed" {
		t.Errorf("generic: want (error, refresh_failed), got (%q, %q)", status, code)
	}
}

// TestVault_Renew_NearRefreshExpiry_Warns pins the P3 signal: when the
// stored refresh grant is within the 7-day warning window, Renew emits a
// warning that names the account (never the token material).
func TestVault_Renew_NearRefreshExpiry_Warns(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 8

	var logs bytes.Buffer
	v.logger = slog.New(slog.NewTextHandler(&logs, nil))

	tok := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	near := time.Now().Add(3 * 24 * time.Hour)
	tok.RefreshTokenExpiresAt = &near
	store.seedToken(tok)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return &models.TokenData{AccessToken: "fresh", TokenType: "bearer", ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
	if !strings.Contains(logs.String(), "refresh grant is nearing") {
		t.Errorf("expected near-expiry warning in logs; got %q", logs.String())
	}
	if strings.Contains(logs.String(), "old-refresh") {
		t.Errorf("warning must not contain token material; got %q", logs.String())
	}
}

// TestVault_Renew_DistantRefreshExpiry_NoWarning pins the P3 lookahead
// boundary: a refresh grant with >7 days remaining must NOT warn.
func TestVault_Renew_DistantRefreshExpiry_NoWarning(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 9

	var logs bytes.Buffer
	v.logger = slog.New(slog.NewTextHandler(&logs, nil))

	tok := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh")
	far := time.Now().Add(30 * 24 * time.Hour)
	tok.RefreshTokenExpiresAt = &far
	store.seedToken(tok)
	expectOauthConnLookup(mock, accountID, accountID)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return &models.TokenData{AccessToken: "fresh", TokenType: "bearer", ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
	if strings.Contains(logs.String(), "refresh grant is nearing") {
		t.Errorf("distant refresh expiry must NOT warn; got %q", logs.String())
	}
}

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
