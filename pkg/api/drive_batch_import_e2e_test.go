package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// =====================================================================
// TestDriveBatchImport_EndToEndAuth_VaultRefreshChainDrivesFolderListing
//
// Verifies the full auth+token+listing integration chain through
// the single-page POST /api/v1/media/import/drive/folder endpoint —
// the chain that the other ~30 batch-import tests don't exercise
// because they use the canned fakeVault whose Renew bypasses the
// closure:
//
//  1. Client → POST with valid workspace_id (owned by JWT caller)
//     + drive_account_id (a google-drive platform_account owned
//     by the JWT caller).
//  2. Handler → workspace ownership check passes.
//  3. Handler → userRepo.FindPlatformAccountByID(drive_account_id)
//     resolves the google-drive account.
//  4. Handler → vault.Renew(accountID, TokenTypeBearer, refreshFn).
//     Here the production-relevant chain runs end-to-end: a
//     recording-style renewFn override invokes the closure with
//     a hardcoded refresh string ("vault-decrypted-refresh-XYZ")
//     so we can verify it flowed through. The closure is the
//     lister's RefreshOAuthToken which returns a canned TokenData
//     with AccessToken="fake-mock-refreshed-bearer"; the renewFn
//     echoes the TokenData's AccessToken into the OAuthToken so
//     the handler sees the SAME bearer the refresher produced
//     (not a decoupled sentinel that would mask a swap).
//  5. Handler → ListFolder(ctx, folderID, driveID, AccessToken, pageToken).
//     gotToken captures the access_token the handler forwarded.
//  6. Handler → 202 + DriveBatchImportResponse with N=filesCount
//     upload_jobs queued.
//
// Pins the production-relevant integration invariant:
//   - vault.Renew invoked exactly once per request
//   - lister.RefreshOAuthToken invoked exactly once, with the
//     refresh token the vault resolved from encrypted storage
//   - ListFolder invoked exactly once, with the access_token the
//     vault produced (echoed from RefreshOAuthToken's TokenData)
//
// A regression that breaks any link — replacing the real Vault
// with a no-op, swapping the refresh closure, or feeding the
// wrong token to ListFolder — surfaces here rather than in
// production. Mirrors the assertVaultRenewedOnce /
// driveBatchFakeVault pattern in
// internal/worker/drive_batch_crawler_test.go.
// =====================================================================
func TestDriveBatchImport_EndToEndAuth_VaultRefreshChainDrivesFolderListing(t *testing.T) {
	const (
		specificRefresh   = "vault-decrypted-refresh-XYZ"
		echoedAccessToken = "fake-mock-refreshed-bearer" // value returned by lister.RefreshOAuthToken
	)
	files := []services.GoogleDriveFile{
		{ID: "e2e-1", Name: "e2e-1.mp4", MimeType: "video/mp4"},
		{ID: "e2e-2", Name: "e2e-2.mp4", MimeType: "video/mp4"},
		{ID: "e2e-3", Name: "e2e-3.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}

	// Recording-style override: actually invoke the refresher
	// closure with the hardcoded refresh token (mirrors what the
	// production CredentialVault does — looks up the encrypted
	// refresh from Postgres, invokes the closure, encrypts +
	// persists the returned TokenData). The TokenData returned
	// by RefreshOAuthToken is echoed into the OAuthToken so the
	// handler sees the SAME AccessToken the refresher produced,
	// not a decoupled sentinel that would mask a swap.
	vault := &fakeVault{
		renewFn: func(ctx context.Context, _ int64, _ string, ref credentials.TokenRefresher) (*models.OAuthToken, error) {
			td, refreshErr := ref(ctx, specificRefresh)
			if refreshErr != nil {
				return nil, refreshErr
			}
			return &models.OAuthToken{
				TokenType:   models.TokenTypeBearer,
				AccessToken: td.AccessToken,
			}, nil
		},
	}

	// Build the router inline (not via newBatchImportTestRouter
	// because the helper hardwires a vanilla fakeVault and we
	// need the recording-style override).
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", lister)
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == 99 {
				return &models.PlatformAccount{ID: 99, UserID: 1, Platform: "google-drive"}, nil
			}
			if validFacebookAccountIDs[id] {
				return &models.PlatformAccount{ID: id, UserID: 1, Platform: models.PlatformFacebook}, nil
			}
			return nil, nil
		},
		listFn: func(userID int64, _ string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	store := &mockUploadJobStore{}
	r := mustNewRouterWithDefaults(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(store),
		WithCredentialVault(vault), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))

	body := `{"folder_id":"e2e-folder","workspace_id":1,"facebook_account_id":50,"drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 (full chain success), got %d: %s", w.Code, w.Body.String())
	}

	// ─── Chain assertions ───
	if vault.renewCalls != 1 {
		t.Errorf("vault.Renew calls: want 1, got %d", vault.renewCalls)
	}
	if lister.refreshCallCount != 1 {
		t.Errorf("lister.RefreshOAuthToken calls: want 1, got %d", lister.refreshCallCount)
	}
	if lister.lastRefreshInput != specificRefresh {
		t.Errorf("lister.RefreshOAuthToken input: want %q (the refresh vault resolved from encrypted storage), got %q",
			specificRefresh, lister.lastRefreshInput)
	}
	// Note: the exact-match check above also catches an empty
	// lastRefreshInput (specificRefresh is non-empty, so any
	// "" against it triggers the t.Errorf). No separate empty
	// guard required — keeps the assertion set minimal.
	if lister.gotToken != echoedAccessToken {
		t.Errorf("lister.ListFolder access_token: want %q (the AccessToken from RefreshOAuthToken's TokenData), got %q",
			echoedAccessToken, lister.gotToken)
	}
	if lister.gotFolderID != "e2e-folder" {
		t.Errorf("lister.ListFolder folderID: want e2e-folder, got %q", lister.gotFolderID)
	}
	if lister.listCallCount != 1 {
		t.Errorf("lister.ListFolder calls: want 1, got %d", lister.listCallCount)
	}
	if len(store.jobs) != len(files) {
		t.Errorf("upload_jobs queued: want %d, got %d", len(files), len(store.jobs))
	}

	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ScheduledCount != len(files) {
		t.Errorf("response ScheduledCount: want %d, got %d", len(files), resp.ScheduledCount)
	}
}
