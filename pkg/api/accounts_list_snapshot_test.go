package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestHandleListAccounts_BatchSnapshotEnrichment proves the aggregated
// N+1 fix: GET /api/v1/accounts enriches avatar_url and stamps
// snapshot_stale with ONE batched snapshot read (listFn invoked exactly
// once), never one snapshot read per account.
func TestHandleListAccounts_BatchSnapshotEnrichment(t *testing.T) {
	accounts := []*models.PlatformAccount{
		{ID: 1, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-fresh", Username: "fresh", Status: models.AccountStatusActive},
		{ID: 2, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-stale", Username: "stale", Status: models.AccountStatusActive},
		{ID: 3, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-nosnap", Username: "nosnap", Status: models.AccountStatusActive},
		{ID: 4, UserID: 7, Platform: models.PlatformInstagram, PlatformUserID: "ig-1", Username: "ig", Status: models.AccountStatusActive},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return accounts, nil
		},
	}

	batchCalls := 0
	now := time.Now()
	snapStore := &mockSnapshotStore{
		listFn: func(ids []int64) (map[int64]*repository.AccountResourceSnapshot, error) {
			batchCalls++
			if len(ids) != len(accounts) {
				t.Fatalf("batch ids: got %d, want %d (%v)", len(ids), len(accounts), ids)
			}
			return map[int64]*repository.AccountResourceSnapshot{
				1: {PlatformAccountID: 1, FetchedAt: now, Profile: map[string]any{"avatar_url": "https://avatars/fresh"}},
				2: {PlatformAccountID: 2, FetchedAt: now.Add(-30 * time.Minute), Profile: map[string]any{"avatar_url": "https://avatars/stale"}},
			}, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
	w := httptest.NewRecorder()

	r.handleListAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if batchCalls != 1 {
		t.Fatalf("snapshot batch read: got %d calls, want exactly 1 (fan-out eliminated)", batchCalls)
	}

	var response struct {
		Accounts []struct {
			ID            int64  `json:"id"`
			AvatarURL     string `json:"avatar_url"`
			SnapshotStale bool   `json:"snapshot_stale"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Accounts) != len(accounts) {
		t.Fatalf("accounts: got %d, want %d", len(response.Accounts), len(accounts))
	}

	byID := map[int64]struct {
		avatar string
		stale  bool
	}{}
	for _, a := range response.Accounts {
		byID[a.ID] = struct {
			avatar string
			stale  bool
		}{a.AvatarURL, a.SnapshotStale}
	}

	if got := byID[1]; got.avatar != "https://avatars/fresh" || got.stale {
		t.Errorf("account 1 (fresh snapshot): got %+v, want avatar=%q stale=false", got, "https://avatars/fresh")
	}
	if got := byID[2]; got.avatar != "https://avatars/stale" || !got.stale {
		t.Errorf("account 2 (stale snapshot): got %+v, want avatar=%q stale=true", got, "https://avatars/stale")
	}
	// No snapshot row → stale, and no avatar to fall back on.
	if got := byID[3]; got.avatar != "" || !got.stale {
		t.Errorf("account 3 (no snapshot): got %+v, want avatar=\"\" stale=true", got)
	}
	// Non-YouTube account without snapshot row: no snapshot enrichment.
	if got := byID[4]; got.avatar != "" || !got.stale {
		t.Errorf("account 4 (no snapshot): got %+v, want avatar=\"\" stale=true", got)
	}
}

// TestHandleListAccounts_NilSnapshotStoreSkipsBatch proves the shortcut:
// without a wired snapshot store the list stays a single query and does
// not try the batch read at all.
func TestHandleListAccounts_NilSnapshotStoreSkipsBatch(t *testing.T) {
	accounts := []*models.PlatformAccount{
		{ID: 1, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-1", Username: "one", Status: models.AccountStatusActive},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return accounts, nil
		},
	}
	// No WithSnapshotStore → r.snapshotStore is nil.
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
	w := httptest.NewRecorder()

	r.handleListAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}
