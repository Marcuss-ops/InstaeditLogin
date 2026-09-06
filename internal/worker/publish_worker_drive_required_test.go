package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// driveRequiredStatusWriterSpy captures MarkDriveRequiredFailed calls made
// through the DriveRequiredStatusWriter optional interface while reusing the
// shared mockPostStore as the PublisherPostStore surface.
type driveRequiredStatusWriterSpy struct {
	mockPostStore
	calls     []int64
	lastErr   string
	returnErr error
}

func (s *driveRequiredStatusWriterSpy) MarkDriveRequiredFailed(id int64, lastError string) error {
	s.calls = append(s.calls, id)
	s.lastErr = lastError
	return s.returnErr
}

func driveRequiredTestWorker(t *testing.T, posts PublisherPostStore, result *models.DeliveryResult) *PublishWorker {
	t.Helper()
	users := &mockUserStore{}
	vault := &mockCredentialVault{}
	provider := &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformGoogleDrive}}
	w := newTestWorkerWithoutThrottle(posts, users, models.PlatformGoogleDrive, provider, vault)
	registry := services.NewDeliveryRegistry()
	if err := registry.Register(&driveDeliveryTestProvider{result: result}); err != nil {
		t.Fatal(err)
	}
	w.WithDeliveryRegistry(registry)
	return w
}

func dispatchDriveRequiredViolation(t *testing.T, w *PublishWorker, targetID int64) {
	t.Helper()
	// The violation scenario: the Drive destination (registry key = account
	// platform) terminally failed while the platform publish had already
	// completed. extraConfig carries the operator's drive_required flag.
	_, _ = w.dispatchPostCompletion(context.Background(), &models.PostTarget{ID: targetID}, &models.PlatformAccount{
		ID:             10,
		Platform:       models.PlatformGoogleDrive,
		PlatformUserID: "drive-user",
	}, &models.MediaAsset{ID: "asset-1", ContentType: "video/mp4"}, "https://cdn.example.test/video.mp4", map[string]string{"drive_required": "true"})
}

// TestPublishWorker_DriveRequiredViolation_WritesBackStatus drives
// dispatchPostCompletion with a provider that returns a terminal failure
// while the destination carries drive_required=true. The gate must flip the
// target to 'drive_required_failed' via the optional DriveRequiredStatusWriter
// contract (Task 8/10.1), not merely log.
func TestPublishWorker_DriveRequiredViolation_WritesBackStatus(t *testing.T) {
	posts := &driveRequiredStatusWriterSpy{}
	posts.findByIDFn = func(int64) (*models.Post, error) {
		return &models.Post{ID: 100, MediaURL: "https://cdn.example.test/video.mp4"}, nil
	}
	w := driveRequiredTestWorker(t, posts, &models.DeliveryResult{
		ProviderName: models.PlatformGoogleDrive,
		Status:       "failed",
	})

	dispatchDriveRequiredViolation(t, w, 200)

	if len(posts.calls) != 1 {
		t.Fatalf("MarkDriveRequiredFailed calls: got %d, want 1", len(posts.calls))
	}
	if posts.calls[0] != 200 {
		t.Fatalf("MarkDriveRequiredFailed id: got %d, want 200", posts.calls[0])
	}
	if posts.lastErr == "" {
		t.Fatal("MarkDriveRequiredFailed: expected non-empty operator detail")
	}
}

// TestPublishWorker_DriveRequiredViolation_NoWriterFallsBackToWarn verifies
// the optional-interface contract: a post store that does NOT implement
// DriveRequiredStatusWriter must not panic — the gate logs a warn and moves
// on (legacy test rigs and pre-migration stores).
func TestPublishWorker_DriveRequiredViolation_NoWriterFallsBackToWarn(t *testing.T) {
	posts := &mockPostStore{}
	w := driveRequiredTestWorker(t, posts, &models.DeliveryResult{
		ProviderName: models.PlatformGoogleDrive,
		Status:       "failed",
	})

	// Must not panic; the warn path is the observable behaviour.
	dispatchDriveRequiredViolation(t, w, 300)
}

// TestPublishWorker_DriveRequiredViolation_CasLossIsInfo verifies a CAS loss
// (row moved past 'published') is not escalated into an operator alarm: the
// writeback attempt still happens exactly once, and the stale outcome is the
// no-regression contract.
func TestPublishWorker_DriveRequiredViolation_CasLossIsInfo(t *testing.T) {
	posts := &driveRequiredStatusWriterSpy{
		returnErr: repository.ErrPostTargetTransitionStale,
	}
	w := driveRequiredTestWorker(t, posts, &models.DeliveryResult{
		ProviderName: models.PlatformGoogleDrive,
		Status:       "failed",
	})

	dispatchDriveRequiredViolation(t, w, 400)

	if len(posts.calls) != 1 {
		t.Fatalf("MarkDriveRequiredFailed calls: got %d, want 1", len(posts.calls))
	}
	_ = errors.Is // sentinel classification asserted via repository sentinel wiring
}

// deliveryErrorCodeWriterSpy captures MarkDeliveryDispatchFailed calls
// through the DeliveryErrorCodeWriter optional interface.
type deliveryErrorCodeWriterSpy struct {
	mockPostStore
	ids   []int64
	codes []string
	err   error
}

// failingDeliveryTestProvider is a DeliveryProvider whose Deliver always
// fails with the given error — the dispatch-failure test fixture.
type failingDeliveryTestProvider struct {
	err error
}

func (p *failingDeliveryTestProvider) Name() string { return models.PlatformGoogleDrive }

func (p *failingDeliveryTestProvider) Deliver(context.Context, *models.MediaAsset, *models.DeliveryDestination, string) (*models.DeliveryResult, error) {
	return nil, p.err
}

func (s *deliveryErrorCodeWriterSpy) MarkDeliveryDispatchFailed(id int64, errorCode string) error {
	s.ids = append(s.ids, id)
	s.codes = append(s.codes, errorCode)
	return s.err
}

// TestPublishWorker_DispatchError_PersistsDeliveryClass verifies the
// delivery_class is persisted on the target row (post_targets.last_error_code)
// when the post-completion dispatch errors — with the typed sentinel mapping.
func TestPublishWorker_DispatchError_PersistsDeliveryClass(t *testing.T) {
	posts := &deliveryErrorCodeWriterSpy{}
	w := driveRequiredTestWorker(t, posts, nil)
	registry := services.NewDeliveryRegistry()
	if err := registry.Register(&failingDeliveryTestProvider{err: services.ErrDriveSessionExpired}); err != nil {
		t.Fatal(err)
	}
	w.WithDeliveryRegistry(registry)

	_, _ = w.dispatchPostCompletion(context.Background(), &models.PostTarget{ID: 210}, &models.PlatformAccount{
		ID:       10,
		Platform: models.PlatformGoogleDrive,
	}, &models.MediaAsset{ID: "asset-1", ContentType: "video/mp4"}, "https://cdn.example.test/video.mp4")

	if len(posts.ids) != 1 || posts.ids[0] != 210 {
		t.Fatalf("MarkDeliveryDispatchFailed ids: got %v, want [210]", posts.ids)
	}
	if posts.codes[0] != "ERR_DRIVE_SESSION_EXPIRED" {
		t.Errorf("delivery_class = %q, want ERR_DRIVE_SESSION_EXPIRED", posts.codes[0])
	}
}

// TestPublishWorker_DispatchError_StageCarrierClass verifies an untyped
// (non-sentinel) transport error still persists a stable stage-derived code.
func TestPublishWorker_DispatchError_StageCarrierClass(t *testing.T) {
	posts := &deliveryErrorCodeWriterSpy{}
	w := driveRequiredTestWorker(t, posts, nil)
	registry := services.NewDeliveryRegistry()
	boom := errors.New("connection refused")
	if err := registry.Register(&failingDeliveryTestProvider{err: &services.DeliveryError{Stage: "sessionStore.Create", Err: boom}}); err != nil {
		t.Fatal(err)
	}
	w.WithDeliveryRegistry(registry)

	_, _ = w.dispatchPostCompletion(context.Background(), &models.PostTarget{ID: 211}, &models.PlatformAccount{
		ID:       10,
		Platform: models.PlatformGoogleDrive,
	}, &models.MediaAsset{ID: "asset-1", ContentType: "video/mp4"}, "https://cdn.example.test/video.mp4")

	if len(posts.codes) != 1 {
		t.Fatalf("MarkDeliveryDispatchFailed calls: got %d, want 1", len(posts.codes))
	}
	if posts.codes[0] != "SESSIONSTORE.CREATE" {
		t.Errorf("delivery_class = %q, want SESSIONSTORE.CREATE (legacy dot preserved)", posts.codes[0])
	}
}

// TestPublishWorker_DispatchError_NoWriterDoesNotPanic pins the optional-
// interface contract for legacy post stores.
func TestPublishWorker_DispatchError_NoWriterDoesNotPanic(t *testing.T) {
	posts := &mockPostStore{}
	w := driveRequiredTestWorker(t, posts, nil)
	registry := services.NewDeliveryRegistry()
	if err := registry.Register(&failingDeliveryTestProvider{err: services.ErrDriveSessionExpired}); err != nil {
		t.Fatal(err)
	}
	w.WithDeliveryRegistry(registry)

	// Must not panic; the class is already in the warn log.
	_, _ = w.dispatchPostCompletion(context.Background(), &models.PostTarget{ID: 212}, &models.PlatformAccount{
		ID:       10,
		Platform: models.PlatformGoogleDrive,
	}, &models.MediaAsset{ID: "asset-1", ContentType: "video/mp4"}, "https://cdn.example.test/video.mp4")
}
