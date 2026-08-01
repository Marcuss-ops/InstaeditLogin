package worker

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestPublishWorkerYouTubePhase2_NilLookupFallsThrough(t *testing.T) {
	w := &PublishWorker{}
	handled, err := w.publishYouTubePhase2(
		context.Background(),
		&models.PostTarget{ID: 1, PostID: 2},
		&models.PlatformAccount{Platform: models.PlatformYouTube},
		&models.Post{ID: 2},
		&models.OAuthToken{AccessToken: "token"},
		models.PublishPayload{},
	)
	if err != nil {
		t.Fatalf("publishYouTubePhase2 returned error: %v", err)
	}
	if handled {
		t.Fatal("publishYouTubePhase2 handled a target without a configured lookup")
	}
}

func TestIsOrphanedYouTubeVideo_NilErrorIsFalse(t *testing.T) {
	if isOrphanedYouTubeVideo(nil, "video-id") {
		t.Fatal("nil error must not classify a YouTube video as orphaned")
	}
}

func TestPublishWorkerBuildPayload_PrivacyCascadeRegression(t *testing.T) {
	worker := &PublishWorker{}
	cases := []struct {
		name     string
		account  string
		override string
		batch    string
		want     string
	}{
		{name: "post override wins", account: models.PlatformYouTube, override: "private", batch: "public", want: "private"},
		{name: "batch default", account: models.PlatformYouTube, batch: "unlisted", want: "unlisted"},
		{name: "youtube fallback", account: models.PlatformYouTube, want: "unlisted"},
		{name: "generic fallback", account: models.PlatformInstagram, want: "PUBLIC_TO_EVERYONE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := worker.buildPayload(
				&models.PlatformAccount{Platform: tc.account},
				&models.Post{PrivacyLevel: tc.override, DefaultPrivacyLevel: tc.batch},
				"idem-key",
			)
			if payload.PrivacyLevel != tc.want {
				t.Fatalf("privacy level = %q, want %q", payload.PrivacyLevel, tc.want)
			}
			if payload.IdempotencyKey != "idem-key" {
				t.Fatalf("idempotency key = %q, want idem-key", payload.IdempotencyKey)
			}
		})
	}
}
