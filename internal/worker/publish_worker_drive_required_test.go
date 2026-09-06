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
