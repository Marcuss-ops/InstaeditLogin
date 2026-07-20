package worker

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestMockProvider_CanaryUpload_NilFn_ReturnsTypedError(t *testing.T) {
	m := &mockProvider{}
	res, err := m.CanaryUpload(context.Background(), "tok", "UCexpected")
	if res != nil {
		t.Fatalf("expected nil result when canaryUploadFn is unset; got %+v", res)
	}
	if err != services.ErrYouTubeCanaryRejected {
		t.Fatalf("expected ErrYouTubeCanaryRejected; got %v", err)
	}
	if got, want := m.canaryUploadCalls, 1; got != want {
		t.Fatalf("canaryUploadCalls = %d, want %d", got, want)
	}
}

func TestMockProvider_CanaryUpload_FnReturnsResult(t *testing.T) {
	expected := &services.CanaryUploadResult{
		VideoID:           "canary-vid-123",
		UploadedChannelID: "UCexpected",
	}
	m := &mockProvider{
		canaryUploadFn: func(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error) {
			return expected, nil
		},
	}
	res, err := m.CanaryUpload(context.Background(), "tok", "UCexpected")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.VideoID != "canary-vid-123" || res.UploadedChannelID != "UCexpected" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestMockProvider_CanaryUpload_CounterIncrementsPerCall(t *testing.T) {
	m := &mockProvider{}
	for i := 0; i < 3; i++ {
		_, _ = m.CanaryUpload(context.Background(), "tok", "UCexpected")
	}
	if got, want := m.canaryUploadCalls, 3; got != want {
		t.Fatalf("canaryUploadCalls = %d, want %d", got, want)
	}
}
