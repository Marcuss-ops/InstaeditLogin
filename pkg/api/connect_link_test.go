package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// fakeConnectLinkNonceStore is an in-memory replay-protecting
// ConnectLinkNonceStore for handler-level tests. It records every
// created nonce and rejects any attempt to consume a nonce that is
// unknown, already consumed, or expired.
type fakeConnectLinkNonceStore struct {
	created  map[string]fakeConnectLinkNonceRecord
	consumed map[string]bool
}

type fakeConnectLinkNonceRecord struct {
	expectedChannelID string
	expiresAt         time.Time
}

func newFakeConnectLinkNonceStore() *fakeConnectLinkNonceStore {
	return &fakeConnectLinkNonceStore{
		created:  make(map[string]fakeConnectLinkNonceRecord),
		consumed: make(map[string]bool),
	}
}

func (f *fakeConnectLinkNonceStore) Create(nonce, expectedChannelID string, expiresAt time.Time) error {
	f.created[nonce] = fakeConnectLinkNonceRecord{
		expectedChannelID: expectedChannelID,
		expiresAt:         expiresAt,
	}
	return nil
}

func (f *fakeConnectLinkNonceStore) Consume(nonce string) error {
	rec, ok := f.created[nonce]
	if !ok {
		return repository.ErrNonceMissing
	}
	if time.Now().After(rec.expiresAt) {
		return repository.ErrNonceExpired
	}
	if f.consumed[nonce] {
		return repository.ErrNonceConsumed
	}
	f.consumed[nonce] = true
	return nil
}

// issueTestAdminJWT mints a JWT with the admin claim set. The admin
// middleware on /admin/* requires an identity whose IsAdmin() is true.
func issueTestAdminJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.IssueAccessAdmin(userID, 1, 1, true)
	if err != nil {
		t.Fatalf("issue admin jwt (user=%d, ws=1, session=1): %v", userID, err)
	}
	return tok
}

// TestConnectLinkReplay_SecondCallbackReturns410 verifies the full
// connect-link replay-prevention flow end-to-end at the handler
// level:
//
//  1. An admin issues a connect-link for a pending YouTube channel.
//  2. The first OAuth callback with the signed state succeeds.
//  3. A second callback with the same state is rejected with 410.
func TestConnectLinkReplay_SecondCallbackReturns410(t *testing.T) {
	const channelID = 123
	const platformUserID = "UC012345678901234567890123"

	// Arrange: a YouTube provider that returns a deterministic
	// platform profile and token data on callback. The mock does NOT
	// implement AccountDiscoverer, so the callback falls through the
	// non-discoverer branch; the replay check runs before either
	// branch, so this is sufficient for the test.
	svc := &mockProvider{
		platform:       "youtube",
		handleCallback: successCallback,
	}

	// Arrange: a user store that returns a pending YouTube channel
	// with the manager_email_hint required by connect-link, then
	// attaches it on callback.
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != channelID {
				return nil, nil
			}
			return &models.PlatformAccount{
				ID:             channelID,
				UserID:         1,
				Platform:       models.PlatformYouTube,
				PlatformUserID: platformUserID,
				Status:         models.AccountStatusPendingAuthorization,
				Metadata: models.Metadata{
					"manager_email_hint": "manager@example.com",
				},
			}, nil
		},
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             channelID,
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Status:         models.AccountStatusActive,
			}, nil
		},
	}

	nonceStore := newFakeConnectLinkNonceStore()

	// The /admin/* routes are only mounted when adminStore is wired.
	// connect-link only performs a nil check on the store in the happy
	// path, so a no-op stub is sufficient.
	r := newTestRouter(svc, store, "", WithConnectLinkNonceStore(nonceStore), WithAdminStore(&stubAdminStore{}))

	// Step 1: admin issues a connect-link.
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/channels/%d/connect-link", channelID), nil)
	req.Header.Set("Authorization", "Bearer "+issueTestAdminJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("connect-link: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var linkResp AdminConnectLinkResponse
	if err := json.NewDecoder(w.Body).Decode(&linkResp); err != nil {
		t.Fatalf("decode connect-link response: %v", err)
	}
	if linkResp.ConnectURL == "" {
		t.Fatal("connect-link response missing connect_url")
	}

	// Extract the state query param from the connect URL.
	connectURL, err := url.Parse(linkResp.ConnectURL)
	if err != nil {
		t.Fatalf("parse connect_url: %v", err)
	}
	state := connectURL.Query().Get("state")
	if state == "" {
		t.Fatal("connect_url state param is empty")
	}

	// The nonce should have been persisted when the link was issued.
	if len(nonceStore.created) != 1 {
		t.Fatalf("nonce store: want 1 created nonce, got %d", len(nonceStore.created))
	}
	var nonce string
	for n := range nonceStore.created {
		nonce = n
	}
	if nonce == "" {
		t.Fatal("created nonce is empty")
	}

	// Step 2: first OAuth callback consumes the connect-link state.
	callbackReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/auth/youtube/callback?code=fake-code&state=%s", state), nil)
	callbackReq.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	callbackW := httptest.NewRecorder()
	r.Setup().ServeHTTP(callbackW, callbackReq)

	if callbackW.Code != http.StatusOK {
		t.Fatalf("first callback: want 200, got %d: %s", callbackW.Code, callbackW.Body.String())
	}
	if !nonceStore.consumed[nonce] {
		t.Fatal("nonce was not marked as consumed after first callback")
	}

	// Step 3: second OAuth callback with the same state returns 410.
	callbackReq2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/auth/youtube/callback?code=fake-code&state=%s", state), nil)
	callbackReq2.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	callbackW2 := httptest.NewRecorder()
	r.Setup().ServeHTTP(callbackW2, callbackReq2)

	if callbackW2.Code != http.StatusGone {
		t.Fatalf("second callback: want 410 Gone, got %d: %s", callbackW2.Code, callbackW2.Body.String())
	}
	if !strings.Contains(callbackW2.Body.String(), "already consumed") {
		t.Errorf("second callback body should mention consumption; got %q", callbackW2.Body.String())
	}
}
