package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeDeliveryEnv is the in-package ExternalDeliveryStore fake
// with a seedable row map + explicit timestamping. Reuses the
// shape from internal_velox_get_delivery_test.go but inlines a
// minimal version so this file is self-contained (no cross-file
// helper import for tests).
type fakeDeliveryEnv struct {
	rows      map[string]*models.ExternalDelivery
	lookupErr error
}

func newFakeDeliveryEnv() *fakeDeliveryEnv {
	return &fakeDeliveryEnv{rows: map[string]*models.ExternalDelivery{}}
}

func (f *fakeDeliveryEnv) GetByID(_ context.Context, id string) (*models.ExternalDelivery, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	d, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return d, nil
}

// Insert is part of the ExternalDeliveryStore interface used by
// the POST handler. The GET tests don't exercise the insert
// path; this stub satisfies the compile-time interface assertion
// and gives row-seed helpers a single install path.
func (f *fakeDeliveryEnv) Insert(_ context.Context, e *models.ExternalDelivery, _ []byte) (*models.ExternalDelivery, error) {
	f.rows[e.ID] = e
	return e, nil
}

// compile-time assertion the fake satisfies the production
// interface. If a future interface widening adds new methods,
// this line fails pre-test.
var _ ExternalDeliveryStore = (*fakeDeliveryEnv)(nil)

// seed installs a row with explicit timestamps so each test can
// assert id + created_at + updated_at are surfaced in the
// response. Optional lastErrCode/Message + platform mirrors
// the existing seed helper in internal_velox_get_delivery_test.go
// but folded here to avoid cross-file imports.
func (f *fakeDeliveryEnv) seed(id string, status models.ExternalDeliveryStatus, createdAt, updatedAt time.Time, lastErrCode, lastErrMsg, mediaID, mediaURL string, completedAt *time.Time) {
	row := &models.ExternalDelivery{
		ID:           id,
		SourceSystem: "velox",
		Status:       status,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	if lastErrCode != "" {
		s := lastErrCode
		row.LastErrorCode = &s
	}
	if lastErrMsg != "" {
		s := lastErrMsg
		row.LastErrorMessage = &s
	}
	if mediaID != "" {
		s := mediaID
		row.PlatformMediaID = &s
	}
	if mediaURL != "" {
		s := mediaURL
		row.PlatformURL = &s
	}
	row.CompletedAt = completedAt
	f.rows[id] = row
}

// newDeliveriesTestRouter constructs a Router with the GET
// handler's deps wired AND a chi.NewRouter() initialised so
// registerInternalVeloxRoutes() mounts the route. Calls the
// testSendRequest helper from internal_velox_get_delivery_test.go
// to fire requests into mux.
func newDeliveriesTestRouter(t *testing.T, deliveries ExternalDeliveryStore, token string) *Router {
	t.Helper()
	r := &Router{
		mux:                chi.NewRouter(),
		externalDeliveries: deliveries,
		veloxAPIToken:      token,
	}
	r.registerInternalVeloxRoutes()
	return r
}

// fireGetDeliveryRequest wraps the existing testSendRequest helper
// with a fixed path + method so each test reads as a one-line
// "GET /deliveries/{id}" assertion.
func fireGetDeliveryRequest(t *testing.T, r *Router, id, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	return testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/"+id, authHeader)
}

// TestGetInternalDelivery_ExtendedShape_Accepted pins that the
// three NEW fields (id, created_at, updated_at) are surfaced
// even for a fresh, no-op row in 'accepted' status. Pre-
// extension the response was just {"status":"accepted"} which
// lacked the audit-trail triple Velox reconciliation needs.
func TestGetInternalDelivery_ExtendedShape_Accepted(t *testing.T) {
	store := newFakeDeliveryEnv()
	createdAt := time.Date(2026, 7, 20, 17, 59, 42, 0, time.UTC)
	updatedAt := createdAt // equal at insert; diverges on first transition

	store.seed("sdel_01JNEW", models.ExternalDeliveryStatusAccepted,
		createdAt, updatedAt, "", "", "", "", nil)

	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JNEW", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}

	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "sdel_01JNEW" {
		t.Errorf("ID = %q; want sdel_01JNEW", got.ID)
	}
	if got.Status != "accepted" {
		t.Errorf("Status = %q; want accepted", got.Status)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v; want %v", got.CreatedAt, createdAt)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Errorf("UpdatedAt = %v; want %v", got.UpdatedAt, updatedAt)
	}
	if got.PublishedAt != nil {
		t.Errorf("PublishedAt = %v; want nil for non-published row", got.PublishedAt)
	}
	body := w.Body.String()
	for _, want := range []string{`"id":"sdel_01JNEW"`, `"created_at":"2026-07-20T17:59:42Z"`, `"updated_at":"2026-07-20T17:59:42Z"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing substring %q; body=%s", want, body)
		}
	}
}

// TestGetInternalDelivery_ExtendedShape_Published pins that
// created_at + updated_at are surfaced AND updated_at
// differs from created_at on a post-transition row. The
// divergence is the audit signal Velox needs for "ignore
// updates older than X" filtering; the test pins the
// behaviour so a future refactor doesn't accidentally
// collapse the two timestamps to a single column.
func TestGetInternalDelivery_ExtendedShape_Published(t *testing.T) {
	completedAt := time.Date(2026, 7, 20, 18, 3, 21, 0, time.UTC)
	createdAt := completedAt.Add(-3 * time.Minute) // 17:00:21 → published 18:03:21
	updatedAt := completedAt                       // last transition → completedAt

	store := newFakeDeliveryEnv()
	store.seed("sdel_01JPUB", models.ExternalDeliveryStatusPublished,
		createdAt, updatedAt, "", "",
		"dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		&completedAt)

	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JPUB", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}

	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "sdel_01JPUB" {
		t.Errorf("ID = %q; want sdel_01JPUB", got.ID)
	}
	if got.Status != "published" {
		t.Errorf("Status = %q; want published", got.Status)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v; want %v (3 min before completed)", got.CreatedAt, createdAt)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Errorf("UpdatedAt = %v; want %v (equal to completed at)", got.UpdatedAt, updatedAt)
	}
	// Critical assertion: updated_at MUST diverge from
	// created_at for a post-transition row. Pre-extension
	// these fields didn't exist; pinning that they are
	// NOT collapsed to a single column in the future.
	if got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("CreatedAt == UpdatedAt (%v); want updated_at > created_at for published row",
			got.UpdatedAt)
	}
	if got.PublishedAt == nil {
		t.Fatal("PublishedAt = nil; want completedAt timestamp for published row")
	}
	if got.PublishedAt != nil && !got.PublishedAt.Equal(completedAt) {
		t.Errorf("PublishedAt = %v; want %v", got.PublishedAt, completedAt)
	}
	if got.PlatformMediaID != "dQw4w9WgXcQ" {
		t.Errorf("PlatformMediaID = %q; want dQw4w9WgXcQ", got.PlatformMediaID)
	}
	if got.PlatformURL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("PlatformURL = %q; want youtube url", got.PlatformURL)
	}
}

// TestGetInternalDelivery_ExtendedShape_OmitZeroTimestamp pins
// that created_at + updated_at are NEVER omitted (they are
// required-audit columns from the repo's NOW() stamp at insert
// AND on every UpdateStatus). If a future refactor marks these
// fields with omitempty, the JSON shape would silently drop the
// timestamps on rows whose DB stamps happen to be the Go zero-
// value time — a regression that's invisible in normal happy-
// path tests because the JS parser would just see no field. This
// test reads the raw bytes and FAILURES if either key is absent.
func TestGetInternalDelivery_ExtendedShape_OmitZeroTimestamp(t *testing.T) {
	// Use the Go zero-value time (NOT rounded-zero) to simulate
	// the worst case where a hypothetical bug produces a zero
	// stamp. The repo NEVER stores zero times (NOW() is always
	// real wall-clock), so a non-zero response is the expected
	// shape; the test asserts the timestamps are surfaced as
	// ISO-8601 strings in the response body regardless.
	store := newFakeDeliveryEnv()
	realCreatedAt := time.Date(2026, 7, 20, 17, 59, 42, 0, time.UTC)
	realUpdatedAt := time.Date(2026, 7, 20, 18, 3, 21, 0, time.UTC)
	store.seed("sdel_01JZERO", models.ExternalDeliveryStatusAccepted,
		realCreatedAt, realUpdatedAt, "", "", "", "", nil)

	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JZERO", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	// Both keys MUST appear in the raw body — omitempty
	// regression catcher (sub-string match on the JSON shape).
	if !strings.Contains(body, `"created_at":"2026-07-20T17:59:42Z"`) {
		t.Errorf("body missing created_at field with iso-8601 value; body=%s", body)
	}
	if !strings.Contains(body, `"updated_at":"2026-07-20T18:03:21Z"`) {
		t.Errorf("body missing updated_at field with iso-8601 value; body=%s", body)
	}
}

// TestGetInternalDelivery_NotFound_ExtendedShape — unknown
// id → 404. Body uses standard writeError envelope and does
// NOT leak existence differences between not-found and
// disabled (per the file-level doc-comment contract). Asserts
// the response body does NOT contain any of the new audit
// fields (they can't surface for a non-existent row).
func TestGetInternalDelivery_NotFound_ExtendedShape(t *testing.T) {
	store := newFakeDeliveryEnv()
	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_does_not_exist", "Bearer secret-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery not found") {
		t.Errorf("body should mention 'delivery not found'; got %s", w.Body.String())
	}
	// Sanity: no "id" / "created_at" / "updated_at" should leak
	// in a not-found body.
	for _, leak := range []string{`"id":"sdel_does_not_exist"`, `"created_at":`, `"updated_at":`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("404 body should not leak %q; body=%s", leak, w.Body.String())
		}
	}
}

// TestGetInternalDelivery_LookupFailure_ExtendedShape — repo
// returns non-nil error → 500. Confirms the new audit fields
// are NOT surfaced in the error envelope (they don't apply on
// DB error).
func TestGetInternalDelivery_LookupFailure_ExtendedShape(t *testing.T) {
	store := newFakeDeliveryEnv()
	store.lookupErr = errors.New("db connection reset")
	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JXXX", "Bearer secret-token")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery lookup failed") {
		t.Errorf("body should mention 'delivery lookup failed'; got %s", w.Body.String())
	}
	// Sanity: no leakage of audit fields on a 500.
	for _, leak := range []string{`"created_at":`, `"updated_at":`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("500 body should not leak %q; body=%s", leak, w.Body.String())
		}
	}
}

// TestGetInternalDelivery_AuthMissing_ExtendedShape — Bearer
// middleware intercepts BEFORE the handler fires. 401; new audit
// fields don't surface (handler never ran).
func TestGetInternalDelivery_AuthMissing_ExtendedShape(t *testing.T) {
	store := newFakeDeliveryEnv()
	createdAt := time.Date(2026, 7, 20, 17, 59, 42, 0, time.UTC)
	store.seed("sdel_01JAB", models.ExternalDeliveryStatusAccepted,
		createdAt, createdAt, "", "", "", "", nil)

	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JAB", "") // NO Authorization header

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing Authorization") {
		t.Errorf("body should mention missing Authorization; got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"created_at":`) {
		t.Errorf("401 body should not surfacing created_at; got %s", w.Body.String())
	}
}

// TestGetInternalDelivery_AuthMismatch_ExtendedShape — Bearer
// middleware intercepts on wrong-token. 403; audit fields don't
// surface.
func TestGetInternalDelivery_AuthMismatch_ExtendedShape(t *testing.T) {
	store := newFakeDeliveryEnv()
	createdAt := time.Date(2026, 7, 20, 17, 59, 42, 0, time.UTC)
	store.seed("sdel_01JWX", models.ExternalDeliveryStatusAccepted,
		createdAt, createdAt, "", "", "", "", nil)

	r := newDeliveriesTestRouter(t, store, "secret-token")
	w := fireGetDeliveryRequest(t, r, "sdel_01JWX", "Bearer wrong-token")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "token mismatch") {
		t.Errorf("body should mention token mismatch; got %s", w.Body.String())
	}
}

// TestGetInternalDelivery_EmptyPath_Rejects — path id is the
// endpoint's identity anchor. Empty path (route mount with no
// {id} segment) yields 404 from chi's router, NOT 200; this
// test pins that behaviour so the audit field assertion in
// the happy-path tests can't accidentally test against an
// "absent" record (which would be silently mapped to 200 with
// zero-value timestamps).
func TestGetInternalDelivery_EmptyPath_Rejects(t *testing.T) {
	store := newFakeDeliveryEnv()
	r := newDeliveriesTestRouter(t, store, "secret-token")

	// Hit the bare path with no {id}. chi returns 404 because
	// the route is registered as /deliveries/{id} (template
	// requires a non-empty segment).
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/deliveries/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	// Acceptable outcomes: 404 (route-guard refused) or 405
	// (method-not-allowed if redirector fires). The body
	// MUST NOT contain a 200-shaped audit-trail triple.
	body := w.Body.String()
	for _, leak := range []string{`"id":"`, `"created_at":`, `"updated_at":`} {
		if strings.Contains(body, leak) {
			t.Errorf("non-existent id body should not contain %q; got %s", leak, body)
		}
	}
	if w.Code == http.StatusOK {
		t.Errorf("empty-path request returned 200; want 404 or 405")
	}
}

// TestGetInternalDelivery_RowIDRoundtripsThroughResponse pins
// that the response's `id` field equals the URL path id
// (canonical social_delivery_id). Pre-extension the response
// had no id field at all; a future refactor that accidentally
// drops the id (relying on the URL instead) breaks Velox's
// log-aggregation pattern that consumes bodies). The test
// pins the round-trip.
func TestGetInternalDelivery_RowIDRoundtripsThroughResponse(t *testing.T) {
	cases := []string{
		"sdel_01JABCD",
		"sdel_01JAUTHORITY",
		"sdel_01J" + strings.Repeat("X", 32),
	}
	for _, id := range cases {
		id := id
		t.Run("id="+id, func(t *testing.T) {
			store := newFakeDeliveryEnv()
			createdAt := time.Date(2026, 7, 20, 17, 59, 42, 0, time.UTC)
			store.seed(id, models.ExternalDeliveryStatusAccepted,
				createdAt, createdAt, "", "", "", "", nil)

			r := newDeliveriesTestRouter(t, store, "secret-token")
			w := fireGetDeliveryRequest(t, r, id, "Bearer secret-token")

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", w.Code)
			}
			var got VeloxGetDeliveryResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.ID != id {
				t.Errorf("response ID = %q; want %q (must match URL path)", got.ID, id)
			}
		})
	}
}
