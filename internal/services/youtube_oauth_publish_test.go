package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestYouTubeAsyncPublish_EncodeDecodePublishID verifies the composite
// publishID encoding and decoding used to carry the channel ID through
// the async publishing lifecycle.
func TestYouTubeAsyncPublish_EncodeDecodePublishID(t *testing.T) {
	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	if publishID != "UCexpected:yt-async-video-id" {
		t.Errorf("publishID: want UCexpected:yt-async-video-id, got %q", publishID)
	}

	channelID, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if channelID != "UCexpected" {
		t.Errorf("channelID: want UCexpected, got %q", channelID)
	}
	if videoID != "yt-async-video-id" {
		t.Errorf("videoID: want yt-async-video-id, got %q", videoID)
	}

	// Invalid inputs.
	for _, invalid := range []string{"", "nocolon", ":", "channel:", ":video"} {
		if _, _, err := decodeYouTubePublishID(invalid); err == nil {
			t.Errorf("expected error for %q", invalid)
		}
	}
}

// TestYouTubeAsyncPublish_Reconcile_Processing_ReturnsNil verifies that
// Reconcile returns (nil, nil) while the video is still processing.
func TestYouTubeAsyncPublish_Reconcile_Processing_ReturnsNil(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "processing"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected in-flight (nil result), got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Succeeded_ReturnsResult verifies that
// Reconcile returns the public video URL once processing succeeds.
func TestYouTubeAsyncPublish_Reconcile_Succeeded_ReturnsResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res == nil {
		t.Fatal("expected success result, got nil")
	}
	if res.PlatformMediaID != "yt-async-video-id" {
		t.Errorf("PlatformMediaID: want yt-async-video-id, got %q", res.PlatformMediaID)
	}
	wantURL := "https://www.youtube.com/watch?v=yt-async-video-id"
	if res.PlatformURL != wantURL {
		t.Errorf("PlatformURL: want %q, got %q", wantURL, res.PlatformURL)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Failed_ReturnsError verifies that a
// failed processing status is treated as a terminal failure.
func TestYouTubeAsyncPublish_Reconcile_Failed_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "failed"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected terminal error for failed status, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on failure, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_ChannelMismatch_ReturnsError verifies
// that Reconcile fails when the video landed on a different channel.
func TestYouTubeAsyncPublish_Reconcile_ChannelMismatch_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCother"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected channel mismatch error, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on mismatch, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Terminated_ReturnsError verifies that a
// terminated processing status is treated as a terminal failure.
func TestYouTubeAsyncPublish_Reconcile_Terminated_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "terminated"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected terminal error for terminated status, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on termination, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_ContinuePublish_NoOp verifies that ContinuePublish
// is a no-op for YouTube.
func TestYouTubeAsyncPublish_ContinuePublish_NoOp(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ContinuePublish(context.Background(), "token", "UCexpected:yt-async-video-id"); err != nil {
		t.Fatalf("ContinuePublish should be a no-op, got error: %v", err)
	}
}

// TestYouTubeAsyncPublish_Reconcile_MissingChannelID_ReturnsError verifies
// that Reconcile fails when the API response omits the channel ID.
func TestYouTubeAsyncPublish_Reconcile_MissingChannelID_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: ""},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected channel mismatch error for missing channelId, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on missing channelId, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_CheckPublishStatus_ReturnsStatus verifies that
// CheckPublishStatus returns the processing status string.
func TestYouTubeAsyncPublish_CheckPublishStatus_ReturnsStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	state, err := svc.CheckPublishStatus(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("CheckPublishStatus returned error: %v", err)
	}
	if state != "succeeded" {
		t.Errorf("state: want succeeded, got %q", state)
	}
}

// TestYouTubeBuildUploadMetadata_ScheduledPublish sets privacy to private
// and includes publishAt when PublishAt is in the future.
func TestYouTubeBuildUploadMetadata_ScheduledPublish(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	future := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	payload := models.PublishPayload{
		Title:        "Scheduled Video",
		Text:         "A scheduled video",
		PrivacyLevel: "public",
		PublishAt:    &future,
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "private" {
		t.Errorf("privacyStatus: want private, got %q", status["privacyStatus"])
	}
	if status["publishAt"] != future.UTC().Format(time.RFC3339) {
		t.Errorf("publishAt: want %q, got %q", future.UTC().Format(time.RFC3339), status["publishAt"])
	}
}

// TestYouTubeBuildUploadMetadata_ImmediatePublish uses the requested
// privacy level when no future PublishAt is set.
func TestYouTubeBuildUploadMetadata_ImmediatePublish(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	payload := models.PublishPayload{
		Title:        "Immediate Video",
		Text:         "An immediate video",
		PrivacyLevel: "unlisted",
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "unlisted" {
		t.Errorf("privacyStatus: want unlisted, got %q", status["privacyStatus"])
	}
	if _, exists := status["publishAt"]; exists {
		t.Errorf("publishAt should not be set for immediate publishes")
	}
}

// TestYouTubeBuildUploadMetadata_PastPublishAt_Ignored verifies that a
// PublishAt in the past is ignored and the requested privacy is used.
func TestYouTubeBuildUploadMetadata_PastPublishAt_Ignored(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	past := time.Now().UTC().Add(-2 * time.Hour)
	payload := models.PublishPayload{
		Title:        "Past Video",
		Text:         "A past video",
		PrivacyLevel: "public",
		PublishAt:    &past,
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "public" {
		t.Errorf("privacyStatus: want public, got %q", status["privacyStatus"])
	}
	if _, exists := status["publishAt"]; exists {
		t.Errorf("publishAt should not be set for past timestamps")
	}
}
