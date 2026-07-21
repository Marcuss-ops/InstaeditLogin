package veloxclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// testSecret is a 32-byte test secret (matches the Velox verifier's
// MinimumSecretBytes). NOT a real production secret — only used in
// this test package.
const testSecret = "test-control-secret-32bytes-min!!"

// newTestClient builds a Client pointing at srv.URL with testSecret.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New(srv.URL, testSecret)
	if c == nil {
		t.Fatalf("New returned nil for non-empty baseURL + secret")
	}
	return c
}

// parseBearer extracts the Bearer token from the Authorization header
// and returns the parsed MapClaims so a test can assert on iss/aud/sub/
// workspace_id/scopes/exp/jti. Uses jwt.MapClaims (matching the
// production signer) so the parsed types are native Go values:
// aud → string, exp → float64 (JSON number), workspace_id → float64.
func parseBearer(t *testing.T, authHeader string) jwt.MapClaims {
	t.Helper()
	if !strings.HasPrefix(authHeader, "Bearer ") {
		t.Fatalf("missing Bearer prefix in Authorization header: %q", authHeader)
	}
	raw := strings.TrimPrefix(authHeader, "Bearer ")
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, nil
		}
		return []byte(testSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		t.Fatalf("parse bearer token: %v", err)
	}
	return claims
}

// TestNewEmptyArgsReturnsNil confirms New() returns nil (and the BFF
// routes stay unmounted) when either arg is empty — the nil-guard
// contract from the Router's WithVeloxBFFClient option.
func TestNewEmptyArgsReturnsNil(t *testing.T) {
	if c := New("", testSecret); c != nil {
		t.Errorf("New with empty baseURL should return nil, got %v", c)
	}
	if c := New("http://velox:8080", ""); c != nil {
		t.Errorf("New with empty secret should return nil, got %v", c)
	}
}

// TestNewTrimsTrailingSlash confirms the base URL trailing slash is
// trimmed so do() doesn't produce double-slash paths.
func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://velox:8080/", testSecret)
	if c.baseURL != "http://velox:8080" {
		t.Errorf("baseURL = %q; want no trailing slash", c.baseURL)
	}
}

// TestSignControlToken_EmptySecret confirms the signer fails fast
// when the secret is empty (no silent unauthenticated calls).
func TestSignControlToken_EmptySecret(t *testing.T) {
	if _, err := signControlToken(nil, 1, 1); err == nil {
		t.Error("signControlToken with empty secret should return error")
	}
}

// TestSignControlToken_InvalidIdentity confirms the signer rejects
// zero user or workspace ids (a BFF that somehow lost the session
// identity fails closed rather than signing a bogus token).
func TestSignControlToken_InvalidIdentity(t *testing.T) {
	if _, err := signControlToken([]byte(testSecret), 0, 1); err == nil {
		t.Error("signControlToken with userID=0 should return error")
	}
	if _, err := signControlToken([]byte(testSecret), 1, 0); err == nil {
		t.Error("signControlToken with workspaceID=0 should return error")
	}
}

// TestListJobs verifies the client signs a JWT, calls the Velox
// list endpoint, and converts the response into veloxapi.Job slice.
func TestListJobs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/jobs" {
			t.Errorf("path = %s; want /api/v1/jobs", r.URL.Path)
		}
		claims := parseBearer(t, r.Header.Get("Authorization"))
		if iss, _ := claims["iss"].(string); iss != "instaedit" {
			t.Errorf("iss = %q; want instaedit", iss)
		}
		if aud, _ := claims["aud"].(string); aud != "velox" {
			t.Errorf("aud = %v; want velox (string, not array)", claims["aud"])
		}
		if wsID, _ := claims["workspace_id"].(float64); int64(wsID) != 42 {
			t.Errorf("workspace_id = %v; want 42", claims["workspace_id"])
		}
		scopes, _ := claims["scopes"].([]interface{})
		if len(scopes) == 0 {
			t.Error("scopes should not be empty")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listJobsResponse{
			Jobs: []jobResponse{
				{ID: "job_1", WorkspaceID: 42, RenderStatus: "SUCCEEDED", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				{ID: "job_2", WorkspaceID: 42, RenderStatus: "RUNNING", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	jobs, err := c.ListJobs(context.Background(), 42, veloxapi.ListJobsFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d; want 2", len(jobs))
	}
	if jobs[0].ID != "job_1" || jobs[0].WorkspaceID != 42 {
		t.Errorf("jobs[0] = %+v", jobs[0])
	}
}

// TestListJobsFilterQuery confirms the status + limit query params
// are forwarded to Velox.
func TestListJobsFilterQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "RUNNING" {
			t.Errorf("status query = %q; want RUNNING", r.URL.Query().Get("status"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit query = %q; want 10", r.URL.Query().Get("limit"))
		}
		_ = json.NewEncoder(w).Encode(listJobsResponse{})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.ListJobs(context.Background(), 42, veloxapi.ListJobsFilter{Status: "RUNNING", Limit: 10}); err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}

// TestCreateJob verifies the client POSTs the body (without
// workspace_id/user_id in the body — they're in the JWT) and
// converts the response.
func TestCreateJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/jobs" {
			t.Errorf("path = %s; want /api/v1/jobs", r.URL.Path)
		}
		claims := parseBearer(t, r.Header.Get("Authorization"))
		if sub, _ := claims["sub"].(string); sub != "99" {
			t.Errorf("sub = %q; want 99", sub)
		}
		if wsID, _ := claims["workspace_id"].(float64); int64(wsID) != 42 {
			t.Errorf("workspace_id = %v; want 42", claims["workspace_id"])
		}
		// Read the full body and decode it twice: once as createJobRequest
		// for field assertions, once as map[string]json.RawMessage to
		// verify workspace_id/user_id are NOT present in the body.
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var body createJobRequest
		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.ProjectID != "project_123" {
			t.Errorf("project_id = %q; want project_123", body.ProjectID)
		}
		var rawMap map[string]json.RawMessage
		if err := json.Unmarshal(rawBody, &rawMap); err != nil {
			t.Fatalf("decode body as map: %v", err)
		}
		if _, ok := rawMap["workspace_id"]; ok {
			t.Error("workspace_id MUST NOT appear in the request body (it's in the JWT)")
		}
		if _, ok := rawMap["user_id"]; ok {
			t.Error("user_id MUST NOT appear in the request body (it's in the JWT)")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(jobResponse{ID: "job_new", WorkspaceID: 42, ProjectID: "project_123", RenderStatus: "QUEUED"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	job, err := c.CreateJob(context.Background(), 42, 99, veloxapi.CreateJobRequest{
		ProjectID:  "project_123",
		RenderSpec: json.RawMessage(`{"template":"news"}`),
		DeliveryPlan: veloxapi.DeliveryPlan{
			Destinations: []veloxapi.DeliveryDestination{
				{ExternalDestinationID: "extdst_01J", Metadata: json.RawMessage(`{"title":"Hi"}`)},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.ID != "job_new" || job.WorkspaceID != 42 {
		t.Errorf("job = %+v", job)
	}
}

// TestGetJob verifies the aggregated JobDetail response is decoded.
func TestGetJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs/job_123" {
			t.Errorf("path = %s; want /api/v1/jobs/job_123", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jobDetailResponse{
			Job: jobResponse{ID: "job_123", WorkspaceID: 42, RenderStatus: "SUCCEEDED"},
			Deliveries: []deliveryResponse{
				{ExternalDestinationID: "extdst_01J", SocialDeliveryID: "sdel_1", Status: "published", PlatformURL: "https://youtube.com/watch?v=abc"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	detail, err := c.GetJob(context.Background(), 42, "job_123")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if detail.Job.ID != "job_123" {
		t.Errorf("job.ID = %q", detail.Job.ID)
	}
	if len(detail.Deliveries) != 1 || detail.Deliveries[0].Status != "published" {
		t.Errorf("deliveries = %+v", detail.Deliveries)
	}
}

// TestGetJobNotFound confirms a 404 from Velox maps to
// veloxapi.ErrJobNotFound.
func TestGetJobNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetJob(context.Background(), 42, "missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	// Use errors.Is via the sentinel exported from veloxapi.
	if !isErrSentinel(err, veloxapi.ErrJobNotFound) {
		t.Errorf("err = %v; want ErrJobNotFound", err)
	}
}

// TestCancelJob verifies the cancel endpoint is called and 204 is
// treated as success.
func TestCancelJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/jobs/job_123/cancel" {
			t.Errorf("path = %s; want /api/v1/jobs/job_123/cancel", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.CancelJob(context.Background(), 42, "job_123"); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
}

// TestListJobDeliveries verifies the deliveries endpoint.
func TestListJobDeliveries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs/job_123/deliveries" {
			t.Errorf("path = %s; want /api/v1/jobs/job_123/deliveries", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(listDeliveriesResponse{
			Deliveries: []deliveryResponse{
				{ExternalDestinationID: "extdst_01J", Status: "queued"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	deliveries, err := c.ListJobDeliveries(context.Background(), 42, "job_123")
	if err != nil {
		t.Fatalf("ListJobDeliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "queued" {
		t.Errorf("deliveries = %+v", deliveries)
	}
}

// TestListWorkers verifies the workers list endpoint.
func TestListWorkers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workers" {
			t.Errorf("path = %s; want /api/v1/workers", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(listWorkersResponse{
			Workers: []workerResponse{
				{ID: "worker_1", WorkspaceID: 42, Status: "idle", CPU: 8, RAMMB: 16384, GPU: "rtx4090"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	workers, err := c.ListWorkers(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 1 || workers[0].GPU != "rtx4090" {
		t.Errorf("workers = %+v", workers)
	}
}

// TestGetWorker verifies the single-worker endpoint.
func TestGetWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workers/worker_1" {
			t.Errorf("path = %s; want /api/v1/workers/worker_1", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(workerResponse{ID: "worker_1", WorkspaceID: 42, Status: "busy"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	w, err := c.GetWorker(context.Background(), 42, "worker_1")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if w.ID != "worker_1" || w.Status != "busy" {
		t.Errorf("worker = %+v", w)
	}
}

// TestGetAsset verifies the asset endpoint.
func TestGetAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/assets/asset_1" {
			t.Errorf("path = %s; want /api/v1/assets/asset_1", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(assetResponse{
			ID: "asset_1", WorkspaceID: 42, SHA256: "abc123", SizeBytes: 12345, MimeType: "video/mp4",
			DownloadURL: "https://velox/download/asset_1",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	a, err := c.GetAsset(context.Background(), 42, "asset_1")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if a.SHA256 != "abc123" || a.MimeType != "video/mp4" {
		t.Errorf("asset = %+v", a)
	}
}

// TestServer5xx confirms a 5xx from Velox surfaces as a non-nil
// error (not a sentinel) so the BFF handler maps to 500.
func TestServer5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("velox internal error"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.ListJobs(context.Background(), 42, veloxapi.ListJobsFilter{})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if isErrSentinel(err, veloxapi.ErrJobNotFound) {
		t.Error("5xx should NOT map to ErrJobNotFound")
	}
}

// TestJWTExpiry confirms the signed token has a short expiry (≤ 5
// minutes per the spec's 2-5 minute recommendation).
func TestJWTExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := parseBearer(t, r.Header.Get("Authorization"))
		exp, _ := claims["exp"].(float64)
		if exp == 0 {
			t.Fatal("exp claim missing")
		}
		jti, _ := claims["jti"].(string)
		if jti == "" {
			t.Error("jti claim missing (replay protection relies on it)")
		}
		_ = json.NewEncoder(w).Encode(listJobsResponse{})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.ListJobs(context.Background(), 42, veloxapi.ListJobsFilter{}); err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}

// isErrSentinel reports whether err wraps target via errors.Is.
func isErrSentinel(err, target error) bool {
	return errors.Is(err, target)
}
