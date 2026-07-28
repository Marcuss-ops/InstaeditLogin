// Package vault provides a canonical credentials.VaultAPI test
// double used by integration / e2e suites across the codebase. The
// helper exists so every test that needs a real VaultAPI-shaped
// value (api.MustNewRouter's mandatory WithCredentialVault option,
// AuthenticatedDriveSource's mandatory vault wiring, future
// worker-side tests) can construct one without re-declaring the
// five-method interface surface or stubbing one method at a time.
//
// The FakeVault pattern is intentionally kept MINIMAL: only the
// methods the canonical test paths actually invoke are real. The
// rest are explicit "not implemented in test fake" errors so a
// future test that exercises an unimplemented path short-circuits
// loudly instead of silently passing (which a panic or nil-deref
// would not catch because the method body returns nil today).
//
// Where the test INVENTORY lives today:
//   - internal/worker/authenticated_drive_source_test.go::fakeVault
//     — pre-existing identical 5-method struct. Single-sourced here;
//     the worker copy retained locally for the three tests in that
//     file to avoid a cross-package import churn for a tiny lift.
//     A follow-up commit can fold that one into the canonical export
//     if the duplication budget exceeds the cross-package-import cost.
//   - pkg/api/fakevault_test.go::fakeVault (test-side) — pkg/api's
//     own batch-import test fake. Different shape (records Save /
//     Revoke on struct fields for assertion introspection) so it
//     stays in package api; not a candidate for unification.
//
// The package compiles unconditionally (no //go:build integration
// tag): only the stdlib + internal/credentials + internal/models
// are referenced. The build tags live on the TEST FILES that
// trigger actual vault.Save/Renew calls (today: -tags=integration
// on the worker test, -tags=e2e on the e2e suite); this package
// itself is part of the always-built graph.
//
// Contract surface (mirrors credentials.VaultAPI exactly):
//   - Save:     errors with a typed sentinel — no-op-by-error so a
//     test that wires a fake but accidentally traverses
//     Save surfaces a recognisable error in CI logs.
//   - Get:      same shape as Save.
//   - Rotate:   same shape as Save (semantic alias in production;
//     test fake collapses both).
//   - Renew:    the ONLY method usually invoked by integration /
//     e2e tests. Records the (accountID, refresh material)
//     pair on struct fields so future tests can assert
//     renewCalls == 1, renewAccess == "fake-access", etc.
//     Honours renewErr for scripted failure paths.
//   - Revoke:   same shape as Save.
//
// Helper constructors:
//   - NewFakeVault()  — zero-value, all methods return canned
//     /sentinel values. Use as &vault.FakeVault{} or
//     vault.NewFakeVault().
package vault

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// errFakeVaultNotImplemented is the typed-sentinel error returned
// by methods the FakeVault does NOT implement today. Lifted from
// production telemetry breadcrumbs — when a future test accidentally
// traverses Save/Rotate/Revoke, the [lastErr] substring "not
// implemented in test fake (pkg: internal/testutil/vault)" makes
// the cause unambiguous in CI logs without a debugger.
var errFakeVaultNotImplemented = errors.New("not implemented in test fake (pkg: internal/testutil/vault)")

// FakeVault is a credentials.VaultAPI test double. The struct field
// set is minimal (just the four Renew-related fields); methods that
// production doesn't dispatch during the canonical test paths return
// the typed sentinel error above.
//
// The mutex protects renewAccess / renewHandoff / renewCalls /
// renewErr so concurrent tests (TestParallel groups, the e2e suite
// running under -parallel N) don't race the field writes. A single
// embed is enough; we don't use sync/atomic because the access
// patterns are read-after-write within a single test body.
type FakeVault struct {
	mu           sync.Mutex
	renewCalls   int
	renewErr     error
	renewAccess  string
	renewHandoff string
}

// NewFakeVault returns a zero-valued FakeVault. Live mutations are
// safe under the embedded sync.Mutex — pre-script renewErr before
// calling NewFakeVault if the test needs a scripted failure path.
//
// Compared to &vault.FakeVault{} literal: identical; the helper
// just reads more obviously at the call site (FakeVault{} looks
// like a struct-init pattern, NewFakeVault() reads as a
// constructor).
func NewFakeVault() *FakeVault { return &FakeVault{} }

// Save returns the typed sentinel — production code that
// dispatches Save through a FakeVault will see an error of shape
// "not implemented in test fake (pkg: internal/testutil/vault)"
// in CI logs. This is the desired failure mode: surfacing a
// "production wired a fake by accident" regression immediately
// rather than silently swallowing.
// func (f *FakeVault) Save is NOT here on purpose: comments below.

func (f *FakeVault) Save(_ context.Context, _ int64, _ *models.TokenData) error {
	return errFakeVaultNotImplemented
}

func (f *FakeVault) Get(_ context.Context, _ int64, _ string) (*models.OAuthToken, error) {
	return nil, errFakeVaultNotImplemented
}

func (f *FakeVault) Rotate(_ context.Context, _ int64, _ *models.TokenData) error {
	return errFakeVaultNotImplemented
}

// Renew increments renewCalls on every call so future tests that
// assert "Renew was called exactly N times" (the pattern
// routes_test.go::TestOAuthCallback_* uses today) get a faithful
// count instead of a silent no-op. The handoff's AccessToken is
// recorded into renewAccess (mirrors the worker fakeVault's
// renewAccess field) so a future test that asserts on the
// post-refresh access token gets the real value rather than a
// stall. The handoff's refresh argument is recorded into
// renewHandoff, prefixed by "stored-refresh-" by convention so
// tests can assert the resolved value matches what the production
// vault would have read from the tokens table.
//
// Honours renewErr before invoking the handoff so a test that
// pre-scripts a refresh failure sees the canned error without
// ever reaching the refresher closure.
func (f *FakeVault) Renew(
	ctx context.Context,
	accountID int64,
	_ string,
	handoff credentials.TokenRefresher,
) (*models.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCalls++
	if f.renewErr != nil {
		return nil, f.renewErr
	}
	// fakeVault simulates a stored refresh_token of "stored-refresh-{accountID}".
	refresh := "stored-refresh-" + strconv.FormatInt(accountID, 10)
	f.renewHandoff = refresh
	tok, err := handoff(ctx, refresh)
	if err != nil {
		return nil, err
	}
	f.renewAccess = tok.AccessToken
	return &models.OAuthToken{
		AccessToken: tok.AccessToken,
		TokenType:   tok.TokenType,
	}, nil
}

func (f *FakeVault) Revoke(_ context.Context, _ int64) error {
	return errFakeVaultNotImplemented
}

// Compile-time assertion: any future change to credentials.VaultAPI
// surfaces here as a build error (NOT a runtime panic). Pinned at
// `go vet` time.
var _ credentials.VaultAPI = (*FakeVault)(nil)
