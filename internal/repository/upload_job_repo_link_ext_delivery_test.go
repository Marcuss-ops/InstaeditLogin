package repository

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recordingLinker is the in-process ExternalDeliveryLinker fake
// used by the upload_job_repo LinkToExternalDelivery tests below.
// Records every LinkUploadJob invocation + supports an injected
// error to simulate DB unavailability.
type recordingLinker struct {
	mu      sync.Mutex
	calls   []linkerCall
	linkErr error
}

// linkerCall captures the canonical (deliveryID, uploadJobID)
// pair the helper forwards to. Mirrors the ExternalDeliveryLinker
// signature so a future added arg to that interface lights up a
// compile-time breakage here.
type linkerCall struct {
	DeliveryID  string
	UploadJobID int64
}

func (m *recordingLinker) LinkUploadJob(_ context.Context, deliveryID string, uploadJobID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.linkErr != nil {
		return m.linkErr
	}
	m.calls = append(m.calls, linkerCall{deliveryID, uploadJobID})
	return nil
}

func (m *recordingLinker) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *recordingLinker) firstCall(t *testing.T) linkerCall {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		t.Fatal("no linker calls recorded")
	}
	return m.calls[0]
}

// newTestUploadJobRepo returns an UploadJobRepository with a nil
// DB handle — safe because LinkToExternalDelivery is a pure
// forwarder that never touches r.db. Avoids spinning up Postgres
// in tests that only care about the helper's contract surface.
func newTestUploadJobRepo(_ *testing.T) *UploadJobRepository {
	return &UploadJobRepository{db: nil}
}

// Compile-time assertion: the fake above satisfies the public
// method-signature contract the helper forwards through. Catches
// signature drift at build time BEFORE any test runs (so a future
// added arg to ExternalDeliveryLinker.LinkUploadJob fails the
// build of this test file first, surfacing the drift clearly).
var _ ExternalDeliveryLinker = (*recordingLinker)(nil)

// 1. Happy forwarder — verifies the helper forwards the right
// (external_delivery_id, upload_job_id) pair to the linker.
func TestUploadJobRepo_LinkToExternalDelivery_HappyForwarder(t *testing.T) {
	ctx := context.Background()
	repo := newTestUploadJobRepo(t)
	linker := &recordingLinker{}

	const uploadJobID int64 = 42
	const externalDeliveryID = "sdel_01JTestHappy"

	if err := repo.LinkToExternalDelivery(ctx, linker, uploadJobID, externalDeliveryID); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}
	if got := linker.callCount(); got != 1 {
		t.Fatalf("expected 1 linker call; got %d", got)
	}
	call := linker.firstCall(t)
	if call.DeliveryID != externalDeliveryID {
		t.Errorf("DeliveryID arg = %q; want %q", call.DeliveryID, externalDeliveryID)
	}
	if call.UploadJobID != uploadJobID {
		t.Errorf("UploadJobID arg = %d; want %d", call.UploadJobID, uploadJobID)
	}
}

// 2. Nil linker rejection — must reject BEFORE touching any state.
// Returns a typed error mentioning "nil linker"; bootstrap
// misconfiguration is the canonical cause.
func TestUploadJobRepo_LinkToExternalDelivery_NilLinker(t *testing.T) {
	ctx := context.Background()
	repo := newTestUploadJobRepo(t)

	err := repo.LinkToExternalDelivery(ctx, nil, 42, "sdel_any")
	if err == nil {
		t.Fatal("nil linker must produce a non-nil err")
	}
	if !strings.Contains(err.Error(), "nil linker") {
		t.Errorf("err should mention nil linker; got %q", err.Error())
	}
}

// 3. Empty externalDeliveryID rejection — defends against the FSM
// bug where a re-scan lands a blank id. Caller BUG surface, NOT
// silent no-op.
func TestUploadJobRepo_LinkToExternalDelivery_EmptyExternalDeliveryID(t *testing.T) {
	ctx := context.Background()
	repo := newTestUploadJobRepo(t)
	linker := &recordingLinker{}

	err := repo.LinkToExternalDelivery(ctx, linker, 42, "")
	if err == nil {
		t.Fatal("empty externalDeliveryID must produce a non-nil err")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("err should mention empty; got %q", err.Error())
	}
	if linker.callCount() != 0 {
		t.Errorf("empty-id reject must not touch linker; got %d calls", linker.callCount())
	}
}

// 4. Non-positive uploadJobID rejection — defends against an
// upload-job INSERT that didn't capture the RETURNING id (the
// row was created but the id variable stays at the zero value).
func TestUploadJobRepo_LinkToExternalDelivery_BadUploadJobID(t *testing.T) {
	ctx := context.Background()
	repo := newTestUploadJobRepo(t)
	linker := &recordingLinker{}

	for _, bad := range []int64{0, -1, -100} {
		err := repo.LinkToExternalDelivery(ctx, linker, bad, "sdel_test")
		if err == nil {
			t.Errorf("uploadJobID=%d must produce a non-nil err; got nil", bad)
		}
		if !strings.Contains(err.Error(), "positive") {
			t.Errorf("uploadJobID=%d: err should mention positive; got %q", bad, err.Error())
		}
	}
	if linker.callCount() != 0 {
		t.Errorf("bad-id reject must not touch linker; got %d calls", linker.callCount())
	}
}

// 5. Linker error wrap — preserves the FAILURE context for
// postmortem. The wrap uses %w so callers can errors.Is-dispatch
// against the linker-supplied sentinel (operators rely on this
// to keep their postmortem grep across repos consistent).
func TestUploadJobRepo_LinkToExternalDelivery_LinkerErrorWrap(t *testing.T) {
	ctx := context.Background()
	repo := newTestUploadJobRepo(t)
	linkSentinel := errors.New("linker db unavailable")
	linker := &recordingLinker{linkErr: linkSentinel}

	err := repo.LinkToExternalDelivery(ctx, linker, 42, "sdel_test")
	if err == nil {
		t.Fatal("expected non-nil err when linker fails")
	}
	if !errors.Is(err, linkSentinel) {
		t.Errorf("err should preserve the linker sentinel via %%w; got %v", err)
	}
	if !strings.Contains(err.Error(), "forward to external_delivery_repo.LinkUploadJob") {
		t.Errorf("err should carry the contextual prefix; got %q", err.Error())
	}
}
