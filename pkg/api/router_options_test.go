package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestWithGroupStore_RouteMounting is a regression test that locks
// down the conditional mount of /api/v1/groups on the WithGroupStore
// router option.
//
// The contract the test pins:
//
//   - WithGroupStore absent        → chi has no route at /api/v1/groups
//                                    → 404 "page not found"
//   - WithGroupStore(some impl)    → chi mounts the auth-protected
//                                    stack at /api/v1/groups
//                                    → any response other than 404
//                                    (typically 401 without a session,
//                                    or 200/400 once a JWT is wired)
//
// Why this matters: AuthModule.Register in pkg/api/modules.go gates
// the /api/v1/groups route on `if m.deps.GroupStore != nil`. A future
// refactor that moves the gate, deletes the WithGroupStore wire-up in
// internal/bootstrap/app.go, or accidentally adds an AND-clause
// (e.g. `&& someOtherStore != nil`) would silently disable the
// groups endpoint. Without this test the regression only shows up at
// the SPA layer as "Impossibile caricare i gruppi. Riprova più
// tardi." — the exact failure that took two hours to triage the day
// the wire-up was forgotten.
//
// The mock GroupStore is reused from groups_handlers_test.go
// (mockGroupStore is declared in that file; it is accessible from
// here because both files are in `package api`).
func TestWithGroupStore_RouteMounting(t *testing.T) {
	t.Parallel()

	t.Run("without_WithGroupStore_returns_404", func(t *testing.T) {
		t.Parallel()
		// mustNewRouterWithDefaults supplies every required dep
		// (vault, authorizer, oneTimeCodes, idempotencyStore,
		// connectLinkNonceStore). Notably absent: WithGroupStore.
		// AuthModule.Register will then short-circuit on the
		// `if m.deps.GroupStore != nil` branch and not register
		// the GET /api/v1/groups handler at all.
		r := mustNewRouterWithDefaults(
			services.NewCapabilityRouter(),
			&mockUserStore{},
			auth.NewManager(testJWTSecret, 24),
			"https://app.instaedit.org",
			nil,
		)
		rr := httptest.NewRecorder()
		r.Setup().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET /api/v1/groups without WithGroupStore: want 404, got %d (body=%q)", rr.Code, rr.Body.String())
		}
	})

	t.Run("with_WithGroupStore_route_is_mounted", func(t *testing.T) {
		t.Parallel()
		// mockGroupStore is a no-op GroupStore from
		// groups_handlers_test.go. The handler does NOT call into
		// it on this probe because the auth middleware
		// short-circuits with 401 first (no JWT), but the fixture
		// is still required so the interface assertion in
		// WithGroupStore(GroupStore) is satisfied.
		gStore := &mockGroupStore{}
		r := mustNewRouterWithDefaults(
			services.NewCapabilityRouter(),
			&mockUserStore{},
			auth.NewManager(testJWTSecret, 24),
			"https://app.instaedit.org",
			nil,
			WithGroupStore(gStore),
		)
		rr := httptest.NewRecorder()
		r.Setup().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil))
		// Anything != 404 means the route is mounted. In practice
		// the auth middleware returns 401 (no Authorization
		// header). We assert the inverse — 404 means the route is
		// NOT mounted, which is the regression we are guarding.
		if rr.Code == http.StatusNotFound {
			t.Errorf("GET /api/v1/groups with WithGroupStore: route must be mounted (response != 404), got 404 (body=%q)", rr.Body.String())
		}
	})
}
