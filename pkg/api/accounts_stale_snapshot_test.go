package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestHandleGetAccount_StaleSnapshot_ServesCachedWithoutProviderCall pins
// the STRICT RULE: opening a channel page must never call the provider
// (YouTube). Even when the platform implements AccountDetailsProvider and
// the snapshot is stale, GET /accounts/{id} serves the cached resource
// immediately and records refresh_pending for the background worker.
func TestHandleGetAccount_StaleSnapshot_ServesCachedWithoutProviderCall(t *testing.T) {
	now := time.Now()
	providerCalls := 0
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		detailsFn: func(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
			providerCalls++
			t.Errorf("provider.GetAccountDetails MUST NOT be called on page load (strict rule); token=%q", accessToken)
			return nil, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	staleSnap := &repository.AccountResourceSnapshot{
		PlatformAccountID: 21,
		ResourceType:      "channel",
		FetchedAt:         now.Add(-30 * time.Minute),
		Profile:           map[string]any{"display_name": "Stale Cached Channel"},
	}
	pendingMarks := 0
	snapStore := &mockSnapshotStore{
		staleFn: func(id int64, maxAge time.Duration) (bool, error) { return true, nil },
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return staleSnap, nil
		},
		markPendingFn: func(id int64, when time.Time) error {
			pendingMarks++
			if id != 21 {
				t.Errorf("markPending called with id=%d, want 21", id)
			}
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if providerCalls != 0 {
		t.Fatalf("provider called %d time(s); page load must never call YouTube", providerCalls)
	}
	if pendingMarks != 1 {
		t.Fatalf("refresh_pending marked %d time(s), want 1 (worker must know a refresh is due)", pendingMarks)
	}

	var resp struct {
		ID            int64 `json:"id"`
		SnapshotStale bool  `json:"snapshot_stale"`
		Resource      *struct {
			DisplayName string `json:"display_name"`
		} `json:"resource"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SnapshotStale != true {
		t.Errorf("snapshot_stale: want true (cached fallback), got %v", resp.SnapshotStale)
	}
	if resp.Resource == nil || resp.Resource.DisplayName != "Stale Cached Channel" {
		t.Errorf("resource must come from the cached stale snapshot, got %+v", resp.Resource)
	}
}

// TestHandleGetAccount_FreshSnapshot_NoPendingMark proves a fresh snapshot
// is served straight from cache WITHOUT stamping refresh_pending.
func TestHandleGetAccount_FreshSnapshot_NoPendingMark(t *testing.T) {
	now := time.Now()
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	freshSnap := &repository.AccountResourceSnapshot{
		PlatformAccountID: 21,
		ResourceType:      "channel",
		FetchedAt:         now,
		Profile:           map[string]any{"display_name": "Fresh Channel"},
	}
	snapStore := &mockSnapshotStore{
		staleFn: func(id int64, maxAge time.Duration) (bool, error) { return false, nil },
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return freshSnap, nil
		},
		markPendingFn: func(id int64, when time.Time) error {
			t.Errorf("refresh_pending MUST NOT be marked for a fresh snapshot (account %d)", id)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		SnapshotStale bool `json:"snapshot_stale"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SnapshotStale {
		t.Errorf("snapshot_stale: want false (fresh snapshot), got true")
	}
}
