package worker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// recordingStore is the in-process ExternalDeliveryStore fake.
// Records every UpdateStatus call for assertion + supports an
// injected error to simulate DB unavailability. Mutex protects
// parallel test goroutines (each test is single-goroutine today
// but the lock keeps the runtime race-detector happy if future
// tests go parallel).
type recordingStore struct {
	mu        sync.Mutex
	updates   []updateCall
	updateErr error
}

// updateCall captures one UpdateStatus invocation verbatim so each
// test can assert (delivery id, new status, error args, media args)
// tuple-by-tuple.
type updateCall struct {
	DeliveryID                         string
	NewStatus                          models.ExternalDeliveryStatus
	ErrCode, ErrMsg, MediaID, MediaURL *string
}

func (m *recordingStore) UpdateStatus(
	_ context.Context,
	id string,
	newStatus models.ExternalDeliveryStatus,
	errCode, errMsg, mediaID, mediaURL *string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updates = append(m.updates, updateCall{
		DeliveryID: id,
		NewStatus:  newStatus,
		ErrCode:    errCode,
		ErrMsg:     errMsg,
		MediaID:    mediaID,
		MediaURL:   mediaURL,
	})
	return nil
}

func (m *recordingStore) snapshot(t *testing.T) []updateCall {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]updateCall, len(m.updates))
	copy(out, m.updates)
	return out
}

func (m *recordingStore) lastUpdate(t *testing.T) updateCall {
	t.Helper()
	updates := m.snapshot(t)
	if len(updates) == 0 {
		t.Fatal("no updates recorded")
	}
	return updates[len(updates)-1]
}

func ptrS(s string) *string { return &s }

// 1. Happy-path 6-step advance — exercises every convenience
// method exactly once and asserts the FULL UpdateStatus call log.
func TestIngestFSM_HappyPath_6Steps(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	deliveryID := "sdel_01JTestHappyPath"

	// Advance each stage with the convenience method matching
	// its 1-step successor. ToPublished needs the mediaID +
	// mediaURL args; the others pass nil (COALESCE preserves
	// the column).
	if err := fsm.ToDownloading(ctx, deliveryID, models.ExternalDeliveryStatusAccepted); err != nil {
		t.Fatalf("ToDownloading: %v", err)
	}
	if err := fsm.ToArtifactVerified(ctx, deliveryID, models.ExternalDeliveryStatusDownloading); err != nil {
		t.Fatalf("ToArtifactVerified: %v", err)
	}
	if err := fsm.ToIngestCompleted(ctx, deliveryID, models.ExternalDeliveryStatusArtifactVerified); err != nil {
		t.Fatalf("ToIngestCompleted: %v", err)
	}
	if err := fsm.ToQueued(ctx, deliveryID, models.ExternalDeliveryStatusIngestCompleted); err != nil {
		t.Fatalf("ToQueued: %v", err)
	}
	if err := fsm.ToPublishing(ctx, deliveryID, models.ExternalDeliveryStatusQueued); err != nil {
		t.Fatalf("ToPublishing: %v", err)
	}
	mediaID := "dQw4w9WgXcQ"
	mediaURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	if err := fsm.ToPublished(ctx, deliveryID, models.ExternalDeliveryStatusPublishing, &mediaID, &mediaURL); err != nil {
		t.Fatalf("ToPublished: %v", err)
	}

	updates := store.snapshot(t)
	if len(updates) != 6 {
		t.Fatalf("expected 6 updates; got %d", len(updates))
	}
	wantStatuses := []models.ExternalDeliveryStatus{
		models.ExternalDeliveryStatusDownloading,
		models.ExternalDeliveryStatusArtifactVerified,
		models.ExternalDeliveryStatusIngestCompleted,
		models.ExternalDeliveryStatusQueued,
		models.ExternalDeliveryStatusPublishing,
		models.ExternalDeliveryStatusPublished,
	}
	for i, want := range wantStatuses {
		if updates[i].NewStatus != want {
			t.Errorf("update[%d].NewStatus = %q; want %q", i, updates[i].NewStatus, want)
		}
		if updates[i].DeliveryID != deliveryID {
			t.Errorf("update[%d].DeliveryID = %q; want %q", i, updates[i].DeliveryID, deliveryID)
		}
	}
	// ToPublished must propagate media id + URL; earlier steps pass nil.
	if updates[5].MediaID == nil || *updates[5].MediaID != mediaID {
		t.Errorf("ToPublished MediaID not propagated: %v", updates[5].MediaID)
	}
	if updates[5].MediaURL == nil || *updates[5].MediaURL != mediaURL {
		t.Errorf("ToPublished MediaURL not propagated: %v", updates[5].MediaURL)
	}
	if updates[0].ErrCode != nil || updates[0].ErrMsg != nil || updates[0].MediaID != nil || updates[0].MediaURL != nil {
		t.Errorf("happy-path advance should not pass error/media args: %+v", updates[0])
	}
}

// 2. Terminal-source rejection — published / failed / dead_letter
// must reject EVERY outgoing transition with ErrTerminal AND
// must NOT touch the store.
func TestIngestFSM_TerminalSourceRejection(t *testing.T) {
	ctx := context.Background()
	terminalStates := []models.ExternalDeliveryStatus{
		models.ExternalDeliveryStatusPublished,
		models.ExternalDeliveryStatusFailed,
		models.ExternalDeliveryStatusDeadLetter,
	}
	for _, term := range terminalStates {
		store := &recordingStore{}
		fsm := NewIngestFSM(store, slog.Default())

		err := fsm.ToQueued(ctx, "sdel_term_"+string(term), term)
		if !errors.Is(err, ErrTerminal) {
			t.Errorf("terminal %q → queued: expected ErrTerminal; got %v", term, err)
		}
		if updates := store.snapshot(t); len(updates) != 0 {
			t.Errorf("terminal %q: expected 0 store writes; got %d", term, len(updates))
		}

		// And second pattern: passing a different next-status the
		// terminal state has no edge to.
		err = fsm.Transition(ctx, "sdel_term_"+string(term), term, models.ExternalDeliveryStatusArtifactVerified, nil, nil, nil, nil)
		if !errors.Is(err, ErrTerminal) {
			t.Errorf("terminal %q: Transition → artifact_verified should reject via ErrTerminal; got %v", term, err)
		}
	}
}

// 3. Illegal but non-terminal source — surfaces as
// ErrIllegalTransition. blocked_auth → artifact_verified is
// illegal (blocked_auth only has the queued edge).
func TestIngestFSM_IllegalTransition_Sentinel(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	err := fsm.ToArtifactVerified(ctx, "sdel_illegal", models.ExternalDeliveryStatusBlockedAuth)
	if err == nil {
		t.Fatal("expected ErrIllegalTransition")
	}
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("expected ErrIllegalTransition; got %v", err)
	}
	// And confirm ErrTerminal is NOT double-counted.
	if errors.Is(err, ErrTerminal) {
		t.Errorf("blocked_auth is not terminal-source; ErrTerminal should NOT be in chain: %v", err)
	}
	if updates := store.snapshot(t); len(updates) != 0 {
		t.Errorf("illegal transitions must not touch store; got %d updates", len(updates))
	}
}

// 4. Resume dual-target — true → queued, false → downloading.
func TestIngestFSM_Resume_DualTarget(t *testing.T) {
	ctx := context.Background()

	// downloadURLValid=true → queued
	storeQ := &recordingStore{}
	fsmQ := NewIngestFSM(storeQ, slog.Default())
	if err := fsmQ.Resume(ctx, "sdel_resume_q", models.ExternalDeliveryStatusRetryWait, true); err != nil {
		t.Fatalf("Resume(true): %v", err)
	}
	if u := storeQ.lastUpdate(t); u.NewStatus != models.ExternalDeliveryStatusQueued {
		t.Errorf("Resume(true) → expected Queued; got %q", u.NewStatus)
	}

	// downloadURLValid=false → downloading
	storeD := &recordingStore{}
	fsmD := NewIngestFSM(storeD, slog.Default())
	if err := fsmD.Resume(ctx, "sdel_resume_d", models.ExternalDeliveryStatusRetryWait, false); err != nil {
		t.Fatalf("Resume(false): %v", err)
	}
	if u := storeD.lastUpdate(t); u.NewStatus != models.ExternalDeliveryStatusDownloading {
		t.Errorf("Resume(false) → expected Downloading; got %q", u.NewStatus)
	}
}

// 5. Resume from non-retry_wait state — rejects with descriptive
// error (no store write).
func TestIngestFSM_Resume_WrongSource(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	err := fsm.Resume(ctx, "sdel_resume_e", models.ExternalDeliveryStatusQueued, true)
	if err == nil {
		t.Fatal("Resume from Queued should reject")
	}
	if !strings.Contains(err.Error(), "queued") {
		t.Errorf("err should reference actual state name; got %v", err)
	}
	if updates := store.snapshot(t); len(updates) != 0 {
		t.Errorf("rejected Resume must not touch store; got %d", len(updates))
	}
}

// 6. Error-exit coverage — ToRetryWait / ToFailed / ToBlockedAuth
// from each pre-terminal source. Each call carries the code + msg
// args; the FSM forwards them to UpdateStatus verbatim.
func TestIngestFSM_ErrorExits_AllSources(t *testing.T) {
	ctx := context.Background()
	preTerminalSources := []models.ExternalDeliveryStatus{
		models.ExternalDeliveryStatusDownloading,
		models.ExternalDeliveryStatusArtifactVerified,
		models.ExternalDeliveryStatusIngestCompleted,
		models.ExternalDeliveryStatusQueued,
		models.ExternalDeliveryStatusPublishing,
	}
	// accepted is OUT of this list — accepted has retry_wait +
	// blocked_auth + failed edges per transitionMap but the
	// worker hasn't actually issued a Download HEAD yet; the
	// accepted→error exits are tested separately via the
	// transitionMap contract TestTransitionMapEnumCoverage
	// in the models package.

	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	for _, src := range preTerminalSources {
		// retry_wait
		if err := fsm.ToRetryWait(ctx, "sdel_rw_"+string(src), src, "transient_5xx", "quota exhausted"); err != nil {
			t.Errorf("ToRetryWait from %q: %v", src, err)
		}
		u := store.lastUpdate(t)
		if u.NewStatus != models.ExternalDeliveryStatusRetryWait {
			t.Errorf("ToRetryWait from %q should update RetryWait; got %q", src, u.NewStatus)
		}
		if u.ErrCode == nil || *u.ErrCode != "transient_5xx" {
			t.Errorf("ToRetryWait ErrCode not propagated: %v", u.ErrCode)
		}
		if u.ErrMsg == nil || *u.ErrMsg != "quota exhausted" {
			t.Errorf("ToRetryWait ErrMsg not propagated: %v", u.ErrMsg)
		}

		// failed
		if err := fsm.ToFailed(ctx, "sdel_f_"+string(src), src, "json_garbage", "malformed payload"); err != nil {
			t.Errorf("ToFailed from %q: %v", src, err)
		}
		u = store.lastUpdate(t)
		if u.NewStatus != models.ExternalDeliveryStatusFailed {
			t.Errorf("ToFailed from %q should update Failed; got %q", src, u.NewStatus)
		}

		// blocked_auth
		if err := fsm.ToBlockedAuth(ctx, "sdel_ba_"+string(src), src, "reauth_required", "google oauth expired"); err != nil {
			t.Errorf("ToBlockedAuth from %q: %v", src, err)
		}
		u = store.lastUpdate(t)
		if u.NewStatus != models.ExternalDeliveryStatusBlockedAuth {
			t.Errorf("ToBlockedAuth from %q should update BlockedAuth; got %q", src, u.NewStatus)
		}
	}

	// 5 sources × 3 exits = 15 updates
	if got := len(store.snapshot(t)); got != 15 {
		t.Errorf("expected 15 updates; got %d", got)
	}
}

// 7. dead_letter reachable ONLY from retry_wait — confirms the
// transitionMap closure. Other pre-terminal sources rejecting
// dead_letter is the LOCK the operator-runbook depends on.
func TestIngestFSM_DeadLetterOnlyFromRetryWait(t *testing.T) {
	ctx := context.Background()

	// happy: retry_wait → dead_letter
	storeOK := &recordingStore{}
	fsmOK := NewIngestFSM(storeOK, slog.Default())
	if err := fsmOK.ToDeadLetter(ctx, "sdel_dl_legal", models.ExternalDeliveryStatusRetryWait, "max_attempts", "budget exhausted"); err != nil {
		t.Errorf("retry_wait → dead_letter should be legal; got %v", err)
	}
	if u := storeOK.lastUpdate(t); u.NewStatus != models.ExternalDeliveryStatusDeadLetter {
		t.Errorf("expected DeadLetter; got %q", u.NewStatus)
	}

	// illegal: publishing → dead_letter (queue+publish families
	// must stage through retry_wait first per the operator
	// runbook).
	storeBad := &recordingStore{}
	fsmBad := NewIngestFSM(storeBad, slog.Default())
	err := fsmBad.ToDeadLetter(ctx, "sdel_dl_illegal", models.ExternalDeliveryStatusPublishing, "x", "y")
	if err == nil {
		t.Fatal("publishing → dead_letter should be illegal")
	}
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("expected ErrIllegalTransition; got %v", err)
	}
	if updates := storeBad.snapshot(t); len(updates) != 0 {
		t.Errorf("illegal dead_letter must not touch store; got %d", len(updates))
	}
}

// 8. blocked_auth → queued admin-reconnect edge. Distinct
// convenience method (ToQueuedFromBlockedAuth) so the worker
// doesn't risk a typo'd from-state.
func TestIngestFSM_BlockedAuth_AdminReconnect(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	if err := fsm.ToQueuedFromBlockedAuth(ctx, "sdel_reconnect"); err != nil {
		t.Fatalf("ToQueuedFromBlockedAuth: %v", err)
	}
	if u := store.lastUpdate(t); u.NewStatus != models.ExternalDeliveryStatusQueued {
		t.Errorf("expected Queued; got %q", u.NewStatus)
	}
}

// 9. Persist failure bubbles up with the wrapped upstream error.
// Caller can errors.Is against the recorded sentinel to
// distinguish transient vs permanent. Confirms Transition does
// NOT silently swallow DB errors.
func TestIngestFSM_PersistFailureBubblesUp(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{updateErr: errors.New("db connection refused")}
	fsm := NewIngestFSM(store, slog.Default())

	err := fsm.ToDownloading(ctx, "sdel_persist_err", models.ExternalDeliveryStatusAccepted)
	if err == nil {
		t.Fatal("persist failure should produce a non-nil err")
	}
	if !strings.Contains(err.Error(), "db connection refused") {
		t.Errorf("err should wrap the underlying DB error; got %v", err)
	}
}

// 10. Empty deliveryID rejected at the boundary — guard so the
// worker can't (e.g. via a typo'd row scan) dispatch on a blank
// status update.
func TestIngestFSM_EmptyDeliveryIDRejected(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	err := fsm.ToDownloading(ctx, "", models.ExternalDeliveryStatusAccepted)
	if err == nil {
		t.Fatal("empty deliveryID should be rejected")
	}
	if updates := store.snapshot(t); len(updates) != 0 {
		t.Errorf("empty deliveryID must not touch store; got %d", len(updates))
	}

	// Same guard via the lower-level Transition.
	err = fsm.Transition(ctx, "", models.ExternalDeliveryStatusAccepted, models.ExternalDeliveryStatusDownloading, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("Transition with empty deliveryID should be rejected")
	}
}

// 11. Empty from OR to state rejected at the boundary — guards
// against zero-valued ExternalDeliveryStatus leaking through.
func TestIngestFSM_EmptyStateRejected(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	err := fsm.Transition(ctx, "sdel_e", "", models.ExternalDeliveryStatusDownloading, nil, nil, nil, nil)
	if err == nil {
		t.Error("empty from should be rejected")
	}
	err = fsm.Transition(ctx, "sdel_e", models.ExternalDeliveryStatusAccepted, "", nil, nil, nil, nil)
	if err == nil {
		t.Error("empty to should be rejected")
	}
	if updates := store.snapshot(t); len(updates) != 0 {
		t.Errorf("empty state must not touch store; got %d", len(updates))
	}
}

// 12. Concurrency sanity — multiple goroutines call Transition on
// the same FSM in parallel; every call lands in the recording
// store. Asserts the FSM is safe for the worker-pool scenario
// where N goroutines share one IngestFSM struct.
func TestIngestFSM_ConcurrentDispatch(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{}
	fsm := NewIngestFSM(store, slog.Default())

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			id := "sdel_concurrent_" + string(rune('A'+i%26))
			if err := fsm.ToDownloading(ctx, id, models.ExternalDeliveryStatusAccepted); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	updates := store.snapshot(t)
	if len(updates) != N {
		t.Errorf("expected %d updates; got %d", N, len(updates))
	}
	for _, u := range updates {
		if u.NewStatus != models.ExternalDeliveryStatusDownloading {
			t.Errorf("all concurrent Updates should target Downloading; got %q", u.NewStatus)
		}
	}
}
