package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// testVeloxAPIToken is a fixed string for httptest. Production
// tokens are 32-char random hex from a 16-byte secret; the test
// value uses printable ASCII so failure messages are easy to
// eyeball. The exact length doesn't matter — subtle.ConstantTimeCompare
// returns 0 on length-mismatch (short-circuit) so 401 is
// guaranteed for any wrong token.
func TestValidate_MissingAuthHeader(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body=%q)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: want application/json (writeError path), got %s", got)
	}
	if dst.GetByIDCalls != 0 {
		t.Errorf("destination store must NOT be called when auth fails; got %d calls", dst.GetByIDCalls)
	}
}

// TestValidate_MalformedAuthHeader verifies the prefix check:
// "Token <value>", "Basic ...", etc. all return 401.
func TestValidate_MalformedAuthHeader(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	for _, bad := range []string{
		"Token abc",
		"Basic dXNlcjpwYXNz",
		"", // empty
	} {
		w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
			testVeloxAPIToken, "extdst_01JABC", bad, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("malformed header %q: want 401, got %d", bad, w.Code)
		}
	}
}

// TestValidate_PrefixOnly verifies a header that has only "Bearer "
// (no value after) returns 401 — the length check
// `len(authHeader) <= len(prefix)` catches it before the
// strings.EqualFold call.
func TestValidate_PrefixOnly(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC", "Bearer", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bare Bearer prefix: want 401, got %d", w.Code)
	}
}

// TestValidate_WrongToken verifies the constant-time token
// mismatch path returns 403 (peer DID authenticate — wrong
// credential — rather than 401 "you need to authenticate")
// AND the destination store counter stays at zero
// (timing-leak defense).
func TestValidate_WrongToken(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer wrong-token-32-chars-aaaaaa", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	if dst.GetByIDCalls != 0 {
		t.Errorf("destination store must NOT be called when token mismatches; got %d calls", dst.GetByIDCalls)
	}
}

// TestValidate_TokenMismatchSameLength closes an unlikely but
// possible read of subtle.ConstantTimeCompare: same length +
// wrong content. The compare returns 0 → 403. Verifies the
// happy-length-mismatch path uses the constant-time compare
// (vs. a naive bytewise compare that would leak per-byte
// equality on first match).
func TestValidate_TokenMismatchSameLength(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	// Construct a same-length wrong token (substitute last char).
	wrong := testVeloxAPIToken[:len(testVeloxAPIToken)-1] + "X"
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+wrong, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("same-length wrong token: want 403, got %d", w.Code)
	}
}

// TestValidate_DestinationNotFound pins the (nil, nil) path:
// GetByID returns nil dest, handler returns 404 and does NOT
// query the workspace or platform_account (early-exit branch).
func TestValidate_DestinationNotFound(t *testing.T) {
	dst := &mockExternalDestinationStore{} // GetByIDResult is nil by default
	ws := &mockWorkspaceLookup{}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JDEF",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
	if ws.findByIDCalls != 0 {
		t.Errorf("workspace must NOT be queried after destination not found; got %d calls", ws.findByIDCalls)
	}
	if user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("platform_account must NOT be queried after destination not found; got %d calls",
			user.findPlatformAccountByIDCalls)
	}
}

// TestValidate_DestinationDisabled pins the disabled = missing
// policy: enabled=false returns 404 (uniform with not-found so
// existing-vs-non-existing isn't an oracle).
func TestValidate_DestinationDisabled(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           false,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Errorf("disabled destination: want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
	if ws.findByIDCalls != 0 || user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("downstream lookups must NOT fire when destination is disabled")
	}
}

// TestValidate_HappyPathNoDiagnostic verifies the 204 No Content
// response when destination + workspace + platform_account all
// line up and no diagnostic mode is requested.
func TestValidate_HappyPathNoDiagnostic(t *testing.T) {
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
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1, Name: "ws-1"},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:       345,
			Platform: "youtube",
			Status:   "active",
		},
	}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("happy path: want 204, got %d (body=%q)", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("happy path 204: body MUST be empty, got %q", w.Body.String())
	}
	if dst.GetByIDCalls != 1 {
		t.Errorf("destination lookup: want 1, got %d", dst.GetByIDCalls)
	}
	if ws.findByIDCalls != 1 {
		t.Errorf("workspace lookup: want 1, got %d", ws.findByIDCalls)
	}
	if user.findPlatformAccountByIDCalls != 1 {
		t.Errorf("platform_account lookup: want 1, got %d", user.findPlatformAccountByIDCalls)
	}
}

// TestValidate_ReauthRequired pins the dual-signal reauth
// check: EITHER status='reauth_required' OR
// reauth_required_at != nil must return 404.
func TestValidate_ReauthRequired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		pa   *models.PlatformAccount
	}{
		{
			name: "status enum is reauth_required",
			pa: &models.PlatformAccount{
				ID:       345,
				Platform: "youtube",
				Status:   "reauth_required",
			},
		},
		{
			name: "reauth_required_at timestamp is non-nil",
			pa: &models.PlatformAccount{
				ID:               345,
				Platform:         "youtube",
				Status:           "active",
				ReauthRequiredAt: &now,
			},
		},
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
				findPlatformAccountByIDResult: tc.pa,
			}
			w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
				"Bearer "+testVeloxAPIToken, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("reauth: want 404, got %d (body=%q)", w.Code, w.Body.String())
			}
		})
	}
}

// TestValidate_DiagnosticQueryParam verifies the ?diagnostic=true
// trigger returns 200 with the diagnostic JSON body. The shape
// must match VeloxValidateDestinationResponse.
