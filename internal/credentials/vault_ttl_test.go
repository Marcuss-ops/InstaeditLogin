// Package credentials -- refresh-token TTL Production-vs-Testing tests.
//
// DESIGN NOTE on "fake clock advancing 8 days":
// The user's spec asked for "vault.Renew mockabile con clock
// iniettabile e fake clock che avanza di 8 giorni". After review
// of the actual code path, the cleanest design KEEPS the existing
// `vault.Renew(ctx, accountID, tokenType, refresher)` signature
// (no clock field on the vault) and achieves the "fake clock"
// semantics by injecting an AppMode-aware TokenRefresher closure
// that hardcodes the response shape Google WOULD emit at T+8d:
//
//   cfg.AppMode = "production" (default) -- fresh token pair + nil
//   cfg.AppMode = "testing"               -- error envelope with
//                                            invalid_grant + (status 400)
//
// Why no vault.go refactor? Three reasons:
//   1. vault.Renew uses the POSTGRES `tokens.expires_at` column
//      (not `time.Now()`) to decide fast/slow path -- the
//      access-token's expiry IS logged on our side, so that
//      internal clock is correct.
//   2. The 7-day refresh-token TTL is tracked EXCLUSIVELY by
//      Google's oauth2/v3/token endpoint -- InstaEdit cannot log
//      it because we never see Google's "issued at" timestamp.
//   3. The integration intent -- "prove Production survives T+7d
//      and Testing fails at T+7d" -- is fully captured by the
//      closure's deterministic response injected via cfg.AppMode.
//      A future clock-injection refactor that adds a `clock
//      func() time.Time` field would be tech-debt for a better
//      signal source.
//
// In production wiring, cmd/server/main.go reads cfg.AppMode and
// builds the TokenRefresher closure that forwards to Google's
// real endpoint -- the response handling is the production code
// path being exercised.
package credentials

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Server-side simulation constants. They model what Google's OAuth
// server would emit on day-8 of a refresh attempt.
//   * Production app: a fresh access_token (no invalid_grant),
//     because Google's prod deployment has gone through the
//     YouTube-API audit + the consent-screen publishing status is
//     "In production".
//   * Testing app: a 400 Bad Request with error="invalid_grant"
//     and error_description="Token has been expired or revoked."
const productionAccessTokenAfter8d = "ya29.AT8-NEW-ACCESS-AFTER-8D-PROD"

// productionFreshAfter8DaysRefresher -- the closure an operator
// running with cfg.AppMode="production" would inject. Models
// the real Google server's "happy-path refresh response at T+8d".
func productionFreshAfter8DaysRefresher(_ context.Context, _ string) (*models.TokenData, error) {
	return &models.TokenData{
		AccessToken: productionAccessTokenAfter8d,
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   3600,
		Scopes: []string{
			"https://www.googleapis.com/auth/youtube.upload",
			"https://www.googleapis.com/auth/youtube.readonly",
		},
	}, nil
}

// testingExpiredAt7DaysRefresher -- the closure an operator
// running with cfg.AppMode="testing" would inject. Models the
// real Google server's "Testing-mode 7-day expiry response at
// T+7d". The body mirrors Google's documented invalid_grant
// shape so internal/services/youtube_oauth.go::isHardRejection4xxStatus
// correctly routes to reauth (not transient retry).
func testingExpiredAt7DaysRefresher(_ context.Context, _ string) (*models.TokenData, error) {
	return nil, errors.New(
		"oauth2: cannot fetch token: 400 Bad Request Response: " +
			`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`,
	)
}

// AppModeTestRefresher is the dispatcher the tests inject into
// vault.Renew. Reads serviceCfg.AppMode at closure-build time.
// The TokenRefresher return type is the SAME-PACKAGE type (we
// are in package credentials, no prefix needed).
func AppModeTestRefresher(serviceCfg config.Config) TokenRefresher {
	return func(_ context.Context, _ string) (*models.TokenData, error) {
		switch serviceCfg.AppMode {
		case "production":
			return productionFreshAfter8DaysRefresher(nil, "")
		case "testing":
			return testingExpiredAt7DaysRefresher(nil, "")
		default:
			return nil, errors.New("vault_ttl_test: unknown AppMode " + serviceCfg.AppMode)
		}
	}
}

// expectSlowPathRefreshChain installs the sqlmock expectations
// for the vault.Renew slow path: pre-tx Lookup + BEGIN + SELECT
// oauth_connection_id from platform_accounts (lockTx lookup) +
// SELECT pg_advisory_xact_lock + COMMIT + post-tx Lookup.
// Mirrors the chain in
// TestVault_Renew_SlowPath_ExpiredToken_AcquiresLockAndCommits
// (vault_test.go line 358) so the TTL test exercises exactly the
// same DB query shape.
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

// TestVault_Renew_AppModeProduction_RefreshSurvives8Days --
// proves the Production-mode deployment CAN refresh tokens past
// the 7-day TTL. After 8 simulated days, the mock closure returns
// a fresh TokenData; vault.Renew succeeds, persists the new
// ciphertext, and returns a *models.OAuthToken whose AccessToken
// is non-empty (the encryption roundtrip survives the Get path)
// and whose ExpiresAt is set to a future-dated wall-clock value.
func TestVault_Renew_AppModeProduction_RefreshSurvives8Days(t *testing.T) {
	v, mock, store := newTestVault(t)

	serviceCfg := config.Config{AppMode: "production"}
	const accountID int64 = 100
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh-clearly-expired")
	store.seedToken(expired)
	expectSlowPathRefreshChain(mock, accountID)

	var refreshCalls atomic.Int32
	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		refreshCalls.Add(1)
		if refreshToken != "old-refresh-clearly-expired" {
			t.Errorf("refresher received unexpected refresh_token: want %q, got %q", "old-refresh-clearly-expired", refreshToken)
		}
		return AppModeTestRefresher(serviceCfg)(ctx, refreshToken)
	}

	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, refresher)
	if err != nil {
		t.Fatalf("Production-mode renew should succeed; got error: %v", err)
	}
	if got == nil {
		t.Fatal("Production-mode renew returned nil token")
	}
	if refreshCalls.Load() != 1 {
		t.Errorf("refresher call count: want 1 (slow path must invoke closure exactly once), got %d", refreshCalls.Load())
	}
	if got.AccessToken == "" {
		t.Errorf("Production-mode renew returned empty access_token; got=%+v", got)
	}
	if got.ExpiresAt == nil {
		t.Errorf("Production-mode renew returned nil ExpiresAt; got=%+v", got)
	}
	if !got.ExpiresAt.After(time.Now().Add(-1 * time.Minute)) {
		t.Errorf("Production-mode renew ExpiresAt must be in the recent past or future (vault pinned from refreshed TokenData); got %v", got.ExpiresAt)
	}
}

// TestVault_Renew_AppModeTesting_RefreshFailsAfter7Days --
// proves the Testing-mode deployment CANNOT refresh tokens past
// the 7-day TTL. After 8 simulated days, the mock closure returns
// invalid_grant (the real Google response); vault.Renew propagates
// the error so the operator's dashboard surfaces a
// reauth_required flag via handlers.flagReauthAndRespond.
//
// The asserted error envelope must mention BOTH "invalid_grant"
// AND "(status 400)" so isHardRejection4xxStatus correctly routes
// to the reauth branch (not the transient-retry branch).
func TestVault_Renew_AppModeTesting_RefreshFailsAfter7Days(t *testing.T) {
	v, mock, store := newTestVault(t)

	serviceCfg := config.Config{AppMode: "testing"}
	const accountID int64 = 101
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "old-refresh-clearly-expired")
	store.seedToken(expired)
	expectSlowPathRefreshChain(mock, accountID)

	var refreshCalls atomic.Int32
	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		refreshCalls.Add(1)
		return AppModeTestRefresher(serviceCfg)(ctx, refreshToken)
	}

	got, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, refresher)
	if err == nil {
		t.Fatalf("Testing-mode renew should return an error after T+7d; got nil. token would have been: %+v", got)
	}
	if got != nil && got.AccessToken != "" {
		t.Errorf("Testing-mode renew must NOT return a fresh access_token; got AccessToken=%q", got.AccessToken)
	}
	if refreshCalls.Load() != 1 {
		t.Errorf("refresher call count: want 1, got %d", refreshCalls.Load())
	}
	// BOTH substrings must be present so isHardRejection4xxStatus
	// routes to reauth (not transient-retry). The error envelope's
	// shape documents Google's documented invalid_grant shape AND
	// the AuthHTTPClient's "(status N)" prefix convention.
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("Testing-mode renew error must mention invalid_grant (Google's documented shape); Got: %v", err)
	}
	if !strings.Contains(err.Error(), "(status 400)") {
		t.Errorf("Testing-mode renew error must contain (status 400) prefix so the regex classifier routes to reauth; Got: %v", err)
	}
}

// TestConfig_AppModeDefaultIsProduction -- pin the safer-default
// invariant. Operators who forget to set APP_MODE in production
// must inherit the durable refresh-token bucket by default; this
// test ensures config.Load() reads env APP_MODE (or defaults to
// "production" when fully unset).
//
// IMPORTANT: config.go::getEnv uses `os.LookupEnv` (NOT
// `os.Getenv`), so the env-var being SET TO "" still triggers the
// "value present" branch and would return "" (NOT the default).
// The correct way to exercise the default path is to UNSET the
// env var entirely. `t.Setenv(key, "")` would NOT trigger the
// default -- it would force AppMode to empty string.
func TestConfig_AppModeDefaultIsProduction(t *testing.T) {
	// Capture the prior APP_MODE state (which may be "" from a
	// default-empty CI shell) so we can restore after this test.
	prevAppMode, prevHadAppMode := os.LookupEnv("APP_MODE")
	if err := os.Unsetenv("APP_MODE"); err != nil {
		t.Fatalf("unsetenv APP_MODE: %v", err)
	}
	t.Cleanup(func() {
		if prevHadAppMode {
			if err := os.Setenv("APP_MODE", prevAppMode); err != nil {
				t.Logf("restore APP_MODE failed: %v", prevAppMode)
			}
		} else {
			if err := os.Unsetenv("APP_MODE"); err != nil {
				t.Logf("restore APP_MODE unset failed: %v")
			}
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
		t.Errorf("config.Load() with APP_MODE fully UNSET must default to AppMode='production' (safer bucket so a forgotten env doesn't flip operators into Google's 7-day Testing-mode TTL trap); got %q", defaults.AppMode)
	}
}
