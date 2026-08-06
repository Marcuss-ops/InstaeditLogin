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

// TestHandleListAccounts_JoinedSnapshotEnrichment proves the aggregated
// N+1 fix: GET /api/v1/accounts receives accounts already joined with
// their snapshots (ONE SQL query via the LEFT JOIN) and stamps
// avatar_url + snapshot_stale per account — no per-account reads, no
// Vault access, no provider (YouTube) calls on page load.
func TestHandleListAccounts_JoinedSnapshotEnrichment(t *testing.T) {
	now := time.Now()
	rows := []*repository.AccountWithSnapshot{
		{
			Account: &models.PlatformAccount{ID: 1, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-fresh", Username: "fresh", Status: models.AccountStatusActive},
			Snapshot: &repository.AccountResourceSnapshot{
				PlatformAccountID: 1,
				FetchedAt:         now,
				Profile:           map[string]any{"avatar_url": "https://avatars/fresh"},
			},
		},
		{
			Account: &models.PlatformAccount{ID: 2, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-stale", Username: "stale", Status: models.AccountStatusActive},
			Snapshot: &repository.AccountResourceSnapshot{
				PlatformAccountID: 2,
				FetchedAt:         now.Add(-30 * time.Minute),
				Profile:           map[string]any{"avatar_url": "https://avatars/stale"},
			},
		},
		// No snapshot row → the LEFT JOIN yields nil Snapshot.
		{Account: &models.PlatformAccount{ID: 3, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-nosnap", Username: "nosnap", Status: models.AccountStatusActive}},
		{Account: &models.PlatformAccount{ID: 4, UserID: 7, Platform: models.PlatformInstagram, PlatformUserID: "ig-1", Username: "ig", Status: models.AccountStatusActive}},
	}
	store := &mockUserStore{
		listWithSnapshotsFn: func(userID int64, platform string) ([]*repository.AccountWithSnapshot, error) {
			if userID != 7 || platform != "" {
				t.Fatalf("unexpected list scope: user=%d platform=%q", userID, platform)
			}
			return rows, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
	w := httptest.NewRecorder()

	r.handleListAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
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
	if len(response.Accounts) != len(rows) {
		t.Fatalf("accounts: got %d, want %d", len(response.Accounts), len(rows))
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
	// No snapshot row → stale, no avatar to fall back on.
	if got := byID[3]; got.avatar != "" || !got.stale {
		t.Errorf("account 3 (no snapshot): got %+v, want avatar=\"\" stale=true", got)
	}
	if got := byID[4]; got.avatar != "" || !got.stale {
		t.Errorf("account 4 (no snapshot): got %+v, want avatar=\"\" stale=true", got)
	}
}

// TestHandleListAccounts_MetadataAvatarWinsOverSnapshot proves the
// fallback ordering: an avatar_url already in the account metadata is
// preserved; the snapshot profile is only used when metadata has none.
func TestHandleListAccounts_MetadataAvatarWinsOverSnapshot(t *testing.T) {
	rows := []*repository.AccountWithSnapshot{
		{
			Account: &models.PlatformAccount{
				ID: 1, UserID: 7, Platform: models.PlatformYouTube,
				PlatformUserID: "UC-meta", Username: "meta", Status: models.AccountStatusActive,
				Metadata: models.Metadata{"avatar_url": "https://avatars/from-metadata"},
			},
			Snapshot: &repository.AccountResourceSnapshot{
				PlatformAccountID: 1,
				FetchedAt:         time.Now(),
				Profile:           map[string]any{"avatar_url": "https://avatars/from-snapshot"},
			},
		},
	}
	store := &mockUserStore{
		listWithSnapshotsFn: func(userID int64, platform string) ([]*repository.AccountWithSnapshot, error) {
			return rows, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
	w := httptest.NewRecorder()

	r.handleListAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Accounts []struct {
			ID        int64  `json:"id"`
			AvatarURL string `json:"avatar_url"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Accounts) != 1 || response.Accounts[0].AvatarURL != "https://avatars/from-metadata" {
		t.Fatalf("metadata avatar must win: got %+v", response.Accounts)
	}
}
