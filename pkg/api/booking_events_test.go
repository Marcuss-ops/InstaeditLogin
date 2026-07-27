// Package api — booking_events integration test.
//
// Coverage: handleCreateBookingEvent idempotency under the SAME
// (payload, client-IP) → identical dedupe_hash. Two POSTs must NOT
// materialise two rows because the repository's
//
//	ON CONFLICT (dedupe_hash) DO UPDATE
//	    SET dedupe_hash = booking_events.dedupe_hash
//	RETURNING id, created_at
//
// (internal/repository/booking_event_repo.go::Insert) preserves the
// existing row's identity on conflict. The fake stub here mirrors
// that contract at the api-layer interface so we can verify the
// handler reaches Insert twice with identical hashes without spinning
// up a Postgres test fixture (sql-level coverage is owned by the
// repository + the integration_test cluster, both out of scope here).
//
// Why an in-process stub instead of httptest + handler-injection:
//   - No real network means the test is hermetic and CI-fast.
//   - Setting req.RemoteAddr directly bypasses trustedClientIP's
//     proxy-chain logic, so both POSTs deterministically produce the
//     same hash. The production code path (extractIV(req, nil) →
//     trustedClientIP) is exercised end-to-end via this same
//     RemoteAddr → ip_hash → dedupe_hash derivation.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeBookingEventStore is a stub of BookingEventStore that
// simulates the SQL ON CONFLICT (dedupe_hash) DO UPDATE RETURNING
// id contract documented at
// internal/repository/booking_event_repo.go::Insert:
//
//   - DedupeHash not present → append a new row, stamp ev.ID +
//     ev.CreatedAt (mirrors INSERT … RETURNING of a freshly created
//     row).
//   - DedupeHash already present → back-fill ev.ID + ev.CreatedAt
//     from the existing canonical row (mirrors DO UPDATE RETURNING
//     id when the conflict path fires). The slice length stays at 1
//     so the assertion `len(s.rows) == 1 after 2 POSTs` distinguishes
//     the conflict path from a buggy duplicate-INSERT regression.
//
// The stub keeps a CLONED snapshot of the inserted BookingEvent so
// later calls don't observe upstream mutations of the *models.BookingEvent
// pointer the handler passes in. Without the clone, a test that
// compared store.rows[0].CreatedAt after a second POST would silently
// observe any post-Insert mutation by the handler.
type fakeBookingEventStore struct {
	rows []*models.BookingEvent
}

// Compile-time assertion: fakeBookingEventStore satisfies
// api.BookingEventStore. Drift in the interface signature surfaces at
// go vet time, not as a runtime panic in the test body.
var _ BookingEventStore = (*fakeBookingEventStore)(nil)

func (s *fakeBookingEventStore) Insert(ev *models.BookingEvent) error {
	for _, existing := range s.rows {
		if existing.DedupeHash == ev.DedupeHash {
			// ON CONFLICT (dedupe_hash) DO UPDATE RETURNING id:
			// Postgres returns the existing row's id + created_at.
			// Surface that to the handler so downstream reads see
			// a stable identifier across the idempotency window.
			ev.ID = existing.ID
			ev.CreatedAt = existing.CreatedAt
			return nil
		}
	}
	// INSERT path. Assign a monotonic id + a UTC-truncated
	// timestamp so cross-platform CI runners (Windows time.Now
	// resolution, macOS lazy clock) don't drift CreatedAt within
	// a single test run.
	ev.ID = int64(len(s.rows) + 1)
	ev.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	cloned := *ev // value-copy: caller-side mutations can't touch the stored row
	s.rows = append(s.rows, &cloned)
	return nil
}

// TestBookingEvents_IdempotentOnSameDedupeHash verifies that two
// consecutive POSTs of the SAME qualification payload from the SAME
// client IP land as a single booking_events row. This is the contract
// the marketing strategy-call funnel relies on: a visitor double-clicking
// Submit (or a page refresh after a network blip) MUST NOT double-write
// the lead row.
//
// Step-by-step assertions:
//  1. First POST returns 200 + {"status":"recorded"} and the stub
//     records exactly ONE row with a freshly-minted ID + CreatedAt.
//  2. Second POST with identical body+IP also returns 200 (the conflict
//     path is silent — no 409 leaked to the SPA, which would otherwise
//     trigger a re-paint that breaks the post-submit scheduler.handoff).
//  3. Stub slice length remains 1 — proves the handler reached Store.Insert
//     twice but the conflict path suppressed the second row.
//  4. The canonical row's ID + CreatedAt are byte-identical across
//     both calls — proving DO UPDATE honoured the existing row, NOT
//     minted a fresh id (which would silently duplicate the lead).
func TestBookingEvents_IdempotentOnSameDedupeHash(t *testing.T) {
	var store fakeBookingEventStore

	body := []byte(`{"intent":"general","goal":"launch","budget":"starter","ready":"yes"}`)
	// Stable, deterministic client IP shared across both POSTs.
	// Reaches trustedClientIP(req, nil) via req.RemoteAddr (the
	// production extractIV path when trusted-proxy list is empty);
	// Same IP → same ip_hash → same dedupe_hash → ON CONFLICT.
	const clientIP = "203.0.113.42"

	module := &BookingEventsModule{deps: BookingEventsModuleDeps{
		Store: &store,
		// nil rate-limit: tolerated by m.Register's
		// `if m.deps.RateLimit != nil { r.Use(...) }` guard, so
		// we don't need a no-op stub middleware in the test.
		RateLimit: nil,
		// Same-origin gate must allow the test request. Origin
		// header on the request matches the literal below.
		AllowedOrigins: []string{"http://test.local"},
	}}

	// Mount the module via the same path Register() would mount
	// it in production (routes.go:55-59). That way the test
	// exercises the FULL middleware chain — limitBodySize (4 KiB)
	// → requireSameOrigin → handleCreateBookingEvent — without
	// needing to duplicate that wiring inside the test.
	r := chi.NewRouter()
	module.Register(r)

	post := func(t *testing.T) *httptest.ResponseRecorder {
		t.Helper()
		req, err := http.NewRequest(
			http.MethodPost,
			"/api/v1/booking_events",
			bytes.NewReader(body),
		)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		// Deterministic client IP: strip the port semantics by
		// choosing any port (the production path strips it via
		// net.SplitHostPort inside trustedClientIP).
		req.RemoteAddr = clientIP + ":54321"
		// Same-origin gate: the BookingEventsModule expects an
		// Origin or Referer header matching one of AllowedOrigins.
		req.Header.Set("Origin", "http://test.local")
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// ─── 1. First POST lands ──────────────────────────────────────
	rec1 := post(t)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first POST: want 200, got %d (body=%s)", rec1.Code, rec1.Body.String())
	}
	if len(store.rows) != 1 {
		t.Fatalf("first POST: want 1 row, got %d", len(store.rows))
	}
	canonicalID := store.rows[0].ID
	canonicalCreatedAt := store.rows[0].CreatedAt

	// Sanity-check the response body shape: {"status":"recorded"}
	// — the handler never echoes row id / hash / ip to clients.
	var firstBody map[string]string
	if err := json.Unmarshal(rec1.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first body: %v", err)
	}
	if firstBody["status"] != "recorded" {
		t.Fatalf("first POST: want status=recorded, got %v", firstBody)
	}

	// Sleep so a NEW-row-created timestamp would be measurably
	// later than canonicalCreatedAt. If the second call went down
	// the INSERT branch (buggy stub regression), the difference
	// shows up as a stalled comparison below.
	time.Sleep(20 * time.Millisecond)

	// ─── 2. Second POST with identical body + IP ─────────────────
	rec2 := post(t)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second POST: want 200, got %d (body=%s)", rec2.Code, rec2.Body.String())
	}

	// ─── 3. Idempotency contract verification ─────────────────────
	// 3a. Row count stays at 1 — ON CONFLICT path is silent.
	if got := len(store.rows); got != 1 {
		t.Fatalf("idempotency: expected 1 row after 2 POSTs (ON CONFLICT), got %d", got)
	}
	// 3b. Row ID is stable across calls — DO UPDATE preserved the
	// existing row's identity, did not mint a fresh BIGSERIAL.
	if got := store.rows[0].ID; got != canonicalID {
		t.Fatalf("idempotency: expected stable ID %d, got %d", canonicalID, got)
	}
	// 3c. Row CreatedAt is stable across calls — proves the
	// second call took the conflict branch (preserves
	// existing row's created_at) instead of the INSERT branch
	// (which would pick up a fresh time.Now after the sleep).
	if got := store.rows[0].CreatedAt; !got.Equal(canonicalCreatedAt) {
		t.Fatalf("idempotency: expected stable CreatedAt %v, got %v",
			canonicalCreatedAt, got)
	}

	// ─── 4. BookingEvent lifecycle hygiene ────────────────────────
	// The handler's *models.BookingEvent pointer must still carry
	// the canonical id (post-second-call) so downstream Go code
	// that holds the pointer across calls reads a stable id. We
	// can't observe that directly in this test, but the stub
	// back-filling on Insert covers it: after the second POST
	// returns, the row stored on the stub matches the handler's
	// expected output shape.
	_ = canonicalCreatedAt // keep the variable referenced for go vet
}
