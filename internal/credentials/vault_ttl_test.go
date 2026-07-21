// Package credentials -- refresh-token TTL Production-vs-Testing tests.
//
// DESIGN: real clock injection with deterministic seeding.
//
// Two invariants pinned here:
//
//	I-1 -- vault.Renew reads the "now" through v.clock(), not
//	       time.Now(). A regression that re-introduced time.Now()
//	       anywhere in vault.go would fail the ExpiresAt.Equal(want)
//	       assertion below.
//
//	I-2 -- the 7-day Testing-mode TTL boundary lives ONLY in
//	       AppMode=testing. Production is durable past day 7.
//
// seeding determinism:
//
//	newEncryptedToken computes ExpiresAt = time.Now().Add(duration),
//	using wall-clock time. Vault.Renew's pre-tx FAST-PATH gate
//	`tok.ExpiresAt.Sub(v.clock()) > 60s` is non-deterministic if
//	the seeded.ExpiresAt drifts >60s relative to wall-clock time
//	the test runs. The CI environment can be anywhere on a 24h
//	wall-clock; without anchoring the seed to fc.t, the test could
//	silently take the FAST PATH and never call the closure.
//
//	seedExpired (defined in this file) wraps newEncryptedToken for
//	the encryption step but overrides ExpiresAt = fc.t - 1m AFTER
//	the helper returns. This guarantees the relative gap between
//	seeded.ExpiresAt and fc.t is exactly 1 minute, deterministic
//	regardless of wall-clock now.
package credentials

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeClock is a time-progression driver used by every test below.
// Calling fc.Set(t) advances the simulated "now"; all vault gates
// that ask "is this token mature enough to refresh?" will read
// fc.Now() because v.SetClock(fc.Now) was called in setup.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time  { return c.t }
func (c *fakeClock) Set(t time.Time) { c.t = t }

// ttlAwareClosure models what Google's real oauth2/v3/token endpoint
// would emit given the simulated time + the configured AppMode.
//
//	elapsed      < 7d      -- fresh token pair, regardless of mode
//	elapsed     >= 7d
//	  AppMode=production  -- still a fresh pair (durable tokens)
//	  AppMode=testing     -- invalid_grant (Testing-mode 7-day TTL rule)
func ttlAwareClosure(baseTime time.Time, appMode string, fc *fakeClock) TokenRefresher {
	return func(_ context.Context, refreshToken string) (*models.TokenData, error) {
		elapsed := fc.Now().Sub(baseTime)
		if appMode == "testing" && elapsed >= 7*24*time.Hour {
			return nil, errors.New(
				"oauth2: cannot fetch token: 400 Bad Request Response: " +
					`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}` +
					" (status 400)",
			)
		}
		return &models.TokenData{
			AccessToken:  "fresh-access-at-" + fc.Now().Format(time.RFC3339) + "-" + appMode,
			RefreshToken: "fresh-refresh-" + appMode,
			ExpiresIn:    3600,
			TokenType:    models.TokenTypeBearer,
			Scopes: []string{
				"https://www.googleapis.com/auth/youtube.upload",
				"https://www.googleapis.com/auth/youtube.readonly",
			},
		}, nil
	}
}

// expectSlowPathRefreshChain installs the sqlmock expectations the
// vault.Renew slow path will issue: pre-tx oauth_connection lookup,
// BEGIN, the in-lock oauth_connection_id lookup, the advisory-lock
// SELECT, COMMIT, then the post-tx oauth_connection lookup. Mirrors
// the shape of TestVault_Renew_SlowPath_ExpiredToken_AcquiresLockAndCommits
// in vault_test.go so the TTL tests exercise exactly the same DB
// query chain.
func expectSlowPathRefreshChain(mock sqlmock.Sqlmock, accountID int64) {
	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)
}

// seedExpired deterministically pins the seeded token's ExpiresAt to
// fc.t + (-1 min), bypassing newEncryptedToken's wall-clock ExpiresAt
// default. Without this anchor, the pre-tx FAST-PATH gate in
// vault.Renew could silently take the wrong branch depending on the
// wall-clock moment the test runs.
//
//	fc.t         -- the FAKE clock value at the time seedExpired was called
//	                (typically T0; the helper does not advance fc.t).
//	fc.t - 1m    -- the deterministic ExpiresAt on the seeded Token.
//
// The 1-minute offset is far enough below fc.t that the FAST PATH
// gate `ExpiresAt.Sub(fc.t) > 60s` reliably fails (-1m vs >60s is
// always FALSE), forcing the SLOW PATH where the closure is invoked.
// It is also long enough above fc.t to model "expired 1 minute ago"
// -- semantically the test's "expired token starting state".
func seedExpired(t *testing.T, v *CredentialVault, store *mockTokenStore, fc *fakeClock, accountID int64, refreshToken string) {
	t.Helper()
	// Pass 0 to newEncryptedToken so the helper's internal
	// `time.Now().Add(0)` is irrelevant -- we overwrite ExpiresAt
	// immediately below with the fc-anchored value.
	expired := newEncryptedToken(t, v, accountID, 0, refreshToken)
	expiresAt := fc.t.Add(-1 * time.Minute)
	expired.ExpiresAt = &expiresAt
	store.seedToken(expired)
}

// TestVault_Renew_ProductionMode_T8d_RefreshSucceeds -- spec contract:
//
//	(1) fc.Set(T0).
//	(2) v.SetClock(fc.Now) -- vault now reads from fc.
//	(3) seedExpired at fc.t (=T0) -- expiresAt pinned to T0-1m.
//	(4) fc.Set(T0 + 8d) -- advance simulated time.
//	(5) Hand the vault a production-mode closure.
//	(6) Call vault.Renew.
//	(7) Assert: no err, AccessToken populated, AND persisted
//	    ExpiresAt.Equal(fc.t + 3600s) -- proving the injection
//	    flows through v.clock() inside saveForOAuthConnection.
//
// A regression in vault.go that re-introduced time.Now() would
// surface here: got.ExpiresAt would equal wall-clock-now + 3600s,
// NOT fc.t + 3600s, and the assertion would fail sharply without
// waiting 8 real-world days.
func TestVault_Renew_ProductionMode_T8d_RefreshSucceeds(t *testing.T) {
	v, mock, store := newTestVault(t)

	T0 := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	fc := &fakeClock{}
	fc.Set(T0)
	v.SetClock(fc.Now)

	const accountID int64 = 100
	expectSlowPathRefreshChain(mock, accountID)
	seedExpired(t, v, store, fc, accountID, "old-refresh-production-8d")

	// Advance to T0 + 8d. The closure sees fc.Now()=T0+8d and emits
	// a fresh token pair (production is durable past the 7d boundary).
	fc.Set(T0.Add(8 * 24 * time.Hour))

	closure := ttlAwareClosure(T0, "production", fc)
	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, closure)
	if err != nil {
		t.Fatalf("vault.Renew at T+8d / AppMode=production must succeed (durable refresh tokens); got err=%v", err)
	}
	if got == nil {
		t.Fatal("vault.Renew returned nil token without error")
	}
	if got.AccessToken == "" {
		t.Errorf("AccessToken must be non-empty after a successful refresh; got=%+v", got)
	}
	if got.ExpiresAt == nil {
		t.Fatalf("ExpiresAt must be set with the injected clock; got nil. vault.SetClock wiring is mis-threaded.")
	}
	wantExpiresAt := fc.t.Add(3600 * time.Second)
	if !got.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf("ExpiresAt mis-aligned with injected clock (vault.clock() reads %v but ExpiresAt=%v); want %v -- a regression in vault.go re-introduced time.Now() somewhere",
			fc.t, got.ExpiresAt, wantExpiresAt)
	}
}

// TestVault_Renew_ProductionMode_T7d_StillSucceeds -- boundary belt-
// and-braces. fakeClock.Set(T0 + 7 * 24h) under AppMode=production.
// Production must STILL succeed at the exact day-7 boundary (the
// durable refresh-token invariant).
func TestVault_Renew_ProductionMode_T7d_StillSucceeds(t *testing.T) {
	v, mock, store := newTestVault(t)

	T0 := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	fc := &fakeClock{}
	fc.Set(T0)
	v.SetClock(fc.Now)

	const accountID int64 = 101
	expectSlowPathRefreshChain(mock, accountID)
	seedExpired(t, v, store, fc, accountID, "old-refresh-production-7d")

	fc.Set(T0.Add(7 * 24 * time.Hour)) // boundary

	closure := ttlAwareClosure(T0, "production", fc)
	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, closure)
	if err != nil {
		t.Fatalf("vault.Renew at T+7d / AppMode=production must STILL succeed (durable refresh tokens across boundary); got err=%v", err)
	}
	if got == nil || got.AccessToken == "" {
		t.Fatalf("vault.Renew at T+7d / AppMode=production returned unusable token: %+v", got)
	}
}

// TestVault_Renew_TestingMode_T7d_FailsInvalidGrant -- spec contract:
// Crossing the 7-day Testing-mode boundary MUST yield Google's
// documented invalid_grant envelope. Error must contain BOTH
// "invalid_grant" AND "(status 400)" so
// internal/services/youtube_oauth.go::isHardRejection4xxStatus
// routes to the reauth branch (not transient retry).
func TestVault_Renew_TestingMode_T7d_FailsInvalidGrant(t *testing.T) {
	v, mock, store := newTestVault(t)

	T0 := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	fc := &fakeClock{}
	fc.Set(T0)
	v.SetClock(fc.Now)

	const accountID int64 = 102
	expectSlowPathRefreshChain(mock, accountID)
	seedExpired(t, v, store, fc, accountID, "old-refresh-testing-7d")

	// Advance to T0 + 7d. Crossing the boundary under AppMode=testing
	// forces the closure to emit Google's documented invalid_grant
	// envelope.
	fc.Set(T0.Add(7 * 24 * time.Hour))

	closure := ttlAwareClosure(T0, "testing", fc)
	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, closure)
	if err == nil {
		t.Fatalf("vault.Renew at T+7d / AppMode=testing MUST return invalid_grant; got nil err. token would have been: %+v", got)
	}
	if got != nil && got.AccessToken != "" {
		t.Errorf("vault.Renew at T+7d / AppMode=testing MUST NOT return a fresh access_token; got AccessToken=%q", got.AccessToken)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("Testing-mode T+7d error must mention invalid_grant (Google documented shape); Got=%v", err)
	}
	if !strings.Contains(err.Error(), "(status 400)") {
		t.Errorf("Testing-mode T+7d error must carry (status 400) prefix so isHardRejection4xxStatus routes to reauth; Got=%v", err)
	}
}

// TestVault_Renew_TestingMode_T7dMinus1h_StillWorks -- boundary
// regression guard: when fakeClock is set to T+7d-1h (i.e. strictly
// INSIDE the testing-mode grace window), the app must NOT fail yet.
// A regression in the closure's elapsed-time check that flipped the
// boundary one hour early would surface here.
func TestVault_Renew_TestingMode_T7dMinus1h_StillWorks(t *testing.T) {
	v, mock, store := newTestVault(t)

	T0 := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	fc := &fakeClock{}
	fc.Set(T0)
	v.SetClock(fc.Now)

	const accountID int64 = 103
	expectSlowPathRefreshChain(mock, accountID)
	seedExpired(t, v, store, fc, accountID, "old-refresh-testing-7d-minus-1h")

	// 7d - 1h: strictly inside the Testing-mode grace window. Closure
	// emits a fresh token pair.
	fc.Set(T0.Add(7*24*time.Hour - time.Hour))

	closure := ttlAwareClosure(T0, "testing", fc)
	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, closure)
	if err != nil {
		t.Fatalf("vault.Renew at T+7d-1h / AppMode=testing must STILL succeed (closure emits fresh tokens inside grace window); got err=%v", err)
	}
	if got == nil || got.AccessToken == "" {
		t.Fatalf("vault.Renew at T+7d-1h / AppMode=testing returned unusable token: %+v", got)
	}
}

// TestConfig_AppModeDefaultIsProduction -- pin the safer-default
// invariant. Operators who forget to set APP_MODE in production
// must inherit the durable refresh-token bucket. config.go::getEnv
// uses os.LookupEnv, so an env-var SET TO "" still triggers the
// "value present" branch and would return "" (NOT the default).
// The ONLY way to exercise the default path is to UNSET the env.
func TestConfig_AppModeDefaultIsProduction(t *testing.T) {
	prevAppMode, prevHadAppMode := os.LookupEnv("APP_MODE")
	if err := os.Unsetenv("APP_MODE"); err != nil {
		t.Fatalf("unsetenv APP_MODE: %v", err)
	}
	t.Cleanup(func() {
		if prevHadAppMode {
			_ = os.Setenv("APP_MODE", prevAppMode)
		} else {
			_ = os.Unsetenv("APP_MODE")
		}
	})

	defaults, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() failed: %v", err)
	}
	if defaults == nil {
		t.Fatal("config.Load() returned nil Config")
	}
	if defaults.AppMode != "production" {
		t.Errorf("config.Load() with APP_MODE UNSET must default to AppMode='production' (safer bucket so a forgotten env doesn't flip operators into Google's 7-day Testing-mode TTL trap); got %q", defaults.AppMode)
	}
}
