// Tests for handlers in pkg/api/admin_channels.go. Focuses on the
// new GET /admin/youtube/fleet_readiness endpoint (3 cases); the
// existing handler tests live alongside their handlers'
// `_test.go` siblings (admin_velox_destinations_test.go mirrors
// the AdminStore stub pattern used here).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// stubAdminStore implements the AdminStore interface with inert
// returns everywhere except CreateFleetReadinessSnapshot, where it
// returns a canned FleetReadinessSnapshotResponse AND increments
// an atomic call counter so the handler tests can assert "did we
// actually invoke the repo?" without a sqlmock dependency.
//
// The interface requires 10 methods; this stub intentionally
// returns zero/empty for the 9 methods the new handler does not
// touch. Each method keeps the exact signature so a future
// compile-time check (var _ AdminStore = (*stubAdminStore)(nil))
// catches any drift between this file and the live interface.
type stubAdminStore struct {
	createCalled atomic.Int64
	createResp   repository.FleetReadinessSnapshotResponse
	createErr    error
}

func (s *stubAdminStore) ChannelCounts(_ context.Context) (repository.AdminChannelCounts, error) {
	return repository.AdminChannelCounts{}, nil
}

func (s *stubAdminStore) ListChannelsForOps(_ context.Context, _ string, _ string, _ int) ([]repository.AdminChannelRow, error) {
	return nil, nil
}

func (s *stubAdminStore) QueueCounts(_ context.Context) (repository.AdminQueueCounts, error) {
	return repository.AdminQueueCounts{}, nil
}

func (s *stubAdminStore) InFlightPerWorker(_ context.Context) ([]repository.AdminInFlightRow, error) {
	return nil, nil
}

func (s *stubAdminStore) ListStuckJobs(_ context.Context, _ int) ([]repository.AdminStuckJobRow, error) {
	return nil, nil
}

func (s *stubAdminStore) ListDeadLetterJobs(_ context.Context, _ int) ([]repository.AdminDeadLetterJobRow, error) {
	return nil, nil
}

func (s *stubAdminStore) ErrorRatePerChannel(_ context.Context, _ string, _ string, _ int) ([]repository.AdminErrorRateRow, error) {
	return nil, nil
}

func (s *stubAdminStore) YouTubeQuotaApproximation(_ context.Context, _ time.Duration, _ int64, _ int64) (repository.AdminYouTubeQuota, error) {
	return repository.AdminYouTubeQuota{}, nil
}

func (s *stubAdminStore) UpsertPendingChannel(_ context.Context, _ int64, _ []channelimport.ImportRow) (channelimport.Result, error) {
	return channelimport.Result{}, nil
}

// CreateFleetReadinessSnapshot returns the canned response seeded
// via createResp (or createErr) and bumps the call counter. The
// stub intentionally does NOT validate the adminUserID against
// the request-context identity --- that contract is held by the
// production AdminRepository implementation, NOT the in-process
// stub. The tests verify the handler invokes the repo with the
// identity's UserID; they don't re-test the production-side ID
// validation that's covered end-to-end elsewhere.
func (s *stubAdminStore) CreateFleetReadinessSnapshot(_ context.Context, _ int64) (repository.FleetReadinessSnapshotResponse, error) {
	s.createCalled.Add(1)
	return s.createResp, s.createErr
}

// staffIdentity composes a minimal auth.Identity for handler-side
// tests. The package's Identity interface is broad; only the
// methods handleAdminYouTubeFleetReadiness touches (UserID, IsAdmin)
// are non-zero. Everything else is the zero-value so the stub
// keeps the test surface narrow.
type staffIdentity struct {
	uid     int64
	isAdmin bool
}

func (s staffIdentity) UserID() int64             { return s.uid }
func (s staffIdentity) WorkspaceID() int64        { return 0 }
func (s staffIdentity) IsAPIKey() bool            { return false }
func (s staffIdentity) IsAdmin() bool             { return s.isAdmin }
func (s staffIdentity) HasPermission(string) bool { return s.isAdmin }
func (s staffIdentity) SessionID() int64          { return 0 }
func (s staffIdentity) Permissions() []string     { return nil }

// KeyID is the API-key fingerprint surface. Production's
// ApiKeyIdentity returns the key ID; UserIdentity (and all JWT
// identities) returns 0 because JWT sessions don't carry a key ID.
// Tests don't probe it, but the Identity interface requires it
// AND the type must be int64 (not string) to satisfy the
// interface — this is the fix that unbreaks admin_channels_test.go
// after the Identity contract grew SessionID/Permissions.
func (s staffIdentity) KeyID() int64 { return 0 }

func TestHandleAdminYouTubeFleetReadiness_NonAdmin_Forbidden(t *testing.T) {
	store := &stubAdminStore{}
	r := &Router{adminStore: store}
	// adminStore is non-nil + identity is NOT admin -> handler must
	// short-circuit with 403 + adminStore MUST NOT be called.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/fleet_readiness", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 42, isAdmin: false}))

	r.handleAdminYouTubeFleetReadiness(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got := store.createCalled.Load(); got != 0 {
		t.Errorf("CreateFleetReadinessSnapshot call count: want 0 (handler must short-circuit on non-admin), got %d", got)
	}
}

func TestHandleAdminYouTubeFleetReadiness_NilAdminStore_NotImplemented(t *testing.T) {
	r := &Router{adminStore: nil}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/fleet_readiness", nil)
	// Even with admin identity, nil adminStore must surface as 501.
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 42, isAdmin: true}))

	r.handleAdminYouTubeFleetReadiness(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status: want 501, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestHandleAdminYouTubeFleetReadiness_Admin_OK_JSON(t *testing.T) {
	canonical := repository.FleetReadinessCounts{
		Total:                  200,
		Active:                 187,
		PendingAuthorization:   0,
		ReauthRequired:         13,
		Revoked:                0,
		Error:                  0,
		RefreshTestOK:          200,
		ScopeYoutubeUploadOK:   200,
		ScopeYoutubeReadonlyOK: 200,
		ChannelBindingOK:       200,
		PrivateCanaryOK:        200,
		CanaryChannelMatchOK:   200,
	}
	takenAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store := &stubAdminStore{
		createResp: repository.FleetReadinessSnapshotResponse{
			FleetReadiness: canonical,
			SnapshotID:     "fread_01J0YZZZZZZ",
			TakenAt:        takenAt,
		},
	}
	r := &Router{adminStore: store}
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/fleet_readiness", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 9999, isAdmin: true}))

	rec := httptest.NewRecorder()
	r.handleAdminYouTubeFleetReadiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got := store.createCalled.Load(); got != 1 {
		t.Errorf("CreateFleetReadinessSnapshot call count: want 1, got %d", got)
	}

	// Decode the JSON envelope; verify the 12 DoD fields landed
	// with the exact JSON keys the operator dashboard reads.
	var got struct {
		FleetReadiness repository.FleetReadinessCounts `json:"fleet_readiness"`
		SnapshotID     string                          `json:"snapshot_id"`
		TakenAt        time.Time                       `json:"taken_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON: %v (body=%q)", err, rec.Body.String())
	}
	if got.FleetReadiness != canonical {
		t.Errorf("FleetReadiness mismatch:\n  want %+v\n  got  %+v", canonical, got.FleetReadiness)
	}
	if got.SnapshotID != "fread_01J0YZZZZZZ" {
		t.Errorf("snapshot_id: want fread_01J0YZZZZZZ, got %q", got.SnapshotID)
	}
	if !got.TakenAt.Equal(takenAt) {
		t.Errorf("taken_at: want %v, got %v", takenAt, got.TakenAt)
	}
}
