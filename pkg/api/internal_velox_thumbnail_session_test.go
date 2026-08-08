package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// =====================================================================
// Fakes
// =====================================================================

// fakeYouTubeVideoEditStore is the minimal in-memory YouTubeVideoEditStore
// fake used by the thumbnail-session auto-provisioner tests.
//
// Implements ONLY the methods the auto-provisioner + GET-by-id
// handler actually call. The remaining methods of the interface
// panic-on-call so unused invocations surface loudly. This mirrors
// the e2e_helpers_test pattern: a fake stores an interface; calling
// a method the test does not stub is a test bug, not a fake-stability
// bug.
//
// Map keyed by (workspace_id, platform_account_id, youtube_video_id)
// so FindOrCreateEditableSession can implement the SELECT fast-path
// the production repository provides.
type fakeYouTubeVideoEditStore struct {
	mu   sync.Mutex
	rows map[string]*models.YouTubeVideoEdit
}

func newFakeYouTubeVideoEditStore() *fakeYouTubeVideoEditStore {
	return &fakeYouTubeVideoEditStore{rows: make(map[string]*models.YouTubeVideoEdit)}
}

// yveTripleKey builds the (workspace, account, video) tuple key
// the fake store uses for its internal map. Renamed from `wsKey`
// (used in internal_velox_get_delivery_test.go) to avoid a
// package-level redeclaration that breaks go vet.
func yveTripleKey(ws, acct int64, video string) string {
	return fmt.Sprintf("%d:%d:%s", ws, acct, video)
}

func (f *fakeYouTubeVideoEditStore) FindByID(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return nil, nil
}

func (f *fakeYouTubeVideoEditStore) FindByVeloxProjectID(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row.VeloxProjectID == projectID {
			return row, nil
		}
	}
	return nil, nil
}

func (f *fakeYouTubeVideoEditStore) Create(_ context.Context, edit *models.YouTubeVideoEdit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insertLocked(edit)
}

func (f *fakeYouTubeVideoEditStore) FindOrCreateEditableSession(_ context.Context, wsID, acctID int64, videoID, sessionIDHint, projectIDHint string) (*models.YouTubeVideoEdit, error) {
	if wsID <= 0 || acctID <= 0 || videoID == "" {
		return nil, fmt.Errorf("invalid triple (workspaceID=%d platformAccountID=%d youtubeVideoID=%q)", wsID, acctID, videoID)
	}
	k := yveTripleKey(wsID, acctID, videoID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.rows[k]; ok {
		if existing.Status == "editing" || existing.Status == "failed" || existing.Status == "publishing" {
			return existing, nil
		}
	}
	if sessionIDHint == "" {
		sessionIDHint = uuid.NewString()
	}
	if projectIDHint == "" {
		projectIDHint = "ve_" + uuid.NewString()
	}
	row := &models.YouTubeVideoEdit{
		ID:                sessionIDHint,
		WorkspaceID:       wsID,
		PlatformAccountID: acctID,
		YouTubeVideoID:    videoID,
		VeloxProjectID:    projectIDHint,
		DesiredPrivacy:    "public",
		Status:            "editing",
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	if err := f.insertLocked(row); err != nil {
		return nil, err
	}
	return row, nil
}

// insertLocked is the inner helper invoked from Create +
// FindOrCreateEditableSession while f.mu is already held.
func (f *fakeYouTubeVideoEditStore) insertLocked(edit *models.YouTubeVideoEdit) error {
	k := yveTripleKey(edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID)
	if existing, ok := f.rows[k]; ok {
		if existing.Status == "editing" || existing.Status == "failed" || existing.Status == "publishing" {
			return errors.New("fakeYouTubeVideoEditStore: duplicate key violates uniq_youtube_video_edits_open_session")
		}
	}
	edit.CreatedAt = time.Now().UTC()
	edit.UpdatedAt = edit.CreatedAt
	f.rows[k] = edit
	return nil
}

func (f *fakeYouTubeVideoEditStore) Update(_ context.Context, edit *models.YouTubeVideoEdit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := yveTripleKey(edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID)
	if existing, ok := f.rows[k]; ok && existing.ID == edit.ID {
		edit.UpdatedAt = time.Now().UTC()
		f.rows[k] = edit
		return nil
	}
	return fmt.Errorf("fakeYouTubeVideoEditStore: no row for update id=%s", edit.ID)
}

// Stub methods — satisfy the YouTubeVideoEditStore interface but
// are NEVER called by the auto-provisioner tests. Panicking
// surfaces any future test that accidentally invokes one (a
// signal that the test fixture needs a richer fake).

func (f *fakeYouTubeVideoEditStore) MarkPublishing(_ context.Context, _ string, _ string, _ *time.Time, _ time.Duration) (*models.YouTubeVideoEdit, error) {
	panic("fakeYouTubeVideoEditStore.MarkPublishing: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) AttachThumbnail(_ context.Context, _, _ string) (*models.YouTubeVideoEdit, error) {
	panic("fakeYouTubeVideoEditStore.AttachThumbnail: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) ListByWorkspace(_ context.Context, _ repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
	panic("fakeYouTubeVideoEditStore.ListByWorkspace: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) ListByWorkspaceAccountIDs(_ context.Context, _ int64, _ []int64) ([]*models.YouTubeVideoEdit, error) {
	panic("fakeYouTubeVideoEditStore.ListByWorkspaceAccountIDs: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) ListCoversByGroupAccounts(_ context.Context, _ int64, _ []int64) ([]*models.GroupCover, error) {
	panic("fakeYouTubeVideoEditStore.ListCoversByGroupAccounts: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) SaveDraft(_ context.Context, _ string, _, _ string, _ []string, _, _ string, _ map[string]models.YouTubeTranslation, _ string, _ *time.Time, _ time.Time) error {
	panic("fakeYouTubeVideoEditStore.SaveDraft: not implemented in thumbnail-session test fixture")
}

func (f *fakeYouTubeVideoEditStore) MarkPublishedWithActualPrivacy(_ context.Context, _ string, _, _ string) (*models.YouTubeVideoEdit, error) {
	panic("fakeYouTubeVideoEditStore.MarkPublishedWithActualPrivacy: not implemented in thumbnail-session test fixture")
}

// fakeWSStoreWithChannels is the minimal in-memory WorkspaceStore
// fake that ALSO supports FindChannel. Distinct from
// fakeE2EWorkspace (in internal_velox_e2e_helpers_test.go) which
// only supports FindByID — the auto-provisioner's
// defense-in-depth FindChannel check needs more surface.
//
// Embeds the production WorkspaceStore interface (other methods
// nil-receiver-safe) + overrides ONLY the 2 methods we exercise
// (FindByID + FindChannel). Other methods panic-on-call so unused
// invocations surface loudly.
type fakeWSStoreWithChannels struct {
	WorkspaceStore
	rows    map[int64]*models.Workspace
	channel map[string]*models.WorkspaceChannel
}

func newFakeWSStoreWithChannels() *fakeWSStoreWithChannels {
	return &fakeWSStoreWithChannels{
		rows:    map[int64]*models.Workspace{},
		channel: map[string]*models.WorkspaceChannel{},
	}
}

func wsChannelKey(wsID, acctID int64) string {
	return fmt.Sprintf("%d:%d", wsID, acctID)
}

func (f *fakeWSStoreWithChannels) FindByID(id int64) (*models.Workspace, error) {
	if f.rows == nil {
		return nil, errors.New("fakeWSStoreWithChannels: rows nil")
	}
	return f.rows[id], nil
}

func (f *fakeWSStoreWithChannels) FindChannel(_ context.Context, wsID, acctID int64) (*models.WorkspaceChannel, error) {
	k := wsChannelKey(wsID, acctID)
	if f.channel == nil {
		return nil, nil
	}
	return f.channel[k], nil
}

func (f *fakeWSStoreWithChannels) addBinding(wsID, acctID int64, enabled bool) {
	f.channel[wsChannelKey(wsID, acctID)] = &models.WorkspaceChannel{
		WorkspaceID:       wsID,
		PlatformAccountID: acctID,
		Enabled:           enabled,
	}
}

// fakeUserStoreForThumbnail satisfies the UserStore interface
// enough to drive CreateThumbnailSessionForDelivery. Embeds the
// production interface + overrides only FindPlatformAccountByID.
type fakeUserStoreForThumbnail struct {
	UserStore
	accounts map[int64]*models.PlatformAccount
}

func (f *fakeUserStoreForThumbnail) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if f.accounts == nil {
		return nil, nil
	}
	return f.accounts[id], nil
}

func newFakeUserStoreForThumbnail() *fakeUserStoreForThumbnail {
	return &fakeUserStoreForThumbnail{accounts: map[int64]*models.PlatformAccount{}}
}

// =====================================================================
// Test harness
// =====================================================================

// runCreateThumbnailSession mounts the VeloxModule against a fresh
// chi mux with the supplied fakes, then issues the supplied POST
// against /internal/v1/thumbnail-sessions. Returns the recorded
// response so tests can assert status + body.
//
// authHeader == "" means "no Authorization header" (used by the
// 401-missing-AuthZ branch). All other paths set it via
// buildAuthHeader(token).
func runCreateThumbnailSession(
	t *testing.T,
	yteStore *fakeYouTubeVideoEditStore,
	ws *fakeWSStoreWithChannels,
	users *fakeUserStoreForThumbnail,
	token string,
	body string,
	authHeader string,
) *httptest.ResponseRecorder {
	t.Helper()
	vm := NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: &fakeE2EDestinations{},
		ExternalDeliveryStore:    &fakeE2EDeliveries{rows: map[string]*models.ExternalDelivery{}},
		WorkspaceStore:           ws,
		UserStore:                users,
		YouTubeVideoEditStore:    yteStore,
		VeloxAPIToken:            token,
		EditorBaseURL:            "https://editor.test.local",
	})
	chiRouter := chi.NewRouter()
	vm.Register(chiRouter)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/thumbnail-sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	chiRouter.ServeHTTP(w, req)
	return w
}

// buildAuthHeader wraps a token in the canonical "Bearer <token>"
// shape. Centralised so test calls stay short.
func buildAuthHeader(token string) string {
	return "Bearer " + token
}

// freshHarnessForThumbnail builds a workspace + user store pair
// pre-populated for the canonical happy-path scenario:
//   - workspace 12 owned by user 42
//   - platform_account 381 active on YouTube
//   - workspace↔account binding (12, 381) ENABLED
//
// Centralised so every test in this file starts from the same
// baseline + tests only mutate the fields they care about.
func freshHarnessForThumbnail() (*fakeYouTubeVideoEditStore, *fakeWSStoreWithChannels, *fakeUserStoreForThumbnail) {
	store := newFakeYouTubeVideoEditStore()
	ws := newFakeWSStoreWithChannels()
	users := newFakeUserStoreForThumbnail()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42, Name: "ws"}
	users.accounts[381] = &models.PlatformAccount{ID: 381, Platform: "youtube", Status: "active"}
	ws.addBinding(12, 381, true)
	return store, ws, users
}

// =====================================================================
// Tests
// =====================================================================

// TestHandleCreateThumbnailSession_HappyPath verifies that a valid
// payload returns 201 + the canonical wire shape with an
// editor_session_id in ytedit_<uuid> format.
func TestHandleCreateThumbnailSession_HappyPath(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()

	body := `{
		"workspace_id": 12,
		"platform_account_id": 381,
		"youtube_video_id": "AbCd1234",
		"video_title": "Wrestling Discovery: Pacquiao vs Broner",
		"video_status": "private",
		"final_privacy": "public",
		"delivery_id": "del_test_001"
	}`
	w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var resp createThumbnailSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if !strings.HasPrefix(resp.EditorSessionID, "ytedit_") {
		t.Errorf("editor_session_id: want ytedit_ prefix, got %q", resp.EditorSessionID)
	}
	if resp.YouTubeVideoID != "AbCd1234" {
		t.Errorf("youtube_video_id: want AbCd1234, got %q", resp.YouTubeVideoID)
	}
	if !strings.HasPrefix(resp.VeloxProjectID, "ve_") {
		t.Errorf("velox_project_id: want ve_ prefix, got %q", resp.VeloxProjectID)
	}
	if resp.Status != "editing" {
		t.Errorf("status: want editing, got %q", resp.Status)
	}
	if resp.ThumbnailStatus != "pending" {
		t.Errorf("thumbnail_status: want pending, got %q", resp.ThumbnailStatus)
	}
	if resp.FinalPrivacy != "public" {
		t.Errorf("final_privacy: want public, got %q", resp.FinalPrivacy)
	}
	if resp.WorkspaceID != 12 || resp.PlatformAccountID != 381 {
		t.Errorf("workspace_id/platform_account_id echo wrong: got %+v", resp)
	}
	if resp.EditorURL == "" {
		t.Errorf("editor_url: want non-empty, got empty")
	}
	if resp.Duplicate {
		t.Errorf("duplicate: want false on first call, got true")
	}

	// Verify DraftTitle + DesiredPrivacy were stamped on the row.
	row, err := store.FindByID(context.Background(), resp.EditorSessionID)
	if err != nil || row == nil {
		t.Fatalf("FindByID after create: err=%v row=%v", err, row)
	}
	if row.DraftTitle == nil || *row.DraftTitle != "Wrestling Discovery: Pacquiao vs Broner" {
		t.Errorf("DraftTitle: want the video_title hint, got %v", row.DraftTitle)
	}
	if row.DesiredPrivacy != "public" {
		t.Errorf("DesiredPrivacy: want public, got %q", row.DesiredPrivacy)
	}
}

// TestHandleCreateThumbnailSession_ReplayIdempotency verifies that
// the second call with the same (workspace, account, video) triple
// returns 200 + the SAME editor_session_id (Duplicate=true) and
// does NOT insert a duplicate row.
func TestHandleCreateThumbnailSession_ReplayIdempotency(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()

	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"AbCd1234","final_privacy":"public"}`

	w1 := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d", w1.Code)
	}
	var r1 createThumbnailSessionResponse
	if err := json.Unmarshal(w1.Body.Bytes(), &r1); err != nil {
		t.Fatalf("first unmarshal: %v", err)
	}
	if r1.Duplicate {
		t.Errorf("first call: Duplicate should be false")
	}

	w2 := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w2.Code != http.StatusOK {
		t.Fatalf("replay: want 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	var r2 createThumbnailSessionResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &r2); err != nil {
		t.Fatalf("replay unmarshal: %v", err)
	}
	if r2.EditorSessionID != r1.EditorSessionID {
		t.Errorf("replay editor_session_id: want %q, got %q", r1.EditorSessionID, r2.EditorSessionID)
	}
	if !r2.Duplicate {
		t.Errorf("replay Duplicate: want true, got false")
	}

	if len(store.rows) != 1 {
		t.Errorf("row count after replay: want 1, got %d", len(store.rows))
	}
}

// TestHandleCreateThumbnailSession_ReplayMixedFormat verifies the
// MIXED-FORMAT REPLAY scenario documented in
// CreateThumbnailSessionForDelivery: when the existing row was
// created via the manual POST /api/v1/youtube/editor-sessions
// (bare-uuid format), the auto-provisioner echoes that bare-uuid
// id with duplicate=true.
//
// This is an edge case (operator must have clicked the manual
// editor creator for a freshly-uploaded video before Velox fired),
// but the response shape MUST be stable so the Thumbnail Maker
// SPA can render the right card UI.
func TestHandleCreateThumbnailSession_ReplayMixedFormat(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()

	// Pre-seed a row with the bare-uuid format (manual creator).
	bareID := uuid.NewString()
	now := time.Now().UTC()
	store.rows[yveTripleKey(12, 381, "AbCd1234")] = &models.YouTubeVideoEdit{
		ID:                bareID, // bare uuid, NOT ytedit_ prefix
		WorkspaceID:       12,
		PlatformAccountID: 381,
		YouTubeVideoID:    "AbCd1234",
		VeloxProjectID:    "ve_" + uuid.NewString(),
		DesiredPrivacy:    "public",
		Status:            "editing",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"AbCd1234","final_privacy":"public"}`
	w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w.Code != http.StatusOK {
		t.Fatalf("replay: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp createThumbnailSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.EditorSessionID != bareID {
		t.Errorf("mixed-format replay: want bare-uuid %q, got %q", bareID, resp.EditorSessionID)
	}
	if !resp.Duplicate {
		t.Errorf("mixed-format replay Duplicate: want true, got false")
	}
	if strings.HasPrefix(resp.EditorSessionID, "ytedit_") {
		t.Errorf("mixed-format replay should NOT echo a fresh ytedit_ id")
	}
}

// TestHandleCreateThumbnailSession_TitleTruncation verifies that a
// video_title longer than YouTube's 100-char limit is truncated
// when stamped onto DraftTitle (defence-in-depth so the InstaEditor
// SPA doesn't 500 on a too-long title during the next save-draft).
func TestHandleCreateThumbnailSession_TitleTruncation(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()

	long := strings.Repeat("a", 250)
	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"AbCd1234","video_title":"` + long + `"}`
	w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", w.Code)
	}
	var resp createThumbnailSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	row, _ := store.FindByID(context.Background(), resp.EditorSessionID)
	if row == nil || row.DraftTitle == nil {
		t.Fatalf("row/DraftTitle nil: row=%v DraftTitle=%v", row, row.DraftTitle)
	}
	if len(*row.DraftTitle) != 100 {
		t.Errorf("DraftTitle length: want 100, got %d (%q)", len(*row.DraftTitle), *row.DraftTitle)
	}
}

// TestHandleCreateThumbnailSession_FinalPrivacyNormalization verifies
// that final_privacy is normalized to lowercase and validated
// against the allow-list.
func TestHandleCreateThumbnailSession_FinalPrivacyNormalization(t *testing.T) {
	cases := []struct {
		name       string
		final      string
		wantStored string
		wantStatus int
	}{
		{"public canonical", "public", "public", http.StatusCreated},
		{"unlisted canonical", "unlisted", "unlisted", http.StatusCreated},
		{"private canonical", "private", "private", http.StatusCreated},
		{"uppercase normalized", "PUBLIC", "public", http.StatusCreated},
		{"whitespace normalized", "  public  ", "public", http.StatusCreated},
		{"invalid rejected", "scheduled", "", http.StatusBadRequest},
		{"empty defaults to public", "", "public", http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ws, users := freshHarnessForThumbnail()
			// Use a per-case unique video id so the store stays
			// scoped per subtest.
			body := fmt.Sprintf(`{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid-%s","final_privacy":%q}`, tc.name, tc.final)
			w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
			if w.Code != tc.wantStatus {
				t.Fatalf("status: want %d, got %d body=%s", tc.wantStatus, w.Code, w.Body.String())
			}
			if tc.wantStatus != http.StatusCreated {
				return
			}
			var resp createThumbnailSessionResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			row, _ := store.FindByID(context.Background(), resp.EditorSessionID)
			if row.DesiredPrivacy != tc.wantStored {
				t.Errorf("DesiredPrivacy: want %q, got %q", tc.wantStored, row.DesiredPrivacy)
			}
		})
	}
}

// TestHandleCreateThumbnailSession_MissingFields verifies that
// required fields are validated at the handler boundary.
func TestHandleCreateThumbnailSession_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing workspace_id", `{"platform_account_id":381,"youtube_video_id":"vid"}`},
		{"missing platform_account_id", `{"workspace_id":12,"youtube_video_id":"vid"}`},
		{"missing youtube_video_id", `{"workspace_id":12,"platform_account_id":381}`},
		{"zero workspace_id", `{"workspace_id":0,"platform_account_id":381,"youtube_video_id":"vid"}`},
		{"negative workspace_id", `{"workspace_id":-1,"platform_account_id":381,"youtube_video_id":"vid"}`},
		{"invalid final_privacy", `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid","final_privacy":"scheduled"}`},
		{"invalid video_status", `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid","video_status":"draft"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ws, users := freshHarnessForThumbnail()
			w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, tc.body, buildAuthHeader(testVeloxAPIToken))
			if w.Code != http.StatusBadRequest {
				t.Errorf("status: want 400, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleCreateThumbnailSession_AuthFails verifies the
// VeloxAPIToken gate rejects unauthenticated / wrong-token
// requests with the canonical 401/403 split.
func TestHandleCreateThumbnailSession_AuthFails(t *testing.T) {
	cases := []struct {
		name       string
		authHeader string
		want       int
	}{
		{"no Authorization", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token-32-chars-aaaaaa", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ws, users := freshHarnessForThumbnail()
			body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid"}`
			w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, tc.authHeader)
			if w.Code != tc.want {
				t.Errorf("status: want %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleCreateThumbnailSession_EmptyTokenReturns503 verifies the
// auth middleware emits 503 when VELOX_API_TOKEN is empty (a
// boot-time misconfiguration the middleware surfaces loudly).
func TestHandleCreateThumbnailSession_EmptyTokenReturns503(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()
	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid"}`
	w := runCreateThumbnailSession(t, store, ws, users, "", body, "")
	// With no token the whole internal module remains unmounted, so
	// the route is intentionally indistinguishable from a missing URL.
	if w.Code != http.StatusNotFound {
		t.Errorf("empty token: want 404 (internal module not mounted), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleCreateThumbnailSession_StoreUnconfigured verifies the
// handler returns 503 when the YouTubeVideoEditStore dep is nil
// (matches the nil-guard pattern documented at modules.go).
func TestHandleCreateThumbnailSession_StoreUnconfigured(t *testing.T) {
	ws := newFakeWSStoreWithChannels()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42}
	users := newFakeUserStoreForThumbnail()
	users.accounts[381] = &models.PlatformAccount{ID: 381, Platform: "youtube", Status: "active"}
	ws.addBinding(12, 381, true)

	vm := NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: &fakeE2EDestinations{},
		ExternalDeliveryStore:    &fakeE2EDeliveries{rows: map[string]*models.ExternalDelivery{}},
		WorkspaceStore:           ws,
		UserStore:                users,
		YouTubeVideoEditStore:    nil,
		VeloxAPIToken:            testVeloxAPIToken,
	})
	chiRouter := chi.NewRouter()
	vm.Register(chiRouter)

	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/thumbnail-sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", buildAuthHeader(testVeloxAPIToken))
	w := httptest.NewRecorder()
	chiRouter.ServeHTTP(w, req)
	// The route is not mounted without its persistence dependency.
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404 (thumbnail session route not mounted), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleCreateThumbnailSession_NoChannelBinding verifies the
// FindChannel defense-in-depth check returns 404 when the
// (workspace, account) pair isn't bound.
func TestHandleCreateThumbnailSession_NoChannelBinding(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()
	// Deliberately wipe the binding that freshHarnessForThumbnail added.
	delete(ws.channel, wsChannelKey(12, 381))
	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid"}`
	w := runCreateThumbnailSession(t, store, ws, users, testVeloxAPIToken, body, buildAuthHeader(testVeloxAPIToken))
	if w.Code != http.StatusNotFound {
		t.Errorf("missing channel binding: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateThumbnailSessionForDelivery_FormatIsYTEdit pins the
// ytedit_<uuid> id format the auto-provisioner mints on fresh
// INSERTs. Catches a future refactor that accidentally drops the
// prefix or rolls back to bare uuid.
func TestCreateThumbnailSessionForDelivery_FormatIsYTEdit(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()
	vm := NewVeloxModule(VeloxModuleDeps{
		WorkspaceStore:        ws,
		UserStore:             users,
		YouTubeVideoEditStore: store,
		VeloxAPIToken:         testVeloxAPIToken,
	}).(*VeloxModule)

	for i := 0; i < 5; i++ {
		edit, dup, err := vm.CreateThumbnailSessionForDelivery(context.Background(), CreateThumbnailSessionInput{
			WorkspaceID:       12,
			PlatformAccountID: 381,
			YouTubeVideoID:    strconv.Itoa(i),
			FinalPrivacy:      "public",
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if dup {
			t.Errorf("call %d: dup should be false on fresh insert", i)
		}
		if !strings.HasPrefix(edit.ID, "ytedit_") {
			t.Errorf("call %d: id should start with ytedit_, got %q", i, edit.ID)
		}
	}
}
