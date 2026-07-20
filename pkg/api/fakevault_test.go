package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeVault is a credentials.VaultAPI test double used by the
// drive_batch/import test harness. wired via WithCredentialVault in
// newBatchImportTestRouterWithIdem so the 5 batch-import tests
// that exercised code paths requiring a non-nil vault stop
// failing with `501 credential vault not configured`.
//
// Design (incremental polish on top of the c3a4d31 baseline):
//   - Save / Revoke / Renew RECORD invocations on struct fields so
//     future tests that assert `vault.Save was called N times`
//     (the pattern routes_test.go::TestOAuthCallback_* uses today)
//     get accurate call counts; mirrors the existing
//     internal/worker fakeVault pattern in
//     authenticated_drive_source_test.go.
//   - DownloadFile (on services.DriveImporter) returns a TYPED
//     sentinel error so any future test that actually triggers
//     the downloader path short-circuits loudly instead of
//     nil-deref-panicking on resp.Body.Close() (which the c3a4d31
//     (nil, nil) return silently allowed).
type fakeVault struct {
	// saved accumulates every (accountID, tokenData) Save pair
	// in invocation order. fakeVaultPair is a local-only helper
	// struct. (An earlier draft referenced models.TokenDataPair
	// which does NOT exist; do not re-introduce that alias.)
	saved       []fakeVaultPair
	saveCount   int
	revokeCount int
	revoked     []int64
	renewErr    error
	saveErr     error
	getErr      error
}

// fakeVaultPair is the (accountID, tokenData) helper used by
// fakeVault.Save. Local to this test file; does not reference
// any non-existent model symbol.
type fakeVaultPair struct {
	AccountID int64
	TokenData *models.TokenData
}

// Save records every invocation so future tests that count
// vault.Save calls (e.g. routes_test.go::TestOAuthCallback_YoutubeChannelAttachesChannelID
// asserts `vault.Save was called`) get a faithful trace instead
// of a silent no-op.
func (f *fakeVault) Save(_ context.Context, accountID int64, td *models.TokenData) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, fakeVaultPair{AccountID: accountID, TokenData: td})
	f.saveCount++
	return nil
}

// Get returns a canned *models.OAuthToken matching the requested
// tokenType so handlers that read the cached token before
// calling Drive see the expected shape. honour getErr first so
// tests can pre-script error paths.
func (f *fakeVault) Get(_ context.Context, _ int64, tokenType string) (*models.OAuthToken, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &models.OAuthToken{
		TokenType:   tokenType,
		AccessToken: "fake-access-for-test",
	}, nil
}

// Rotate: no-op today (not exercised by the 5 fixed tests).
func (f *fakeVault) Rotate(_ context.Context, _ int64, _ *models.TokenData) error {
	return nil
}

// Renew returns a canned access token, or renewErr if a test
// pre-scripted (e.g. simulating refresh-token-revoked path).
func (f *fakeVault) Renew(_ context.Context, _ int64, tokenType string, _ credentials.TokenRefresher) (*models.OAuthToken, error) {
	if f.renewErr != nil {
		return nil, f.renewErr
	}
	return &models.OAuthToken{
		TokenType:   tokenType,
		AccessToken: "fake-renewed-bearer-for-test",
	}, nil
}

// Revoke records every accountID; tests asserting on revocation
// ordering can introspect f.revoked.
func (f *fakeVault) Revoke(_ context.Context, accountID int64) error {
	f.revoked = append(f.revoked, accountID)
	f.revokeCount++
	return nil
}

// errFakeVaultDownloadNotStubbed is the typed sentinel returned
// by DownloadFile. Lifted from the driveBatchFakeVault pattern in
// internal/worker/drive_batch_crawler_test.go.
var errFakeVaultDownloadNotStubbed = errors.New("fakeVault.DownloadFile not stubbed (pkg/api fakeVault); wire a real google oauth flow for this test path")

// DownloadFile returns the typed sentinel so any future test
// that exercises the downloader path short-circuits with a
// recognisable error rather than nil-deref-panicking.
func (f *fakeVault) DownloadFile(_ context.Context, _, _ string) (*http.Response, error) {
	return nil, errFakeVaultDownloadNotStubbed
}

// Compile-time assertions: any future change to either interface
// surfaces here as a build error (NOT a runtime panic). Pinned at
// go vet time.
var (
	_ credentials.VaultAPI  = (*fakeVault)(nil)
	_ = errFakeVaultDownloadNotStubbed
)
