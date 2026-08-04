package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------
// Existing v0 fixtures (preserved verbatim for backward compat).
// -----------------------------------------------------------------------

// fakeDeliveryStorage is the in-package ExternalDeliveryStore
// fake: exposes BOTH the Insert surface (POST handler) AND a
// GetByID method (the new GET handler) so the GET tests can
// seed rows directly. Production code uses
// *repository.ExternalDeliveryRepository which satisfies both
// surfaces structurally.
func TestHandleGetInternalDelivery_Happy_Accepted(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusAccepted, "", "", "", "", nil)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}

	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DeliveryID != "sdel_01JABC" {
		t.Errorf("DeliveryID = %q; want sdel_01JABC", got.DeliveryID)
	}
	if got.PublishStatus != "waiting_thumbnail" {
		t.Errorf("PublishStatus = %q; want waiting_thumbnail", got.PublishStatus)
	}
	if got.ThumbnailStatus != "pending" {
		t.Errorf("ThumbnailStatus = %q; want pending", got.ThumbnailStatus)
	}
	if got.LastErrorCode != "" || got.LastErrorMessage != "" {
		t.Errorf("LastError* = %q/%q; want empty for accepted row",
			got.LastErrorCode, got.LastErrorMessage)
	}
	body := w.Body.String()
	for _, legacy := range []string{"\"id\"", "\"status\"", "retry_wait_reason", "platform_media_id", "platform_url", "published_at"} {
		if strings.Contains(body, legacy) {
			t.Errorf("canonical response must not contain legacy field %q; body=%s", legacy, body)
		}
	}
}

// TestHandleGetInternalDelivery_Happy_RetryWait — populated row
// in retry_wait state; only canonical publish/thumbnail status fields
// and error diagnostics are exposed.
func TestHandleGetInternalDelivery_Happy_RetryWait(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusRetryWait,
		"auth_error", "401 invalid_grant from token endpoint", "", "", nil)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PublishStatus != "retry_wait" {
		t.Errorf("PublishStatus = %q; want retry_wait", got.PublishStatus)
	}
	if got.ThumbnailStatus != "pending" {
		t.Errorf("ThumbnailStatus = %q; want pending", got.ThumbnailStatus)
	}
	if got.LastErrorCode != "auth_error" {
		t.Errorf("LastErrorCode = %q; want auth_error", got.LastErrorCode)
	}
	if got.LastErrorMessage != "401 invalid_grant from token endpoint" {
		t.Errorf("LastErrorMessage = %q; want 401 message", got.LastErrorMessage)
	}
}

// TestHandleGetInternalDelivery_Happy_Published — terminal
// success state with platform IDs + completed_at stamped.
// published_at MUST be set; platform URLs must surface.
func TestHandleGetInternalDelivery_Happy_Published(t *testing.T) {
	completedAt := time.Date(2026, 7, 20, 18, 3, 21, 0, time.UTC)
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"", "",
		"dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		&completedAt)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PublishStatus != "published" {
		t.Errorf("PublishStatus = %q; want published", got.PublishStatus)
	}
	if got.ThumbnailStatus != "applied" {
		t.Errorf("ThumbnailStatus = %q; want applied", got.ThumbnailStatus)
	}
	if got.YouTubeVideoID != "dQw4w9WgXcQ" {
		t.Errorf("YouTubeVideoID = %q; want dQw4w9WgXcQ", got.YouTubeVideoID)
	}
	body := w.Body.String()
	for _, legacy := range []string{"\"id\"", "\"status\"", "retry_wait_reason", "platform_media_id", "platform_url", "published_at"} {
		if strings.Contains(body, legacy) {
			t.Errorf("canonical response must not contain legacy field %q; body=%s", legacy, body)
		}
	}
}

// TestHandleGetInternalDelivery_NotFound — unknown id collapses
// to 404. Body uses standard writeError envelope.
func TestHandleGetInternalDelivery_NotFound(t *testing.T) {
	store := newFakeDeliveryStorage()
	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_does_not_exist", "Bearer secret-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery not found") {
		t.Errorf("body should mention 'delivery not found'; got %s", w.Body.String())
	}
}

// TestHandleGetInternalDelivery_StoreUnconfigured — when the
// router was built WITHOUT WithExternalDeliveryStore, the
// route-guard in registerInternalVeloxRoutes refuses to mount
// the GET route. The chi mux then returns 404 on any request
// that hits the path. Matches the same collapse-with-not-found
// semantic the validate handler uses for disabled destinations.
func TestHandleGetInternalDelivery_StoreUnconfigured(t *testing.T) {
	r := newVeloxTestRouter(t, nil, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (route-guard suppresses mount when store nil); body=%s",
			w.Code, w.Body.String())
	}
}

// TestHandleGetInternalDelivery_LookupFailure — repo returns
// non-nil error → 500. Body uses standard writeError shape.
func TestHandleGetInternalDelivery_LookupFailure(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.lookupErr = errors.New("db connection reset")
	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery lookup failed") {
		t.Errorf("body should mention 'delivery lookup failed'; got %s", w.Body.String())
	}
}

// TestHandleGetInternalDelivery_AuthGated — the middleware
// returns 401 missing / 403 mismatch / 503 token-not-configured
// BEFORE the handler runs. Three assertions cover the spec.
func TestHandleGetInternalDelivery_AuthGated(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"", "", "x", "y", &time.Time{})

	// Sub-test 1: missing Authorization → 401.
	t.Run("missing_bearer", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "secret-token")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d; want 401", w.Code)
		}
	})

	// Sub-test 2: bearer mismatch → 403.
	t.Run("bearer_mismatch", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "secret-token")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer wrong-token")
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", w.Code)
		}
	})

	// Sub-test 3: empty token at boot → route-guard refuses to
	// mount the route (same reason as StoreUnconfigured) so chi
	// returns 404. The 503 path is only reachable when the route is
	// mounted manually without the guard (see runDeliver in
	// internal_velox_deliver_test.go). Production behaviour is what
	// this test covers — chi 404, NOT 503.
	t.Run("token_unconfigured", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer anything")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404 (route-guard suppresses mount when token empty)", w.Code)
		}
	})
}

// -----------------------------------------------------------------------
// Spec §8 tests (the new shape).
// -----------------------------------------------------------------------

// TestHandleGetInternalDelivery_Spec8_PublishStatus_MappingExhaustive
// pins the 11-value → 6-value mapping every (status, publish_status)
// pair. Exhaustive table so a future enum extension fails loudly.
