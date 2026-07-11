package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockPostStore is defined in routes_test.go (canonical, shared across
// all test files in this package). This file only declares its own
// test helpers; it does NOT redeclare the struct.

// newPostsTestRouter builds a Router wired with the supplied postStore
// and (optionally) a custom workspace store. Use for /posts endpoint
// tests. Matches the variadic-options NewRouter signature in handlers.go
// (6 positional + options).
//
// wsStore is variadic for backward compat: existing callers that don't
// need a custom workspace store (i.e. the post is never read back, or
// the default FindByID returning a workspace owned by user 1 is fine)
// pass nothing. Tests that exercise cross-tenant behaviour (e.g.
// TestPostsAPI_Get_CrossOwner_404) pass a mockWorkspaceStore with a
// findByIDFn that returns a non-owner.
func newPostsTestRouter(
	postStore *mockPostStore,
	strictAuth bool,
	wsStore ...WorkspaceStore,
) *Router {
	// Only wsStore[0] is consulted. The variadic exists for BACKWARD
	// COMPAT with existing 2-arg callers (the workspace store is an
	// optional override). If a future test genuinely needs to wire two
	// workspace stores, change the signature to take an explicit
	// pointer-or-default helper (or a *_test.go-local router builder)
	// rather than extending this variadic.
	var ws WorkspaceStore = &mockWorkspaceStore{}
	if len(wsStore) > 0 && wsStore[0] != nil {
		ws = wsStore[0]
	}
	return NewRouter(
		map[string]services.PlatformService{},
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		strictAuth,
		"",
		nil,
		WithWorkspaceStore(ws),
		WithPostStore(postStore),
	)
}

// postsIssueJWT issues a JWT for the given user id using the test secret.
func postsIssueJWT(t *testing.T, userID int64) string {
	t.Helper()
	tok, _, _, err := auth.NewManager(testJWTSecret, 24).Issue(userID)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	return tok
}

func TestPostsAPI_Create_Happy_ReturnsPostPlusTargets(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, false)

	body := `{"workspace_id":1,"title":"hi","caption":"world","targets":[{"platform_account_id":10},{"platform_account_id":11}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Post    *models.Post         `json:"post"`
		Targets []*models.PostTarget `json:"targets"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Post == nil || resp.Post.ID != 100 {
		t.Errorf("post.ID: want 100, got %v", resp.Post)
	}
	if len(resp.Targets) != 2 || resp.Targets[0].PlatformAccountID != 10 {
		t.Errorf("targets mismatch: %+v", resp.Targets)
	}
}

func TestPostsAPI_Create_ErrPostUnauthorized_403(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		createFn: func(p *models.Post, t []*models.PostTarget) error {
			return repository.ErrPostUnauthorized
		},
	}, false)

	// Body must include at least one valid target so the handler's
	// no-targets 422 guard doesn't preempt the createFn. The test's
	// intent is to verify ErrPostUnauthorized → 403 mapping, not
	// target validation; the target here is a no-op data point.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1,"targets":[{"platform_account_id":10}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestPostsAPI_Create_BadStatus_400(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1,"status":"bogus"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestPostsAPI_Create_StrictAuth_NoJWT_401(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestPostsAPI_Get_Found_200(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: id, WorkspaceID: 1, Title: "x"}, nil
		},
	}, false)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/123", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostsAPI_Get_NotFound_404(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) { return nil, sql.ErrNoRows },
	}, false)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/999", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPostsAPI_ListByWorkspace_Happy(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		listByWorkspaceFn: func(wid int64) ([]models.Post, error) {
			return []models.Post{{ID: 1, WorkspaceID: wid}, {ID: 2, WorkspaceID: wid}}, nil
		},
	}, false)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/workspace/1", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Posts []models.Post `json:"posts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Posts) != 2 {
		t.Fatalf("want 2 posts, got %d", len(body.Posts))
	}
}

func TestPostsAPI_AddTarget_Happy(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, false)
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostsAPI_AddTarget_PostNotFound_404(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) { return nil, sql.ErrNoRows },
	}, false)
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/999/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPostsAPI_AddTarget_ErrPostUnauthorized_403(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		saveFn: func(t *models.PostTarget) error { return repository.ErrPostUnauthorized },
	}, false)
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestPostsAPI_Schedule_Happy(t *testing.T) {
	ts := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC)
	r := newPostsTestRouter(&mockPostStore{}, false)

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var p models.Post
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Status != models.PostStatusScheduled {
		t.Errorf("status: want scheduled, got %q", p.Status)
	}
}

func TestPostsAPI_Schedule_PostNotFound_404(t *testing.T) {
	ts := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC)
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) { return nil, sql.ErrNoRows },
	}, false)

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/999/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPostsAPI_Schedule_ErrPostUnauthorized_403(t *testing.T) {
	ts := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC)
	r := newPostsTestRouter(&mockPostStore{
		updateFn: func(p *models.Post) error { return repository.ErrPostUnauthorized },
	}, false)

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

// TestPostsAPI_Create_NoTargets_422 verifies the handleCreatePost
// validation guard "at least one target is required" → 422 (NOT 400,
// per the 422-vs-400 contract in HANDOFF-LINUX.md §13.1: the JSON parses
// fine, the field is just semantically missing). This test was lost
// when the remote's posts_test.go overwrote the local one in the merge;
// the validation logic in handleCreatePost is correct (see lines
// guarding `len(body.Targets) == 0`) but had no test coverage. This
// locks the contract in — including the exact error message clients
// see, so any future rewording is forced to update the test in lockstep.
func TestPostsAPI_Create_NoTargets_422(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, false)
	body := `{"workspace_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("at least one target is required")) {
		t.Errorf("response body should mention the missing-target error so clients can display it; got %q", w.Body.String())
	}
}

// TestPostsAPI_Create_BadTargetID_422 verifies the per-target validation
// guard: a target with platform_account_id==0 must be rejected with 422
// (the platform_account_id is required, so "missing" → 422 per §13.1).
// The 422 is emitted with an index in the error message so the client
// can identify which target was bad. Companion to
// TestPostsAPI_Create_NoTargets_422: together they cover the two
// "missing required field" cases for the CreatePostRequest. The body
// assertion locks the exact error message (including the index) so a
// future reword or removal is forced to update the test in lockstep.
func TestPostsAPI_Create_BadTargetID_422(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{}, false)
	body := `{"workspace_id":1,"targets":[{"platform_account_id":0}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("targets[0].platform_account_id is required")) {
		t.Errorf("response body should mention the per-target index for client error reporting; got %q", w.Body.String())
	}
}

// TestPostsAPI_Get_CrossOwner_404 verifies handleGetPost's cross-tenant
// isolation: when the post's workspace is owned by a different user, the
// handler returns 404 (NOT 403) to prevent workspace-existence leaks
// across tenants. The post exists and is findable, the workspace exists
// and is findable, but ws.OwnerID (999) != caller's userID (the lenient
// default of 1) → 404. Companion to TestHandleGetPost_CrossOwner_404
// in routes_test.go (which uses the strict mode + JWT path); this test
// uses the lenient path so the two together cover both auth modes.
//
// The test proves the 404 came from the CROSS-OWNER path (not from a
// post-not-found or workspace-lookup-error path) via two assertions:
//   1. workspaceStore.findByIDFn was called exactly once (a "post not
//      found" from `p == nil` would short-circuit BEFORE the workspace
//      lookup; a "failed to get post" would not reach the workspace at
//      all).
//   2. The response body is the cross-owner "post not found" string —
//      NOT the "failed to get post: " prefix that mapRepoError would
//      produce for a FindByID error.
func TestPostsAPI_Get_CrossOwner_404(t *testing.T) {
	wsCalled := int64(0)
	r := newPostsTestRouter(
		&mockPostStore{
			findByIDFn: func(id int64) (*models.Post, error) {
				return &models.Post{
					ID:          id,
					WorkspaceID: 1,
					Title:       "secret",
					Status:      models.PostStatusDraft,
					CreatedAt:   time.Now(),
				}, nil
			},
		},
		false,
		&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				wsCalled++
				return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
			},
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
	// (1) The workspace WAS looked up — proves we got past the post
	// not-found short-circuit and into the cross-tenant check.
	if wsCalled != 1 {
		t.Errorf("workspaceStore.findByIDFn call count: want 1, got %d (404 must come from the cross-owner check, not an earlier short-circuit)", wsCalled)
	}
	// (2) The body is the cross-owner "post not found" — not the
	// mapRepoError "failed to get post: ..." prefix.
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error":"post not found"`)) {
		t.Errorf("response body should be the cross-owner 404 message, not a mapRepoError 'failed to get post' message; got %q", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("failed to get post")) {
		t.Errorf("response body looks like a mapRepoError 'failed to get post' 404, not the cross-owner 404; got %q", w.Body.String())
	}
}

func TestMapRepoError_AllSentinalMappings(t *testing.T) {
	// Lock-in test for the per-handler mapping helper. Any future PR that
	// adds a new sentinel (or reverses the 403 vs 404 policy on
	// ErrPostUnauthorized) must update this test in lockstep.
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"ErrPostUnauthorized→403", repository.ErrPostUnauthorized, http.StatusForbidden},
		{"ErrPostNotFound→404", repository.ErrPostNotFound, http.StatusNotFound},
		{"ErrPostTargetNotFound→404", repository.ErrPostTargetNotFound, http.StatusNotFound},
		{"sqlErrNoRows→404", sql.ErrNoRows, http.StatusNotFound},
		{"plain→500", errBoomAPI, http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := mapRepoError(c.err); got != c.want {
				t.Errorf("mapRepoError(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// --- createPostResponse.MarshalJSON contract tests (HANDOFF-LINUX.md §13.3) ---
//
// The MarshalJSON on createPostResponse emits a dual-shape payload: the post
// fields are promoted to the top level (so a flat-decode consumer works) AND
// the same *models.Post is reachable under a nested "post" key (so the
// nested-decode consumer works). These tests lock both decoder paths in so
// a future "simplification" can't silently break one of them. Any change
// to the dual-shape contract MUST update HANDOFF-LINUX.md §13.3 and the
// createPostResponse docstring in pkg/api/posts.go in lockstep.

// TestCreatePostResponse_FlatDecoder_PopulatesTopLevelFields locks in the
// flat-decode contract used by the routes_test.go helper that calls
// json.NewDecoder(w.Body).Decode(&flat{}) where flat is a struct with the
// post fields at the top level. If MarshalJSON ever drops the top-level
// promotion, this test fails before the SPA breaks in production.
func TestCreatePostResponse_FlatDecoder_PopulatesTopLevelFields(t *testing.T) {
	scheduledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	post := &models.Post{
		ID:          100,
		WorkspaceID: 1,
		Title:       "hello",
		Caption:     "world",
		MediaURL:    "https://cdn.example.com/x.jpg",
		ScheduledAt: &scheduledAt,
		Status:      models.PostStatusScheduled,
		CreatedAt:   createdAt,
	}
	targets := []*models.PostTarget{
		{ID: 200, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusScheduled},
		{ID: 201, PostID: 100, PlatformAccountID: 11, Status: models.PostStatusScheduled},
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: targets})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var flat struct {
		ID          int64             `json:"id"`
		WorkspaceID int64             `json:"workspace_id"`
		Title       string            `json:"title"`
		Caption     string            `json:"caption"`
		MediaURL    string            `json:"media_url"`
		ScheduledAt *time.Time        `json:"scheduled_at"`
		Status      models.PostStatus `json:"status"`
		CreatedAt   time.Time         `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("unmarshal flat: %v (raw=%s)", err, string(raw))
	}
	if flat.ID != post.ID {
		t.Errorf("id: want %d, got %d", post.ID, flat.ID)
	}
	if flat.WorkspaceID != post.WorkspaceID {
		t.Errorf("workspace_id: want %d, got %d", post.WorkspaceID, flat.WorkspaceID)
	}
	if flat.Title != post.Title {
		t.Errorf("title: want %q, got %q", post.Title, flat.Title)
	}
	if flat.Caption != post.Caption {
		t.Errorf("caption: want %q, got %q", post.Caption, flat.Caption)
	}
	if flat.MediaURL != post.MediaURL {
		t.Errorf("media_url: want %q, got %q", post.MediaURL, flat.MediaURL)
	}
	if flat.ScheduledAt == nil || !flat.ScheduledAt.Equal(*post.ScheduledAt) {
		t.Errorf("scheduled_at: want %v, got %v", post.ScheduledAt, flat.ScheduledAt)
	}
	if flat.Status != post.Status {
		t.Errorf("status: want %q, got %q", post.Status, flat.Status)
	}
	if !flat.CreatedAt.Equal(post.CreatedAt) {
		t.Errorf("created_at: want %v, got %v", post.CreatedAt, flat.CreatedAt)
	}
}

// TestCreatePostResponse_NestedDecoder_PopulatesPostAndTargets locks in the
// nested-decode contract used by TestPostsAPI_Create_Happy_ReturnsPostPlusTargets
// (which decodes into {Post *models.Post; Targets []*models.PostTarget}).
// If MarshalJSON ever drops the "post" key or the "targets" key, this test
// fails before the SPA's nested-decode path breaks in production.
func TestCreatePostResponse_NestedDecoder_PopulatesPostAndTargets(t *testing.T) {
	scheduledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	post := &models.Post{
		ID:          100,
		WorkspaceID: 1,
		Title:       "hello",
		Caption:     "world",
		MediaURL:    "https://cdn.example.com/x.jpg",
		ScheduledAt: &scheduledAt,
		Status:      models.PostStatusScheduled,
		CreatedAt:   createdAt,
	}
	targets := []*models.PostTarget{
		{ID: 200, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusScheduled},
		{ID: 201, PostID: 100, PlatformAccountID: 11, Status: models.PostStatusScheduled},
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: targets})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var nested struct {
		Post    *models.Post         `json:"post"`
		Targets []*models.PostTarget `json:"targets"`
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		t.Fatalf("unmarshal nested: %v (raw=%s)", err, string(raw))
	}
	if nested.Post == nil {
		t.Fatalf("nested.post is nil (raw=%s)", string(raw))
	}
	if nested.Post.ID != post.ID {
		t.Errorf("post.id: want %d, got %d", post.ID, nested.Post.ID)
	}
	if nested.Post.WorkspaceID != post.WorkspaceID {
		t.Errorf("post.workspace_id: want %d, got %d", post.WorkspaceID, nested.Post.WorkspaceID)
	}
	if nested.Post.Title != post.Title {
		t.Errorf("post.title: want %q, got %q", post.Title, nested.Post.Title)
	}
	if nested.Post.Status != post.Status {
		t.Errorf("post.status: want %q, got %q", post.Status, nested.Post.Status)
	}
	if nested.Post.ScheduledAt == nil || !nested.Post.ScheduledAt.Equal(*post.ScheduledAt) {
		t.Errorf("post.scheduled_at: want %v, got %v", post.ScheduledAt, nested.Post.ScheduledAt)
	}
	if !nested.Post.CreatedAt.Equal(post.CreatedAt) {
		t.Errorf("post.created_at: want %v, got %v", post.CreatedAt, nested.Post.CreatedAt)
	}
	if len(nested.Targets) != 2 {
		t.Fatalf("targets length: want 2, got %d", len(nested.Targets))
	}
	if nested.Targets[0].ID != 200 || nested.Targets[0].PlatformAccountID != 10 {
		t.Errorf("targets[0]: want {ID:200,PlatformAccountID:10}, got %+v", nested.Targets[0])
	}
	if nested.Targets[1].ID != 201 || nested.Targets[1].PlatformAccountID != 11 {
		t.Errorf("targets[1]: want {ID:201,PlatformAccountID:11}, got %+v", nested.Targets[1])
	}
}

// TestCreatePostResponse_DualShape_FlatAndNestedAgree is the belt-and-
// suspenders test: it decodes the SAME payload into BOTH a flat struct
// and a nested struct and asserts that the values agree. This catches a
// "simplification" that, e.g., removes the top-level promotion but leaves
// the nested "post" key alone (the individual tests would still pass on
// the nested side; this test fails on the flat side).
func TestCreatePostResponse_DualShape_FlatAndNestedAgree(t *testing.T) {
	scheduledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	post := &models.Post{
		ID:          999,
		WorkspaceID: 7,
		Title:       "dual-shape",
		Caption:     "contract",
		MediaURL:    "https://cdn.example.com/y.jpg",
		ScheduledAt: &scheduledAt,
		Status:      models.PostStatusDraft,
		CreatedAt:   createdAt,
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: nil})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var flat struct {
		ID          int64             `json:"id"`
		WorkspaceID int64             `json:"workspace_id"`
		Title       string            `json:"title"`
		Status      models.PostStatus `json:"status"`
		ScheduledAt *time.Time        `json:"scheduled_at"`
		CreatedAt   time.Time         `json:"created_at"`
	}
	var nested struct {
		Post *models.Post `json:"post"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("unmarshal flat: %v", err)
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		t.Fatalf("unmarshal nested: %v", err)
	}
	if nested.Post == nil {
		t.Fatal("nested.post is nil")
	}
	if flat.ID != nested.Post.ID {
		t.Errorf("id: flat=%d, nested=%d (must be equal)", flat.ID, nested.Post.ID)
	}
	if flat.WorkspaceID != nested.Post.WorkspaceID {
		t.Errorf("workspace_id: flat=%d, nested=%d (must be equal)", flat.WorkspaceID, nested.Post.WorkspaceID)
	}
	if flat.Title != nested.Post.Title {
		t.Errorf("title: flat=%q, nested=%q (must be equal)", flat.Title, nested.Post.Title)
	}
	if flat.Status != nested.Post.Status {
		t.Errorf("status: flat=%q, nested=%q (must be equal)", flat.Status, nested.Post.Status)
	}
	if (flat.ScheduledAt == nil) != (nested.Post.ScheduledAt == nil) {
		t.Errorf("scheduled_at nilness diverges: flat=%v, nested=%v", flat.ScheduledAt, nested.Post.ScheduledAt)
	} else if flat.ScheduledAt != nil && !flat.ScheduledAt.Equal(*nested.Post.ScheduledAt) {
		t.Errorf("scheduled_at: flat=%v, nested=%v", flat.ScheduledAt, nested.Post.ScheduledAt)
	}
	if !flat.CreatedAt.Equal(nested.Post.CreatedAt) {
		t.Errorf("created_at: flat=%v, nested=%v", flat.CreatedAt, nested.Post.CreatedAt)
	}
}

// TestCreatePostResponse_Targets_TopLevelKey_PreservesOrder locks in two
// aspects of the targets encoding:
//   1. "targets" lives at the TOP level of the response, not inside the
//      nested "post" object (otherwise a flat-decode consumer looking
//      for `resp.targets` would silently get nothing).
//   2. The targets slice is JSON-encoded in slice order, preserving
//      per-target IDs, platform_account_ids, statuses, and error
//      messages. A future "sort by id" optimization would break the
//      documented ordering and the per-platform error reporting.
func TestCreatePostResponse_Targets_TopLevelKey_PreservesOrder(t *testing.T) {
	post := &models.Post{
		ID:          1,
		WorkspaceID: 1,
		Status:      models.PostStatusScheduled,
		CreatedAt:   time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	targets := []*models.PostTarget{
		{ID: 200, PostID: 1, PlatformAccountID: 10, Status: models.PostStatusScheduled},
		{ID: 201, PostID: 1, PlatformAccountID: 11, Status: models.PostStatusDraft},
		{ID: 202, PostID: 1, PlatformAccountID: 12, Status: models.PostStatusFailed, ErrorMessage: "rate limited"},
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: targets})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// (1) Structural check: "targets" is at the top level, not nested
	// under "post".
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal top: %v", err)
	}
	postRaw, ok := top["post"]
	if !ok {
		t.Fatalf("top-level 'post' key missing (raw=%s)", string(raw))
	}
	var postObj map[string]json.RawMessage
	if err := json.Unmarshal(postRaw, &postObj); err != nil {
		t.Fatalf("unmarshal nested 'post': %v", err)
	}
	if _, hasNestedTargets := postObj["targets"]; hasNestedTargets {
		t.Errorf("nested 'post' has its own 'targets' key (would create a contract ambiguity): %s", string(postRaw))
	}

	// (2) Order + per-target fields preserved.
	var asNested struct {
		Targets []*models.PostTarget `json:"targets"`
	}
	if err := json.Unmarshal(raw, &asNested); err != nil {
		t.Fatalf("unmarshal nested targets: %v", err)
	}
	if len(asNested.Targets) != 3 {
		t.Fatalf("targets length: want 3, got %d (raw=%s)", len(asNested.Targets), string(raw))
	}
	for i, want := range targets {
		got := asNested.Targets[i]
		if got.ID != want.ID {
			t.Errorf("targets[%d].ID: want %d, got %d", i, want.ID, got.ID)
		}
		if got.PlatformAccountID != want.PlatformAccountID {
			t.Errorf("targets[%d].PlatformAccountID: want %d, got %d", i, want.PlatformAccountID, got.PlatformAccountID)
		}
		if got.Status != want.Status {
			t.Errorf("targets[%d].Status: want %q, got %q", i, want.Status, got.Status)
		}
		if got.ErrorMessage != want.ErrorMessage {
			t.Errorf("targets[%d].ErrorMessage: want %q, got %q", i, want.ErrorMessage, got.ErrorMessage)
		}
	}
}

// TestCreatePostResponse_StructuralKeys_ExactlyTheseAndNoMore is the
// defense-in-depth assertion: after the dual-shape contract is in place,
// the set of top-level keys is FIXED. A future "simplification" that
// adds, renames, or removes a top-level key would silently break the
// flat-decode consumer (which expects a known set of fields) and/or the
// nested-decode consumer (which looks up exactly "post" and "targets").
// This test forces any such change to be a conscious one.
func TestCreatePostResponse_StructuralKeys_ExactlyTheseAndNoMore(t *testing.T) {
	post := &models.Post{
		ID:          1,
		WorkspaceID: 1,
		Status:      models.PostStatusDraft,
		CreatedAt:   time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: nil})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal top: %v", err)
	}
	// The contract says: 8 promoted post fields + 2 envelope keys.
	// Any drift from this set is a contract change and must update
	// HANDOFF-LINUX.md §13.3 + the createPostResponse docstring in
	// pkg/api/posts.go in lockstep.
	wantKeys := map[string]bool{
		"id":           true,
		"workspace_id": true,
		"title":        true,
		"caption":      true,
		"media_url":    true,
		"scheduled_at": true,
		"status":       true,
		"created_at":   true,
		"post":         true,
		"targets":      true,
	}
	for k := range top {
		if !wantKeys[k] {
			t.Errorf("unexpected top-level key %q in createPostResponse (would break the dual-shape contract); raw=%s", k, string(raw))
		}
	}
	for k := range wantKeys {
		if _, ok := top[k]; !ok {
			t.Errorf("missing required top-level key %q in createPostResponse (would break the dual-shape contract); raw=%s", k, string(raw))
		}
	}
}

// TestCreatePostResponse_ScheduledAt_NilFlatIsNullNestedIsOmitted locks
// in a subtle asymmetry: when a post has no scheduled_at, the top-level
// "scheduled_at" is JSON `null` (because MarshalJSON writes a nil
// *time.Time from the map[string]interface{}), but the nested "post"
// object OMITS the "scheduled_at" key entirely (because models.Post's
// tag is `json:"scheduled_at,omitempty"` on a *time.Time).
//
// This is a real consumer-facing quirk: a flat-decode client that uses
// `var s *time.Time` will see `nil` (and JSON `null`), but a
// nested-decode client using the same `*time.Time` will also see `nil`
// because the key is absent and Go's json package leaves the pointer
// as its zero value. The behaviour is therefore *consistent at the Go
// level* but the wire shape is asymmetric. This test pins the wire
// shape so a future refactor that "fixes" the asymmetry is forced to
// make the choice consciously and update HANDOFF-LINUX.md §13.3.
func TestCreatePostResponse_ScheduledAt_NilFlatIsNullNestedIsOmitted(t *testing.T) {
	post := &models.Post{
		ID:          1,
		WorkspaceID: 1,
		Status:      models.PostStatusDraft,
		CreatedAt:   time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		// ScheduledAt deliberately nil.
	}
	raw, err := json.Marshal(createPostResponse{post: post, targets: nil})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal top: %v", err)
	}
	// Top-level "scheduled_at" must be present and equal to JSON null
	// (NOT absent — the key is in the contract set per the structural
	// test above).
	flatRaw, ok := top["scheduled_at"]
	if !ok {
		t.Fatalf("top-level 'scheduled_at' key missing (raw=%s)", string(raw))
	}
	if string(flatRaw) != "null" {
		t.Errorf("top-level 'scheduled_at' for nil schedule: want null, got %s", string(flatRaw))
	}
	// Nested "post" must OMIT the "scheduled_at" key (omitempty wins).
	postRaw, ok := top["post"]
	if !ok {
		t.Fatalf("top-level 'post' key missing (raw=%s)", string(raw))
	}
	var postObj map[string]json.RawMessage
	if err := json.Unmarshal(postRaw, &postObj); err != nil {
		t.Fatalf("unmarshal nested 'post': %v", err)
	}
	if _, hasIt := postObj["scheduled_at"]; hasIt {
		t.Errorf("nested 'post' has 'scheduled_at' for nil schedule; expected it to be omitted (omitempty); raw=%s", string(postRaw))
	}
}

var errBoomAPI = errBoom{}

// errBoom is a generic non-sentinel error for the mapRepoError test.
type errBoom struct{}

func (errBoom) Error() string { return "boom" }
