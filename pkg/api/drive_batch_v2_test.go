// Package api test file for the async folder-batch import handler
// (handleDriveBatchImportV2). These tests exercise the producer-side
// 90-day horizon cap (P1 refactor) end-to-end:
//
//   - DriftBatchImportV2Request with start_at + worst-case min_gap that
//     projects beyond scheduleClampHorizonDays (90) →
//     DriftBatchImportV2OverflowResponse (422 Unprocessable Entity)
//     AND the import_batches header MUST NOT be persisted.
//   - DriftBatchImportV2Request WITHIN the 90-day cap →
//     driftBatchImportV2Response (202 Accepted) with
//     ScheduleClamped=false.
//
// Scaffolding is deliberately self-contained:
//
//   - mockImportBatchStore implements pkg/api.ImportBatchStore with a
//     createCalls counter the no-Create invariant relies on
//     (NOTHING else should call Create on the 422 path).
//   - mockUserStore + mockWorkspaceStore satisfy the WorkspaceStore +
//     UserStore interfaces the handler depends on before it reaches
//     the overflow checkpoint.
//   - auth.NewUserIdentity stamps the request context with a real
//     auth.UserIdentity (satisfies the auth.Identity interface);
//     requireUserID reads UserID() from it.
//
// The buildUpV2Request helper scaffolds a valid body with every
// required field set; tests override min_gap_seconds / max_gap_seconds
// to drive the overflow / within-horizon paths.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// --------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------

// mockImportBatchStoreV2 implements the pkg/api.ImportBatchStore
// interface. createCalls is the assertion surface for the
// "422 MUST NOT call Create" invariant.
//
// The 202 path expects createCalls==1 (one header row per accepted
// batch); the 422 path expects createCalls==0 (the handler refuses
// before the SQL INSERT). Any deviation is a regression in the
// safety-net guarantee.
//
// When forbidCreateOnTest is set (the 422-path test wires this),
// any Create call is a HARD FAIL (t.Errorf) — not just a counter
// mismatch. This converts the assertion from "we counted and
// remembered" (can be muted by accident) into "the call site
// would have failed the test at the moment of the violation".
type mockImportBatchStoreV2 struct {
	t                    *testing.T
	createCalls          int
	createErr            error
	forbidCreateOnTest   bool
	createdBatches       []models.ImportBatch
}

func (m *mockImportBatchStoreV2) Create(b *models.ImportBatch) error {
	if m.forbidCreateOnTest && m.t != nil {
		// HARD fail — the 422 path's whole point is that Create is
		// never reached. Make the violation unmissable in CI logs.
		m.t.Errorf(
			"importBatchStore.Create was called on the 422 overflow "+
				"path — the safety net MUST refuse to persist the "+
				"ImportBatch header (batch=%+v)", b,
		)
	}
	if m.createErr != nil {
		return m.createErr
	}
	m.createCalls++
	b.ID = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	b.CreatedAt = time.Now().UTC()
	b.UpdatedAt = time.Now().UTC()
	m.createdBatches = append(m.createdBatches, *b)
	return nil
}

func (m *mockImportBatchStoreV2) FindByID(id uuid.UUID) (*models.ImportBatch, error) {
	for i := range m.createdBatches {
		if m.createdBatches[i].ID == id {
			return &m.createdBatches[i], nil
		}
	}
	return nil, errors.New("FindByID not implemented in this test")
}

// stubUserStoreV2 is a tiny nullable-valued UserStore. The handler
// resolves the authenticated user via auth.IdentityFromContext (NOT
// via this store) — only the *handler-level type assert* reads
// r.userRepo. The methods below return errors so any future change
// that DOES route through UserStore fails loudly instead of
// silently using the zero values.
//
// Prefixed with `stubUserStoreV2` to avoid colliding with the
// shared mockUserStore in routes_test.go (which exercises the
// OAuth callback path with a richer surface).
type stubUserStoreV2 struct{}

func (m *stubUserStoreV2) AttachPlatformAccount(int64, *models.PlatformProfile, string) (*models.PlatformAccount, error) {
	return nil, errors.New("stubUserStoreV2: not used by handleDriveBatchImportV2")
}
func (m *stubUserStoreV2) ListPlatformAccountsByUser(int64, string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (m *stubUserStoreV2) FindPlatformAccountByID(int64) (*models.PlatformAccount, error) {
	return nil, nil
}
func (m *stubUserStoreV2) FindPlatformAccount(string, string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (m *stubUserStoreV2) UpdatePlatformAccount(*models.PlatformAccount) error { return nil }
func (m *stubUserStoreV2) DeletePlatformAccount(int64) error                { return nil }
func (m *stubUserStoreV2) FindUserIDByEmail(context.Context, string) (int64, error) {
	return 0, nil // not exercised by handleDriveBatchImportV2; satisfies UserStore contract
}
func (m *stubUserStoreV2) FinalizeAttach(context.Context, int64, []string) (int64, error) {
	return 0, nil // not exercised by handleDriveBatchImportV2; satisfies UserStore contract
}

// MarkReauthRequired (Task 2/10) satisfies the UserStore interface
// after the channel-binding best-effort flag was added to the OAuth
// callback path. handleDriveBatchImportV2 doesn't invoke the OAuth
// path so this stub is intentionally a quiet nil-returner — matching
// the surrounding "silently-no-op" pattern on this struct. A future
// test that exercises the 422 mismatch path can override the
// field via the implicit-fields pattern (the shared mockUserStore
// in routes_test.go uses a markReauthRequiredFn function field; this
// stub can stay simple because no test in this file wires it).
func (m *stubUserStoreV2) MarkReauthRequired(context.Context, int64, string, string) error {
	return nil
}

// stubWorkspaceStoreV2 lets the handler resolve body.WorkspaceID
// against an in-memory ownership map. ownedWorkspaces[wid]=true means
// the caller is the workspace owner; FindByID returns a Workspace
// whose OwnerID matches the stamped user identity's UserID (1 in
// the tests below) so requireWorkspaceOwnership short-circuits to
// "owner = caller" and the handler reaches the overflow /
// within-horizon checkpoints.
//
// Prefixed with `stubWorkspaceStoreV2` to avoid colliding with the
// shared mockWorkspaceStore in routes_test.go (which the OAuth /
// /workspaces/* suites reuse with a richer surface).
type stubWorkspaceStoreV2 struct {
	ownedWorkspaces map[int64]bool
}

func (m *stubWorkspaceStoreV2) Create(*models.Workspace) error { return nil }
func (m *stubWorkspaceStoreV2) FindByID(id int64) (*models.Workspace, error) {
	if m.ownedWorkspaces[id] {
		// OwnerID MUST match the user identity's UserID() — the
		// handler's requireWorkspaceOwnership guard reads this
		// field and rejects with 403 when it doesn't match. The
		// test identity below stamps UserID=1, so the workspace
		// owner here MUST be 1 for the handler to reach the
		// overflow / within-horizon checkpoints.
		return &models.Workspace{ID: id, OwnerID: 1}, nil
	}
	return nil, nil
}

// ------- UNUSED (just to silence the method-set check) ---------------
func (m *stubWorkspaceStoreV2) ListByOwner(int64) ([]models.Workspace, error) {
	return nil, nil
}
func (m *stubWorkspaceStoreV2) Delete(int64) error { return nil }
func (m *stubWorkspaceStoreV2) AttachChannel(context.Context, int64, int64, string) (*models.WorkspaceChannel, error) {
	return nil, nil
}
// ListChannels seeds ONE channel with GroupName="test-group" and
// Enabled=true so resolveV2Targets("test-group") → returns
// [PlatformAccountID=42]. This is the bridge the handler needs to
// proceed past the "no target set supplied" defensive return when
// the test request carries TargetGroupID="test-group" instead of
// TargetAccountIDs. The seed channel is intentionally hard-coded so
// the precedence cascade (post.PrivacyLevel > post.DefaultPrivacyLevel
// > "unlisted" fallback) is reproducible across CI runs.
func (m *stubWorkspaceStoreV2) ListChannels(_ context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
	return []models.WorkspaceChannel{
		{
			WorkspaceID:       workspaceID,
			PlatformAccountID: 42,
			GroupName:         "test-group",
			Enabled:           true,
		},
	}, nil
}
func (m *stubWorkspaceStoreV2) UpdateChannel(context.Context, int64, int64, *string, *bool) error {
	return nil
}
func (m *stubWorkspaceStoreV2) DetachChannel(context.Context, int64, int64) error {
	return nil
}
func (m *stubWorkspaceStoreV2) FindChannel(context.Context, int64, int64) (*models.WorkspaceChannel, error) {
	return nil, nil
}

// --------------------------------------------------------------------
// Test scaffolding helpers
// --------------------------------------------------------------------

// buildUpV2Request returns a valid DriveBatchImportV2Request body
// pre-populated with every field the producer-side validator
// (validateDriveBatchV2Request) requires. The targets are encoded
// as TargetGroupID="test-group" (NOT TargetAccountIDs) so the
// handler's resolveV2Targets(...) call returns a non-empty list of
// account_ids — the seeded channel in stubWorkspaceStoreV2.ListChannels
// carries group_name="test-group" + PlatformAccountID=42, so the
// handler proceeds past resolveV2Targets to either the overflow
// check or the Create path.
//
// Tests override min_gap_seconds + max_gap_seconds to drive the
// overflow / within-horizon paths without re-pasting the same
// boilerplate.
func buildUpV2Request(workspaceID int64, minGap, maxGap int) DriveBatchImportV2Request {
	folderID := "abc-folder-id-test"
	privacy := "unlisted"
	groupID := "test-group"
	return DriveBatchImportV2Request{
		Source: models.DriveSourceRef{
			Provider:       "google_drive",
			DriveAccountID: int64Ptr(99),
			FolderID:       folderID,
		},
		WorkspaceID:         workspaceID,
		TargetGroupID:       &groupID,
		DefaultPrivacyLevel: privacy,
		PublishSchedule: models.PublishScheduleRef{
			StartAt:       time.Now().UTC().Add(1 * time.Minute),
			MinGapSeconds: minGap,
			MaxGapSeconds: maxGap,
		},
	}
}

func int64Ptr(i int64) *int64 { return &i }

// callV2Handler drives the producer-side handler with a JWT-style
// identity stamped into ctx. The handler's first guard
// (requireUserID — pkg/api/handlers.go) reads the user id via
// auth.UserIDFromContext(req.Context()) which expects the legacy
// userIDKey ctx value stamped by the JWT middleware. The robust
// path is to stamp BOTH the userIDKey AND the Identity interface —
// belt (userIDKey for requireUserID) + suspenders (Identity for any
// downstream code that reads via auth.IdentityFromContext). This
// matches the production middleware chain (Manager.Middleware +
// downstream handler) which stamps both keys after a successful
// JWT exchange.
func callV2Handler(t *testing.T, r *Router, body DriveBatchImportV2Request) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder/async", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	ctx := req.Context()
	// Belt: legacy userIDKey for requireUserID (which reads via
	// auth.UserIDFromContext).
	ctx = auth.WithUserID(ctx, 1)
	// Suspenders: Identity interface for any downstream code that
	// reads via auth.IdentityFromContext (e.g. requireWorkspaceOwnership
	// when it traces the owner back to the identity's workspace).
	ctx = auth.WithIdentity(ctx, auth.NewUserIdentity(1, body.WorkspaceID, 0))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	r.handleDriveBatchImportV2(rec, req)
	return rec
}

// newTestRouterV2 wires a bare-minimum Router that satisfies every
// dependency handleDriveBatchImportV2 reads before reaching the
// overflow checkpoint:
//
//   - importBatchStore (mock + createCalls counter + optional hard-fail mode)
//   - userRepo         (stub for type-safety; not exercised here)
//   - workspaceStore   (mock with ownedWorkspaces[wid]=true)
//   - maxUploadBytes   (1 MiB — handler's default cap)
//
// We do NOT call Setup(): the handler is invoked directly so we
// skip the chi mux + middleware chain (auth/CSRF/rate-limit) the
// full route would mount on top of.
func newTestRouterV2(store ImportBatchStore, workspaceID int64) *Router {
	return &Router{
		importBatchStore: store,
		userRepo:         &stubUserStoreV2{},
		workspaceStore: &stubWorkspaceStoreV2{
			ownedWorkspaces: map[int64]bool{workspaceID: true},
		},
		maxUploadBytes: 1 << 20, // 1 MiB
	}
}

// --------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------

// TestDriveBatchImportV2_ScheduleOverflow_Returns422 covers the user
// contract (a): when the worst-case projected horizon EXCEEDS the
// 90-day cap, the handler returns 422 with a
// DriveBatchImportV2OverflowResponse whose projected_horizon_days +
// max_horizon_days match the heuristic. The chosen values:
//
//	test_min_gap  = 86_400 seconds  (1 day)
//	worst-case N  = 10_000 files    (scheduleClampHeuristicMaxFiles)
//	projected horizon = 86_400 × 10_000 = 864_000_000 seconds
//	                  = 10_000 days  (verified: (864_000_000 + 86_399) / 86_400 = 10_000)
//
// The deterministic input avoids time-of-day drift so the test is
// reproducible across CI runs without clock-skew flakes.
func TestDriveBatchImportV2_ScheduleOverflow_Returns422(t *testing.T) {
	store := &mockImportBatchStoreV2{
		t:                  t,
		forbidCreateOnTest: true, // HARD FAIL if Create is reached
	}
	router := newTestRouterV2(store, 1)
	body := buildUpV2Request(1, 86_400, 86_400) // 1 day × 10k files = 10k days >> 90

	rec := callV2Handler(t, router, body)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422 (overflow), got %d (body=%s)",
			rec.Code, rec.Body.String())
	}

	var got DriveBatchImportV2OverflowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response body: %v (body=%s)", err, rec.Body.String())
	}

	// Every overflow field MUST be present so the SPA can render
	// an actionable message ("your batch was too long — projected
	// X days, max Y days; widen the gap or trim the folder").
	if got.Error == "" {
		t.Error("response.error: want non-empty (operator-actionable message)")
	}
	if !got.Clamped {
		t.Error("response.clamped: want true (the 422 IS the explicit clamp)")
	}
	if got.ClampReason == "" {
		t.Error("response.clamp_reason: want non-empty (operator-actionable)")
	}
	if got.ProjectedHorizonDays != 10_000 {
		t.Errorf("response.projected_horizon_days: want 10000 (1d × 10k files), got %d",
			got.ProjectedHorizonDays)
	}
	if got.MaxHorizonDays != scheduleClampHorizonDays {
		t.Errorf("response.max_horizon_days: want %d (scheduleClampHorizonDays), got %d",
			scheduleClampHorizonDays, got.MaxHorizonDays)
	}
	// The mock's forbidCreateOnTest = true already hard-fails via
	// t.Errorf from inside Create() if the safety net is somehow
	// bypassed; this counter is the post-hoc belt-and-suspenders
	// assertion the test plan also asked for.
	if store.createCalls != 0 {
		t.Errorf("importBatchStore.Create calls on 422: want 0, got %d (the safety net MUST refuse to persist the header)",
			store.createCalls)
	}
}

// TestDriveBatchImportV2_WithinHorizon_Returns202 covers the user
// contract (b): when the worst-case projected horizon is INSIDE the
// 90-day cap, the handler accepts the batch (202) and stamps
// ScheduleClamped=false on the response.
//
// Values:
//
//	test_min_gap = 60 seconds (1 minute)
//	worst-case N = 10_000 files = 600_000 seconds ≈ 6.94 days < 90
//
// AND importBatchStore.Create MUST be called exactly once (the
// header row is what the background crawler claims).
func TestDriveBatchImportV2_WithinHorizon_Returns202(t *testing.T) {
	store := &mockImportBatchStoreV2{t: t}
	router := newTestRouterV2(store, 1)
	body := buildUpV2Request(1, 60, 60) // 1 min × 10k files ≈ 7 days < 90

	rec := callV2Handler(t, router, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202 (Accepted), got %d (body=%s)",
			rec.Code, rec.Body.String())
	}

	var got DriveBatchImportV2Response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	// ScheduleClamped is the explicit user-facing contract: when the
	// heuristic projects within the cap, the response says
	// "schedule_clamped: false" so the SPA does NOT show a
	// "your batch was clamped, here's what we did" toast. The
	// runtime crawler applies the EXACT re-stamp at file-known
	// fan-out time; the producer-side flag is the
	// preview-vs-truth signal.
	if got.ScheduleClamped {
		t.Error("response.schedule_clamped: want false (within 90-day cap), got true")
	}
	if got.Status != string(models.ImportBatchStatusQueued) {
		t.Errorf("response.status: want %q, got %q",
			models.ImportBatchStatusQueued, got.Status)
	}
	if got.BatchID == uuid.Nil {
		t.Error("response.batch_id: want non-zero (Create must have stamped a UUID)")
	}
	if store.createCalls != 1 {
		t.Errorf("importBatchStore.Create calls on 202: want 1, got %d",
			store.createCalls)
	}
}

// TestDriveBatchImportV2_OverflowDoesNotCreate covers the user
// contract (c) as a standalone, focused assertion: the 422 safety
// net MUST refuse to call importBatchStore.Create. This is a
// narrower, sharper exercise than the no-Create side-check inside
// TestDriveBatchImportV2_ScheduleOverflow_Returns422 — it isolates
// the no-Create invariant so a future regression that adds a Create
// call BEFORE the overflow check OR after a partial overflow reply
// trips THIS test before it can compound with response-field changes.
//
// Uses min_gap_seconds well above the 7_776_000 sec/10_000 file
// threshold (= 90 days × 86_400 sec/day / 10_000 files) so the
// heuristic always returns > 90 days regardless of platform-clock
// rounding.
//
// The mock's forbidCreateOnTest = true means a Create call would
// t.Errorf FROM INSIDE the function — louder + earlier than a
// counter assertion can be.
func TestDriveBatchImportV2_OverflowDoesNotCreate(t *testing.T) {
	store := &mockImportBatchStoreV2{
		t:                  t,
		forbidCreateOnTest: true,
	}
	router := newTestRouterV2(store, 1)
	// min_gap = 1 day × 10k = 10k days — well past the 90-day cap.
	body := buildUpV2Request(1, 86_400, 86_400)

	rec := callV2Handler(t, router, body)

	// Pre-conditions: handler reached the overflow branch.
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("precondition: want 422, got %d (body=%s)",
			rec.Code, rec.Body.String())
	}
	// Core invariant: the safety net's no-Create guarantee.
	// (The mock's Create already t.Errorf'd if it was reached.)
	if store.createCalls != 0 {
		t.Errorf("importBatchStore.Create calls on 422: want 0 ("+
			"the safety net MUST refuse to persist the header before "+
			"writing the response), got %d", store.createCalls)
	}
	if len(store.createdBatches) != 0 {
		t.Errorf("createdBatches length on 422: want 0, got %d (%+v)",
			len(store.createdBatches), store.createdBatches)
	}
}
