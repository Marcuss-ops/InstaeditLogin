package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type driveDeliveryTestProvider struct {
	result *models.DeliveryResult
}

func (p *driveDeliveryTestProvider) Name() string { return models.PlatformGoogleDrive }

func (p *driveDeliveryTestProvider) Deliver(context.Context, *models.MediaAsset, *models.DeliveryDestination, string) (*models.DeliveryResult, error) {
	return p.result, nil
}

func TestPublishDriveExportDoesNotPublishBeforeDelivery(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(int64) (bool, error) { return true, nil },
		findByIDFn: func(int64) (*models.Post, error) {
			return &models.Post{ID: 100, MediaURL: "https://cdn.example.test/video.mp4", Metadata: json.RawMessage(`{"folder_id":"folder-1"}`)}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: models.PlatformGoogleDrive, PlatformUserID: "drive-user"}, nil
		},
	}
	vault := &mockCredentialVault{}
	provider := &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformGoogleDrive}}
	w := newTestWorker(posts, users, models.PlatformGoogleDrive, provider, vault)
	registry := services.NewDeliveryRegistry()
	if err := registry.Register(&driveDeliveryTestProvider{result: &models.DeliveryResult{
		ProviderName: models.PlatformGoogleDrive,
		Status:       "retrying",
		Metadata:     map[string]string{"error_code": "drive_auth_required"},
	}}); err != nil {
		t.Fatal(err)
	}
	w.WithDeliveryRegistry(registry)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget: expected blocked auth error")
	}
	if posts.updateCalls != 1 || posts.updateTargets[0].Status != models.PostStatusBlockedAuth {
		t.Fatalf("target transition: got %d calls, status=%q; want one blocked_auth transition", posts.updateCalls, posts.updateTargets[0].Status)
	}
}
