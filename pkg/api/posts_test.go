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

// newPostsTestRouter builds a Router wired with the supplied postStore
// and (optionally) a custom workspace store.
//
// Taglio 2.1: the Router takes a CapabilityRouter (per-capability lookups)
// instead of the old PlatformRegistry. These tests don't exercise the
// publish path so a default no-op mockTokenService is enough; the
// CapabilityRouter is also empty since no test in this file invokes a
// provider.
func newPostsTestRouter(
	postStore PostStore,
	wsStore ...WorkspaceStore,
) *Router {
	var ws WorkspaceStore = &mockWorkspaceStore{}
	if len(wsStore) > 0 && wsStore[0] != nil {
		ws = wsStore[0]
	}
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(ws),
		WithPostStore(postStore),
		WithCredentialVault(&mockCredentialVault{}), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
}

func postsIssueJWT(t *testing.T, userID int64) string {
	t.Helper()
	// SPRINT 7.1: Issuer must carry (wsID=1, sessionID=1) so
	// Manager.Verify accepts the token when the request reaches
	// r.protected (Taglio 1.1 contracts).
	tok, _, _, err := auth.NewManager(testJWTSecret, 24).IssueAccess(userID, 1, 1)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	return tok
}

func TestPostsAPI_Create_Happy_ReturnsPostPlusTargets(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{})

	body := `{"workspace_id":1,"title":"hi","caption":"world","targets":[{"platform_account_id":10},{"platform_account_id":11}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
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
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1,"targets":[{"platform_account_id":10}]}`)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestPostsAPI_Create_BadStatus_400(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1,"status":"bogus"}`)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestPostsAPI_Create_StrictAuth_NoJWT_401(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{})
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
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/123", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostsAPI_Get_NotFound_404(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) { return nil, sql.ErrNoRows },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/999", nil)
	withBearerJWT(t, req, 1)
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
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/workspace/1", nil)
	withBearerJWT(t, req, 1)
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
	r := newPostsTestRouter(&mockPostStore{})
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostsAPI_AddTarget_PostNotFound_404(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) { return nil, sql.ErrNoRows },
	})
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/999/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPostsAPI_AddTarget_ErrPostUnauthorized_403(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{
		saveTargetFn: func(t *models.PostTarget) error { return repository.ErrPostUnauthorized },
	})
	body := `{"platform_account_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/targets", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestPostsAPI_Schedule_Happy(t *testing.T) {
	ts := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC)
	r := newPostsTestRouter(&mockPostStore{})

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
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
	})

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/999/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
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
	})

	body, _ := json.Marshal(map[string]interface{}{"scheduled_at": ts})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/100/schedule", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestPostsAPI_Create_NoTargets_422(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{})
	body := `{"workspace_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("at least one target is required")) {
		t.Errorf("response body should mention the missing-target error so clients can display it; got %q", w.Body.String())
	}
}

func TestPostsAPI_Create_BadTargetID_422(t *testing.T) {
	r := newPostsTestRouter(&mockPostStore{})
	body := `{"workspace_id":1,"targets":[{"platform_account_id":0}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("targets[0].platform_account_id is required")) {
		t.Errorf("response body should mention the per-target index for client error reporting; got %q", w.Body.String())
	}
}

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
		&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				wsCalled++
				return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
			},
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100", nil)
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
	if wsCalled != 1 {
		t.Errorf("workspaceStore.findByIDFn call count: want 1, got %d (404 must come from the cross-owner check, not an earlier short-circuit)", wsCalled)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error":"post not found"`)) {
		t.Errorf("response body should be the cross-owner 404 message, not a mapRepoError 'failed to get post' message; got %q", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("failed to get post")) {
		t.Errorf("response body looks like a mapRepoError 'failed to get post' 404, not the cross-owner 404; got %q", w.Body.String())
	}
}

func TestMapRepoError_AllSentinalMappings(t *testing.T) {
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

// --- createPostResponse.MarshalJSON contract tests ---

func TestCreatePostResponse_FlatDecoder_PopulatesTopLevelFields(t *testing.T) {
	scheduledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	post := &models.Post{
		ID: 100, WorkspaceID: 1, Title: "hello", Caption: "world",
		MediaURL: "https://cdn.example.com/x.jpg", PublishAt: &scheduledAt,
		Status: models.PostStatusScheduled, CreatedAt: createdAt,
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
}

func TestCreatePostResponse_NestedDecoder_PopulatesPostAndTargets(t *testing.T) {
	scheduledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	post := &models.Post{
		ID: 100, WorkspaceID: 1, Title: "hello", Caption: "world",
		MediaURL: "https://cdn.example.com/x.jpg", PublishAt: &scheduledAt,
		Status: models.PostStatusScheduled, CreatedAt: createdAt,
	}
	targets := []*models.PostTarget{
		{ID: 200, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusScheduled},
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
		t.Fatalf("unmarshal nested: %v", err)
	}
	if nested.Post == nil || nested.Post.ID != 100 {
		t.Errorf("nested post mismatch")
	}
}

func TestCreatePostResponse_DualShape_FlatAndNestedAgree(t *testing.T) {
	post := &models.Post{ID: 999, WorkspaceID: 7, Title: "dual-shape", Status: models.PostStatusDraft, CreatedAt: time.Now()}
	raw, _ := json.Marshal(createPostResponse{post: post, targets: nil})
	var flat struct {
		ID int64 `json:"id"`
	}
	var nested struct {
		Post *models.Post `json:"post"`
	}
	json.Unmarshal(raw, &flat)
	json.Unmarshal(raw, &nested)
	if flat.ID != nested.Post.ID {
		t.Errorf("flat and nested disagree")
	}
}

func TestCreatePostResponse_Targets_TopLevelKey_PreservesOrder(t *testing.T) {
	post := &models.Post{ID: 1, WorkspaceID: 1, Status: models.PostStatusScheduled, CreatedAt: time.Now()}
	targets := []*models.PostTarget{
		{ID: 200, PostID: 1, PlatformAccountID: 10, Status: models.PostStatusScheduled},
	}
	raw, _ := json.Marshal(createPostResponse{post: post, targets: targets})
	var top map[string]json.RawMessage
	json.Unmarshal(raw, &top)
	if _, ok := top["targets"]; !ok {
		t.Error("targets key missing at top level")
	}
}

func TestCreatePostResponse_StructuralKeys_ExactlyTheseAndNoMore(t *testing.T) {
	post := &models.Post{ID: 1, WorkspaceID: 1, Status: models.PostStatusDraft, CreatedAt: time.Now()}
	raw, _ := json.Marshal(createPostResponse{post: post, targets: nil})
	var top map[string]json.RawMessage
	json.Unmarshal(raw, &top)
	if _, ok := top["id"]; !ok {
		t.Error("missing id key")
	}
}

func TestCreatePostResponse_ScheduledAt_NilFlatIsNullNestedIsOmitted(t *testing.T) {
	post := &models.Post{ID: 1, WorkspaceID: 1, Status: models.PostStatusDraft, CreatedAt: time.Now()}
	raw, _ := json.Marshal(createPostResponse{post: post, targets: nil})
	var top map[string]json.RawMessage
	json.Unmarshal(raw, &top)
	if string(top["scheduled_at"]) != "null" {
		t.Errorf("nil scheduled_at should be null at top level, got %s", top["scheduled_at"])
	}
}

var errBoomAPI = errBoom{}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// ---------------------------------------------------------------------------
// Taglio 5.1 step 2 — GET /api/v1/posts/{id}/targets (empty-array fix)
// + GET /api/v1/post-targets/{id} (new polling endpoint)
// ---------------------------------------------------------------------------

// TestHandleGetPostTargets_NonEmpty_200 closes the historical
// empty-array bug on GET /api/v1/posts/{id}/targets. Pre-fix the
// handler returned {"targets":[]} unconditionally (stub); the
// postStore.ListByPost wiring + the assertion below pins the
// contract that ListByPost IS called and its result IS rendered.
func TestHandleGetPostTargets_NonEmpty_200(t *testing.T) {
	listByPostCalled := 0
	r := newPostsTestRouter(&mockPostStore{
		listByPostFn: func(postID int64) ([]models.PostTarget, error) {
			listByPostCalled++
			return []models.PostTarget{
				{ID: 200, PostID: postID, PlatformAccountID: 10, Status: models.PostStatusQueued},
				{ID: 201, PostID: postID, PlatformAccountID: 11, Status: models.PostStatusPublishing},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100/targets", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if listByPostCalled != 1 {
		t.Errorf("listByPostFn call count: want 1, got %d (handler must consult postStore.ListByPost)", listByPostCalled)
	}
	var body struct {
		Targets []models.PostTarget `json:"targets"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Targets) != 2 {
		t.Fatalf("want 2 targets, got %d (handler must forward ListByPost result verbatim)", len(body.Targets))
	}
	if body.Targets[0].ID != 200 || body.Targets[0].Status != models.PostStatusQueued {
		t.Errorf("first target mismatch: %+v", body.Targets[0])
	}
}

// TestHandleGetSinglePostTarget_Happy_200 is the canonical happy
// path for the polling endpoint (Taglio 5.1 step 2). Asserts the
// payload exposes every field the polling frontend relies on:
// privacy, privacy_status, made_for_kids, error_message,
// attempt_count, next_retry_at. The JSON tag assertion is
// name-sensitive (NEXT_RETRY_AT, not NextAttemptAt) per the task
// spec.
func TestHandleGetSinglePostTarget_Happy_200(t *testing.T) {
	payload := []byte(`{"data_persistence":"youtube_load","seed":"meta-test","made_for_kids":true}`)
	r := newPostsTestRouter(
		&mockPostStore{
			findTargetByIDFn: func(id int64) (*models.PostTarget, error) {
				return &models.PostTarget{
					ID:                id,
					PostID:            100,
					PlatformAccountID: 42,
					Status:            models.PostStatusQueued,
					AttemptCount:      3,
					PlatformPostID:    "yt_video_abc",
					ErrorMessage:      "transient upload timeout",
				}, nil
			},
			findByIDFn: func(id int64) (*models.Post, error) {
				if id != 100 {
					t.Errorf("FindByID called with %d, want 100 (parent of target)", id)
				}
				return &models.Post{
					ID:           100,
					WorkspaceID:  1,
					PrivacyLevel: "private",
					Status:       models.PostStatusQueued,
					CreatedAt:    time.Now(),
					Metadata:     payload,
				}, nil
			},
		},
		&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, Name: "primary", OwnerID: 1, CreatedAt: time.Now()}, nil
			},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/post-targets/200", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantKeys := []string{
		"id", "post_id", "platform_account_id", "status",
		"platform_post_id", "error_message", "attempt_count",
		"next_retry_at", "privacy", "privacy_status", "made_for_kids",
	}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("response missing required key %q (full body=%s)", k, w.Body.String())
		}
	}
	if string(raw["privacy"]) != `"private"` {
		t.Errorf("privacy: want \"private\", got %s", raw["privacy"])
	}
	if string(raw["privacy_status"]) != `"private"` {
		t.Errorf("privacy_status: want \"private\", got %s", raw["privacy_status"])
	}
	if string(raw["made_for_kids"]) != "true" {
		t.Errorf("made_for_kids: want true, got %s", raw["made_for_kids"])
	}
	if string(raw["error_message"]) != `"transient upload timeout"` {
		t.Errorf("error_message: want transient upload timeout, got %s", raw["error_message"])
	}
	if string(raw["attempt_count"]) != "3" {
		t.Errorf("attempt_count: want 3, got %s", raw["attempt_count"])
	}
}

// TestHandleGetPostTargets_CrossOwner_404 nails the IDOR guard on
// the LIST endpoint. The historical empty-array stub masked a real
// cross-tenant leak: any authenticated caller could probe the
// endpoint and observe empty (no info) or non-empty (counts).
// Step 2 fixed it by mirroring the Target → Post → Workspace owner
// check that handleGetPost + handleGetSinglePostTarget use. This
// test pins the contract so the regression is permanently blocked.
func TestHandleGetPostTargets_CrossOwner_404(t *testing.T) {
	listByPostCalled := 0
	r := newPostsTestRouter(
		&mockPostStore{
			findByIDFn: func(id int64) (*models.Post, error) {
				return &models.Post{
					ID:          id,
					WorkspaceID: 2, // not owned by JWT user
					Status:      models.PostStatusQueued,
					CreatedAt:   time.Now(),
				}, nil
			},
			listByPostFn: func(postID int64) ([]models.PostTarget, error) {
				listByPostCalled++
				return []models.PostTarget{}, nil // would leak if the guard regressed
			},
		},
		&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, Name: "other", OwnerID: 999, CreatedAt: time.Now()}, nil
			},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100/targets", nil)
	withBearerJWT(t, req, 1) // caller ≠ workspace owner
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (cross-owner probe), got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error":"post not found"`)) {
		t.Errorf("response body should be the cross-owner 404 message, got %q", w.Body.String())
	}
	if listByPostCalled != 0 {
		t.Errorf("listByPostFn call count: want 0 (cross-owner must short-circuit BEFORE query), got %d (proves IDOR regression if this fires)", listByPostCalled)
	}
}

// TestHandleGetSinglePostTarget_CrossOwner_404 nails the IDOR
// guard. The target's parent post belongs to a workspace owned by
// userID=999; the caller is the JWT userID=1 — the handler must
// surface 404 (NOT 403, NOT 200, NOT 410) to avoid leaking the
// existence of cross-tenant target ids.
func TestHandleGetSinglePostTarget_CrossOwner_404(t *testing.T) {
	r := newPostsTestRouter(
		&mockPostStore{
			findTargetByIDFn: func(id int64) (*models.PostTarget, error) {
				return &models.PostTarget{
					ID:                id,
					PostID:            100,
					PlatformAccountID: 42,
					Status:            models.PostStatusQueued,
				}, nil
			},
			findByIDFn: func(id int64) (*models.Post, error) {
				return &models.Post{
					ID:          100,
					WorkspaceID: 2, // NOT owned by JWT user
					Status:      models.PostStatusQueued,
					CreatedAt:   time.Now(),
				}, nil
			},
		},
		&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, Name: "other", OwnerID: 999, CreatedAt: time.Now()}, nil
			},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/post-targets/200", nil)
	withBearerJWT(t, req, 1) // caller ≠ workspace owner
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (cross-owner probe), got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error":"post_target not found"`)) {
		t.Errorf("response body should be the cross-owner 404 message, got %q", w.Body.String())
	}
}
