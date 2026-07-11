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

// mockPostStore is a configurable PostStore mock used by the new /posts
// endpoint tests. Function fields default to safe no-ops if unset.
type mockPostStore struct {
	createFn          func(p *models.Post, targets []*models.PostTarget) error
	findByIDFn        func(id int64) (*models.Post, error)
	updateFn          func(p *models.Post) error
	listByWorkspaceFn func(workspaceID int64) ([]models.Post, error)
	saveFn            func(t *models.PostTarget) error
}

func (m *mockPostStore) Create(p *models.Post, targets []*models.PostTarget) error {
	if m.createFn != nil {
		return m.createFn(p, targets)
	}
	p.ID = 100
	for i, tgt := range targets {
		tgt.ID = int64(1000 + i)
		tgt.PostID = 100
	}
	return nil
}

func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return &models.Post{ID: id, WorkspaceID: 1, Title: "default"}, nil
}

func (m *mockPostStore) Update(p *models.Post) error {
	if m.updateFn != nil {
		return m.updateFn(p)
	}
	return nil
}

func (m *mockPostStore) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	if m.listByWorkspaceFn != nil {
		return m.listByWorkspaceFn(workspaceID)
	}
	return nil, nil
}

func (m *mockPostStore) Save(t *models.PostTarget) error {
	if m.saveFn != nil {
		return m.saveFn(t)
	}
	t.ID = 999
	return nil
}

// newPostsTestRouter builds a Router wired with a noop workspace store and
// the supplied postStore. Use for /posts endpoint tests.
func newPostsTestRouter(
	postStore *mockPostStore,
	strictAuth bool,
) *Router {
	return NewRouter(
		map[string]services.PlatformService{},
		&mockUserStore{},
		&mockWorkspaceStore{},
		postStore,
		auth.NewManager(testJWTSecret, 24),
		strictAuth,
		"",
		nil,
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		bytes.NewReader([]byte(`{"workspace_id":1}`)))
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

var errBoomAPI = errBoom{}

// errBoom is a generic non-sentinel error for the mapRepoError test.
type errBoom struct{}

func (errBoom) Error() string { return "boom" }
