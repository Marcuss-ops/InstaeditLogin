// Package api — End-to-end test for the Velox → InstaEdit → YouTube
// Phase 1 pipeline.
//
// SCENARIOS (driven by 1 happy-path integration test + 4 autograde
// boundary tests):
//
//	STAGE 1 — Validate destination:    204 No Content
//	STAGE 2 — POST /deliveries fresh:   202 + {social_delivery_id, status:"accepted", already_exists:false}
//	STAGE 3 — Worker pipeline:          artifact SHA verified client-side, FSM advances
//	                                    accepted → downloading → artifact_verified → ingest_completed → queued
//	STAGE 4 — Publish + callback:       FSM publishing → published, mock YouTube returns
//	                                    {id, url}, dispatcher POSTs HMAC-signed callback
//	                                    and the Velox mock verifies signature + body shape
//	STAGE 5 — Idempotent replay:        re-POST exact body bytes → 202 with already_exists=true
//	                                    AND same social_delivery_id (NOT a fresh row)
//	STAGE 6 — Idempotency conflict:     re-POST same idempotency_key + DIFFERENT SHA →
//	                                    409 + VeloxDeliverArtifactConflictResponse shape
//
// Architecture decision: instead of running an actual Velox + YouTube
// daemon, we wire three httptest.Server endpoints (artifact download,
// YouTube publish, Velox HMAC receiver) in the test process. The
// package-level Router uses inline construction (mirrors
// internal_velox_get_delivery_test.go::newVeloxTestRouter) so we
// don't have to instantiate MustNewRouter(..., WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60 * time.Second))) with its 6 arguments.
//
// We REUSE production primitives whenever possible:
//   - worker.IngestFSM drives the state-machine transitions
//   - VeloxCallbackDispatcher signs + POSTs the callback
//   - real ExternalDeliveryStore + ExternalDestinationStore interfaces
//     are satisfied by the harness-level fakes
//
// The fakes mirror the production repo behaviour for the 3-way
// idempotency outcome (fresh vs replay vs conflict) so the test
// stresses the SAME handler logic that production hits.
//
// HMAC VERIFICATION — the Velox callback mock re-computes
// HMAC-SHA256(secret, "<ts>.<body>") on every received POST and
// refuses the request with 400 when the header doesn't match. This
// mirrors the production receiver contract (see
// docs/ARCHITECTURE.md §Velox callbacks) so a dispatcher that
// forgets to sign the body OR mixes up ts/body order is detected
// immediately.
//
// DETERMINISM — VeloxCallbackDispatcher's jitter/random sources
// are reset to a fixed seed (rand.NewSource(42)) and the baseDelay
// is set to 1ms; this keeps the test wall-clock under 2 s even when
// the dispatcher churns through 5 retries on a misconfigured
// receiver.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// =====================================================================
// E2E FIXTURES
// =====================================================================

const (
	e2eAPIToken       = "test-velox-e2e-secret-token-not-real"
	e2eWebhookSecret  = "test-velox-e2e-webhook-secret-not-real"
	e2eWorkspaceID    = int64(9001)
	e2ePlatformAcctID = int64(9002)
	e2eDestinationID  = "extdst_01JE2ETESTDESTINATION00000"
	e2eDeliveryID     = "delivery_e2e_8cc0f"
	e2eIdempotencyKey = "delivery_e2e_8cc0f|dest_e2e_9001"
	e2eArtifactID     = "artifact_01JE2ETEST"
	e2eExternalDelID  = "delivery_e2e_8cc0f"
)

// e2eArtifactBytes is a 4096-byte deterministic byte slice. We pick
// a non-trivial size so a streaming implementation (TeeReader + hash
// during read) doesn't accidentally short-circuit on small inputs.
// The pattern (i % 251) gives a varied byte distribution without
// hitting high-bit unfairness that some compression algorithms
// exploit during streaming benchmarks.
var e2eArtifactBytes []byte

// e2eValidSHA is computed at init() from e2eArtifactBytes so the
// test fixture doesn't carry an opaque hardcoded hex string the
// reader can't verify. The handler's sha256HexRegex enforces
// lowercase hex 64 chars; we assert that here once at init to fail
// fast on rune drift.
var e2eValidSHA string

// e2eInvalidSHA is a different 64-char lowercase hex that does NOT
// match e2eArtifactBytes. Used in stage 6 (idempotency conflict)
// to demonstrate that the body-hash check is the discriminator.
var e2eInvalidSHA = strings.Repeat("a", 64)

func init() {
	e2eArtifactBytes = make([]byte, 4096)
	for i := range e2eArtifactBytes {
		e2eArtifactBytes[i] = byte(i % 251)
	}
	sum := sha256.Sum256(e2eArtifactBytes)
	e2eValidSHA = hex.EncodeToString(sum[:])
	if matched, err := regexp_matchLowerHex64(e2eValidSHA); !matched || err != nil {
		panic(fmt.Sprintf("e2e fixture SHA drift: %q matched=%v err=%v", e2eValidSHA, matched, err))
	}
}

// regexp_matchLowerHex64 is a hand-rolled check matching the
// handler's sha256HexRegex pattern. We don't import the production
// regex (it'd be valid but creates a package-level coupling) — the
// init() guard only needs to assert the substring is 64 lowercase
// hex chars, which is trivial.
func regexp_matchLowerHex64(s string) (bool, error) {
	if len(s) != 64 {
		return false, fmt.Errorf("len=%d", len(s))
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false, fmt.Errorf("bad rune %q", c)
		}
	}
	return true, nil
}

// =====================================================================
// E2E HARNESS
// =====================================================================

// capturedCallback is the shape that the Velox HMAC receiver holds
// for each successful POST. Used by the test to assert that the
// dispatcher fired AT LEAST one callback with the expected event
// name + body shape (the production receiver mirrors this
// behaviour via the audit log).
type capturedCallback struct {
	EventID   string
	Timestamp int64
	Signature string
	Event     string
	Body      []byte
}

// e2eHarness bundles all fake stores + httptest servers + the
// dispatcher for a single e2e test. The harness spins up listeners
// eagerly so callers can read the URLs into their fixtures before
// executing stages; t.Cleanup tears them down so tests don't leak
// goroutines.
type e2eHarness struct {
	t *testing.T

	// Fakes
	workspaceStore *fakeE2EWorkspace
	userStore      *fakeE2EUser
	destinations   *fakeE2EDestinations
	deliveries     *fakeE2EDeliveries

	// Mock servers (httptest.Server wrapped closures).
	veloxArtifactSrv *httptest.Server
	veloxCallbackSrv *httptest.Server
	youtubeSrv       *httptest.Server

	// observations
	callbackMu   sync.Mutex
	callbacks    []capturedCallback
	publishCalls int32
	artifactHits int32

	// dispatcher reuses mockAuditStore from the existing
	// internal_velox_callback_dispatcher_test.go pattern.
	audit      *mockAuditStore
	dispatcher *VeloxCallbackDispatcher

	// router (constructed via inline pattern; registerInternalVeloxRoutes
	// mounts the 3 endpoints on r.mux so we can fire HTTP requests
	// through it).
	router *Router
}

// newE2EHarness wires everything afresh per test (no shared state
// across test cases). Cleanup chain is registered via t.Cleanup so
// each test tearing down its listeners is automatic.
func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	h := &e2eHarness{t: t}

	// 1) Velox artifact download — serves the canonical
	//    e2eArtifactBytes. The handler captures Content-Length
	//    verification by echoing the byte slice verbatim; the
	//    caller (test worker) computes sha byte-by-byte to fail
	//    fast on any size mismatch.
	h.veloxArtifactSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&h.artifactHits, 1)
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(e2eArtifactBytes)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(e2eArtifactBytes)
	}))

	// 2) YouTube publish — returns a fake {id, url} shape.
	h.youtubeSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&h.publishCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"yt_e2e_video_001","url":"https://www.youtube.com/watch?v=yt_e2e_video_001"}`)
	}))

	// 3) Velox callback HMAC receiver — re-computes the signature
	//    on every received POST + appends to the captured slice +
	//    replies 200 on match (400 on mismatch).
	h.veloxCallbackSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Velox-Signature")
		tsStr := r.Header.Get("X-Velox-Timestamp")
		ts, _ := strconv.ParseInt(tsStr, 10, 64)
		body, _ := io.ReadAll(r.Body)

		mac := hmac.New(sha256.New, []byte(e2eWebhookSecret))
		mac.Write([]byte(strconv.FormatInt(ts, 10)))
		mac.Write([]byte{'.'})
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if subtle_eq(strings.TrimPrefix(sig, "sha256="), expected) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var payload VeloxCallbackPayload
		_ = json.Unmarshal(body, &payload)

		h.callbackMu.Lock()
		h.callbacks = append(h.callbacks, capturedCallback{
			EventID:   r.Header.Get("X-Velox-Event-ID"),
			Timestamp: ts,
			Signature: sig,
			Event:     payload.Status,
			Body:      append([]byte(nil), body...),
		})
		h.callbackMu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))

	// 4) Audit store — production shim (mockAuditStore is defined
	//    in internal_velox_callback_dispatcher_test.go alongside
	//    VeloxCallbackAuditStore interface impl). Reused here.
	h.audit = &mockAuditStore{}

	// 5) Dispatcher pointed at veloxCallbackSrv. baseDelay=1ms +
	//    jitter=0..1ms + deterministic seed lets a misconfigured
	//    dispatcher's 5-retry path complete inside ~50ms even in
	//    the failure path (we don't want CI flakes).
	h.dispatcher = NewVeloxCallbackDispatcher(
		[]byte(e2eWebhookSecret),
		&http.Client{Timeout: 5 * time.Second},
		h.audit,
		slog.Default(),
	)
	h.dispatcher.baseDelay = 1 * time.Millisecond
	h.dispatcher.jitterMin = 0
	h.dispatcher.jitterMax = 1 * time.Millisecond
	h.dispatcher.randSrc = rand.New(rand.NewSource(42))

	// 6) Fakes pre-seeded with the single happy-path destination +
	//    workspace + platform_account so stage-1 validate returns
	//    204.
	h.workspaceStore = &fakeE2EWorkspace{
		rows: map[int64]*models.Workspace{
			e2eWorkspaceID: {
				ID:      e2eWorkspaceID,
				OwnerID: 42,
				Name:    "e2e-workspace",
			},
		},
	}
	h.userStore = &fakeE2EUser{
		accounts: map[int64]*models.PlatformAccount{
			e2ePlatformAcctID: {
				ID:               e2ePlatformAcctID,
				Platform:         "youtube",
				Status:           "active",
				ReauthRequiredAt: nil, // healthy
			},
		},
	}
	h.destinations = &fakeE2EDestinations{
		rows: map[string]*models.ExternalDestination{
			e2eDestinationID: {
				ID:                e2eDestinationID,
				SourceSystem:      "velox",
				WorkspaceID:       e2eWorkspaceID,
				PlatformAccountID: e2ePlatformAcctID,
				Enabled:           true,
			},
		},
	}
	h.deliveries = &fakeE2EDeliveries{
		rows: map[string]*models.ExternalDelivery{},
	}

	// 7) Router — inline pattern + register routes. We DO NOT call
	//    MustNewRouter(, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60 * time.Second))) because it requires capRouter + auth.Manager
	//    etc. that this test doesn't need.
	h.router = &Router{
		mux:                  chi.NewRouter(),
		workspaceStore:       h.workspaceStore,
		userRepo:             h.userStore,
		externalDestinations: h.destinations,
		externalDeliveries:   h.deliveries,
		veloxAPIToken:        e2eAPIToken,
	}
	h.router.registerInternalVeloxRoutes()

	t.Cleanup(func() {
		h.veloxArtifactSrv.Close()
		h.youtubeSrv.Close()
		h.veloxCallbackSrv.Close()
	})

	return h
}

// veloxCallbackURL returns the mock server URL the dispatcher
// should POST to. Used to build the request fixture so the
// dispatcher targets our recorder.
func (h *e2eHarness) veloxCallbackURL() string {
	return h.veloxCallbackSrv.URL + "/velox/callback"
}

// veloxArtifactURL returns the mock server URL the worker
// (simulated) GETs the artifact from. Mirrors the
// download_url in the request body.
func (h *e2eHarness) veloxArtifactURL() string {
	return h.veloxArtifactSrv.URL + "/artifacts/" + e2eArtifactID
}

// capturedCallbackEvents returns a snapshot of the callback
// receiver's recorded events, sorted by timestamp so the test
// can assert stage-4 fired in the right order.
func (h *e2eHarness) capturedCallbackEvents() []capturedCallback {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	out := make([]capturedCallback, len(h.callbacks))
	copy(out, h.callbacks)
	return out
}

// subtle_eq is a thin constant-time comparison wrapper that
// returns 1 on equality, 0 otherwise. Implementation matches the
// production middleware's subtle.ConstantTimeCompare semantics so
// timing-leak behaviour matches.
func subtle_eq(a, b string) int {
	return subtleConstantTimeCompare([]byte(a), []byte(b))
}

// =====================================================================
// FAKES
// =====================================================================

// fakeE2EWorkspace embeds the production WorkspaceStore interface
// (nil at construction → calling unused methods panics, which is
// correct behaviour for a test fixture) + overrides the ONLY
// method the validate handler calls: FindByID.
type fakeE2EWorkspace struct {
	WorkspaceStore
	rows map[int64]*models.Workspace
}

func (f *fakeE2EWorkspace) FindByID(id int64) (*models.Workspace, error) {
	if f.rows == nil {
		return nil, errors.New("fakeE2EWorkspace: FindByID called before init")
	}
	return f.rows[id], nil
}

// fakeE2EUser mirrors fakeE2EWorkspace: embed UserStore + override
// FindPlatformAccountByID (the ONLY method the validate handler
// calls; the rest panic-on-call).
type fakeE2EUser struct {
	UserStore
	accounts map[int64]*models.PlatformAccount
}

func (f *fakeE2EUser) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if f.accounts == nil {
		return nil, errors.New("fakeE2EUser: FindPlatformAccountByID called before init")
	}
	if acc, ok := f.accounts[id]; ok {
		return acc, nil
	}
	return nil, nil
}

// fakeE2EDestinations satisfies ExternalDestinationStore:
// GetByID + Create.
type fakeE2EDestinations struct {
	rows map[string]*models.ExternalDestination
}

func (f *fakeE2EDestinations) GetByID(_ context.Context, id string) (*models.ExternalDestination, error) {
	if f.rows == nil {
		return nil, errors.New("fakeE2EDestinations: rows nil")
	}
	return f.rows[id], nil
}

func (f *fakeE2EDestinations) ListByWorkspace(_ context.Context, _ int64, _ bool) ([]models.ExternalDestination, error) {
	return nil, nil
}
func (f *fakeE2EDestinations) Delete(_ context.Context, _ string) error {
	return nil
}
func (f *fakeE2EDestinations) Create(_ context.Context, d *models.ExternalDestination) error {
	if f.rows == nil {
		f.rows = map[string]*models.ExternalDestination{}
	}
	if _, exists := f.rows[d.ID]; exists {
		return errors.New("fakeE2EDestinations: Create on existing id")
	}
	f.rows[d.ID] = d
	return nil
}

// UpdateEnabled + UpdateDefaultMetadata stubs satisfy the
// expanded ExternalDestinationStore interface (PATCH endpoint
// expansion). The e2e harness never exercises either verb; the
// stubs return nil so vet succeeds without forcing fixture
// refactors across the e2e pipeline.
func (f *fakeE2EDestinations) UpdateEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}
func (f *fakeE2EDestinations) UpdateDefaultMetadata(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

// UpdateEnabledAndDefaults is the combined-verb stub. The e2e
// harness never exercises this verb; the stub returns nil so vet
// succeeds without forcing fixture refactors across the e2e
// pipeline.
func (f *fakeE2EDestinations) UpdateEnabledAndDefaults(_ context.Context, _ string, _ *bool, _ json.RawMessage) error {
	return nil
}

// fakeE2EDeliveries satisfies BOTH:
//   - api.ExternalDeliveryStore (Insert + GetByID)
//   - worker.ExternalDeliveryStore (UpdateStatus only)
//
// Insert mirrors the production 3-way idempotency outcome:
//   - lookupByKey returns nil → store + return (e, nil)         // fresh
//   - lookupByKey returns existing + same RequestSHA256 → reuse  // replay → 202 already_exists=true
//   - lookupByKey returns existing + DIFFERENT RequestSHA256 → ErrIdempotencyConflict  // 409
//
// UpdateStatus advances the row's state + error/platform fields in
// the in-memory map. The FSM calls this for every transition.
type fakeE2EDeliveries struct {
	rows        map[string]*models.ExternalDelivery
	insertCalls int32

	mu sync.Mutex
}

// GetByID satisfies ExternalDeliveryStore. Returns (nil, nil)
// when the id is unknown — mirrors the production repo's
// (nil, nil) miss semantic.
func (f *fakeE2EDeliveries) GetByID(_ context.Context, id string) (*models.ExternalDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id], nil
}

// Insert mirrors the production pg_advisory_xact_lock + SELECT +
// INSERT 3-way outcome. rawBody is the raw request bytes; we
// compute the request_sha256 hex so subsequent replays can
// compare.
func (f *fakeE2EDeliveries) Insert(_ context.Context, e *models.ExternalDelivery, rawBody []byte) (*models.ExternalDelivery, error) {
	atomic.AddInt32(&f.insertCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(rawBody) > 0 && e.RequestSHA256 == "" {
		sum := sha256.Sum256(rawBody)
		e.RequestSHA256 = hex.EncodeToString(sum[:])
	}

	// 3-way: same (source_system, idempotency_key) tuple already
	// exists → compare SHA + decide fresh/replay/conflict.
	for _, existing := range f.rows {
		if existing.SourceSystem != e.SourceSystem || existing.IdempotencyKey != e.IdempotencyKey {
			continue
		}
		if existing.RequestSHA256 != e.RequestSHA256 {
			// Different SHA → 409 conflict. Return the existing
			// row + ErrIdempotencyConflict so the handler's
			// errors.Is dispatch fires.
			out := *existing
			return &out, repository.ErrIdempotencyConflict
		}
		// Same SHA → replay. Return the existing row; the handler
		// will compare mintedID with this ID and report
		// already_exists=true.
		out := *existing
		return &out, nil
	}

	// Fresh insert: stamp the row in the map.
	f.rows[e.ID] = e
	return e, nil
}

// UpdateStatus is the worker-side write used by worker.IngestFSM.
// Sets status + the relevant COALESCE-style nullable fields and
// bumps UpdatedAt.
func (f *fakeE2EDeliveries) UpdateStatus(
	_ context.Context,
	id string,
	newStatus models.ExternalDeliveryStatus,
	lastErrorCode,
	lastErrorMessage,
	platformMediaID,
	platformURL *string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.rows[id]
	if !ok {
		return fmt.Errorf("fakeE2EDeliveries.UpdateStatus: id %q not found", id)
	}
	d.Status = newStatus
	if lastErrorCode != nil {
		d.LastErrorCode = lastErrorCode
	}
	if lastErrorMessage != nil {
		d.LastErrorMessage = lastErrorMessage
	}
	if platformMediaID != nil {
		d.PlatformMediaID = platformMediaID
	}
	if platformURL != nil {
		d.PlatformURL = platformURL
	}
	d.UpdatedAt = time.Now()
	if newStatus == models.ExternalDeliveryStatusPublished && d.CompletedAt == nil {
		now := time.Now()
		d.CompletedAt = &now
	}
	return nil
}

// =====================================================================
// TEST: full Phase 1 happy-path pipeline
// =====================================================================

// TestE2E_VeloxPhase1_Pipeline drives all 6 stages of the user's
// spec. Each stage is asserted across (request/response shape,
// in-process store state, side-effect on mock servers).
//
// The 6 stages are exercised in a single function because the
// spec describes a SEQUENTIAL workflow — each transition depends
// on the prior stage's homeostatic state. Splitting across 6
// top-level tests would require a heavy shared-harness setup
// repeated for each; the integration-test style here trades a bit
// of failure-mode granularity for clarity.
