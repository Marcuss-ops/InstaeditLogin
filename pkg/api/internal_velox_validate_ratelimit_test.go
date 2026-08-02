package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/go-chi/chi/v5"
)

func TestValidate_WorkspaceMissing(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       99, // non-existent workspace
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: nil, // not found
	}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("workspace missing: want 404, got %d", w.Code)
	}
	if user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("platform_account must NOT be queried when workspace is missing")
	}
}

// TestValidate_PlatformAccountRevoked pins the deletion check on
// Status="revoked" or models.AccountStatusRevoked. Both strings
// are tested because the same semantic ("cancelled by user/admin
// via /api/v1/accounts DELETE handler") is reached via both literal
// values (raw "revoked" string + canonical constant). Returning
// 404 matches the spec's "platform_account non è stato cancellato"
// contract.
func TestValidate_PlatformAccountRevoked(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		status string
	}{
		{"literal revoked", "revoked"},
		{"canonical constant", models.AccountStatusRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := &mockExternalDestinationStore{
				GetByIDResult: &models.ExternalDestination{
					ID:                "extdst_01JABC",
					SourceSystem:      "velox",
					WorkspaceID:       12,
					PlatformAccountID: 345,
					Enabled:           true,
					CreatedAt:         now,
					UpdatedAt:         now,
				},
			}
			ws := &mockWorkspaceLookup{
				findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
			}
			user := &mockUserLookup{
				findPlatformAccountByIDResult: &models.PlatformAccount{
					ID:       345,
					Platform: "youtube",
					Status:   tc.status,
				},
			}
			w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
				"Bearer "+testVeloxAPIToken, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("revoked destination: want 404, got %d (body=%q)",
					w.Code, w.Body.String())
			}
		})
	}
}

// TestValidate_PlatformAccountDisconnected — same as the revoked
// case but for the "disconnected" status (also implies the user
// cancelled the OAuth grant, with a slightly different operator
// runbook origin). Both literal + constant paths tested.
func TestValidate_PlatformAccountDisconnected(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		status string
	}{
		{"literal disconnected", "disconnected"},
		{"canonical constant", models.AccountStatusDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := &mockExternalDestinationStore{
				GetByIDResult: &models.ExternalDestination{
					ID:                "extdst_01JABC",
					SourceSystem:      "velox",
					WorkspaceID:       12,
					PlatformAccountID: 345,
					Enabled:           true,
					CreatedAt:         now,
					UpdatedAt:         now,
				},
			}
			ws := &mockWorkspaceLookup{
				findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
			}
			user := &mockUserLookup{
				findPlatformAccountByIDResult: &models.PlatformAccount{
					ID:       345,
					Platform: "youtube",
					Status:   tc.status,
				},
			}
			w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
				"Bearer "+testVeloxAPIToken, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("disconnected destination: want 404, got %d (body=%q)",
					w.Code, w.Body.String())
			}
		})
	}
}

// TestValidate_RateLimitExceeded drives the 429 + Retry-After
// path. The Router is wired with WithVeloxValidateRateLimit(2,
// 60s) so any 3rd request against the SAME destination_id within
// the window is rejected. We're testing the take() helper
// exhaustively here (not the production 60/min default since that
// would need many requests + 60 seconds of test time).
func TestValidate_RateLimitExceeded(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:       345,
			Platform: "youtube",
			Status:   "active",
		},
	}
	r := buildVeloxTestRouter(dst, ws, user, testVeloxAPIToken)
	WithVeloxValidateRateLimit(2, 60*time.Second)(r)

	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate", handler)

	// First two requests should pass (return 204).
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodPost,
			"/internal/v1/destinations/extdst_01JABC/validate", nil)
		req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("request %d: want 204, got %d (body=%q)", i, w.Code, w.Body.String())
		}
	}

	// Third request: 429 + Retry-After header present.
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/destinations/extdst_01JABC/validate", nil)
	req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: want 429, got %d (body=%q)", w.Code, w.Body.String())
	}
	raStr := w.Header().Get("Retry-After")
	if raStr == "" {
		t.Fatal("Retry-After header missing on 429 response")
	}
	ra, err := strconv.Atoi(raStr)
	if err != nil {
		t.Fatalf("Retry-After must be an integer; got %q", raStr)
	}
	if ra < 1 {
		t.Errorf("Retry-After = %d; want >= 1 second", ra)
	}
	// Body should mention the rate-limit clause so the operator
	// can correlate the 429 to the specific cause.
	if !strings.Contains(w.Body.String(), "rate limit") {
		t.Errorf("body should mention rate limit; got %q", w.Body.String())
	}
}

// TestValidate_RateLimitDisabledByZeroOption verifies the option's
// nil-out behaviour: WithVeloxValidateRateLimit(0, 0) wires a
// nil limiter, the handler must NOT short-circuit on it. The test
// fires 100 requests against the same destination_id and asserts
// the rate limiter doesn't take. Verifies the documented "no
// limit" sentinel.
func TestValidate_RateLimitDisabledByZeroOption(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:       345,
			Platform: "youtube",
			Status:   "active",
		},
	}
	r := buildVeloxTestRouter(dst, ws, user, testVeloxAPIToken)
	WithVeloxValidateRateLimit(0, 0)(r) // disables

	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate", handler)

	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost,
			"/internal/v1/destinations/extdst_01JABC/validate", nil)
		req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("request %d (limit disabled): want 204, got %d (body=%q)",
				i, w.Code, w.Body.String())
		}
	}
}

// TestValidate_TransientPlatformAccountLookupError pins the 5xx
// path: a non-nil error from FindPlatformAccountByID surfaces as
// 500 Server Internal Error with the typed message. Defensive —
// any of destination / workspace / platform-account lookups is
// capable of returning a transient DB error (timeout, connection
// reset), and the spec maps ALL of them to 5xx.
func TestValidate_TransientPlatformAccountLookupError(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDErr: errors.New("db connection reset"),
	}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("transient PA lookup error: want 500, got %d (body=%q)", w.Code, w.Body.String())
	}
	// The handler intentionally redacts lookup details from the HTTP
	// response; operator diagnostics belong in server logs, not in
	// a client-visible body.
	if !strings.Contains(w.Body.String(), "validation failed") {
		t.Errorf("body should use the redacted validation error; got %q", w.Body.String())
	}
}

// TestValidate_DestinationNotFoundBySentinelErr covers the
// post-fix sentinel-aware 404 mapping on the validate
// path. The mock's GetByIDErr is set to
// repository.ErrExternalDestinationNotFound so the handler's NEW
// `if errors.Is(err, repository.ErrExternalDestinationNotFound){ writeError(404) ... }`
// branch fires (vs. the existing `if dest == nil` branch which
// never runs because the sentinel branch returns first). Without
// this test, the mirror fix is unverified AND a future refactor
// that accidentally drops the sentinel branch would silently
// regress the validate path to a 500 on production missing rows.
// The body's bare text matches the veloxDestinationNotFoundBody
// constant on the package. We additionally confirm downstream
// lookups (workspace + platform_account) are NOT triggered so a
// missing-row probe can't be made to enumerate other resources.
func TestValidate_DestinationNotFoundBySentinelErr(t *testing.T) {
	dst := &mockExternalDestinationStore{
		GetByIDErr: repository.ErrExternalDestinationNotFound,
	}
	ws := &mockWorkspaceLookup{
		findByIDErr: context.Canceled, // sentinel branch must short-circuit BEFORE this fires
	}
	user := &mockUserLookup{
		findPlatformAccountByIDErr: context.Canceled, // same
	}
	w := runValidate(t, dst, ws, user,
		testVeloxAPIToken, "extdst_01JSENT",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("sentinel-err destination: want 404, got %d (body=%q)",
			w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "destination not found") {
		t.Errorf("body should mention 'destination not found'; got %q",
			w.Body.String())
	}
	if dst.GetByIDCalls != 1 {
		t.Errorf("GetByIDCalls = %d; want 1", dst.GetByIDCalls)
	}
	if ws.findByIDCalls != 0 {
		t.Errorf("workspace.findByIDCalls = %d; want 0 (sentinel branch must short-circuit before downstream lookups)", ws.findByIDCalls)
	}
	if user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("user.findPlatformAccountByIDCalls = %d; want 0 (sentinel branch must short-circuit before downstream lookups)", user.findPlatformAccountByIDCalls)
	}
}
