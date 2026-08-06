package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandleSyncAllAccounts_EnqueuesAllOwnedAccountsAndReturns202 pins
// the "refresh all channels" contract: the handler stamps the refresh
// queue for every account owned by the caller in one bulk call and
// answers 202 with the enqueued count — it must NEVER iterate accounts
// and issue per-account provider (YouTube) requests.
func TestHandleSyncAllAccounts_EnqueuesAllOwnedAccountsAndReturns202(t *testing.T) {
	var markedUserID int64
	snapStore := &mockSnapshotStore{
		markAllPendingFn: func(userID int64, now time.Time) (int64, error) {
			markedUserID = userID
			return 46, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: "youtube"}, &mockUserStore{}, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/sync-all", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	if markedUserID != 1 {
		t.Errorf("bulk mark called with userID=%d, want 1", markedUserID)
	}
	var resp struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "scheduled" || resp.Count != 46 {
		t.Errorf("resp: got status=%q count=%d, want status=scheduled count=46", resp.Status, resp.Count)
	}
}

func TestHandleSyncAllAccounts_StoreError_500(t *testing.T) {
	snapStore := &mockSnapshotStore{
		markAllPendingFn: func(userID int64, now time.Time) (int64, error) {
			return 0, errors.New("connection lost")
		},
	}
	r := newTestRouter(&mockProvider{platform: "youtube"}, &mockUserStore{}, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/sync-all", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSyncAllAccounts_NoStore_501(t *testing.T) {
	r := newTestRouter(&mockProvider{platform: "youtube"}, &mockUserStore{}, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/sync-all", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d: %s", w.Code, w.Body.String())
	}
}
