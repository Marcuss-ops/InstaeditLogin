package services

import (
	"net/http"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestParseYouTubeErrorBody: error.errors[0].reason becomes the
// ProviderCode (e.g. "quotaExceeded").
func TestParseYouTubeErrorBody(t *testing.T) {
	h := http.Header{}
	h.Set("x-request-id", "youtube-req-123")
	body := `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`
	code, reqID, _ := parseYouTubeErrorBody(body, h)
	if code != "quotaExceeded" {
		t.Errorf("ProviderCode: want %q, got %q", "quotaExceeded", code)
	}
	if reqID != "youtube-req-123" {
		t.Errorf("RequestID: want %q, got %q", "youtube-req-123", reqID)
	}
}

// TestParseYouTubeErrorBody_ProcessingFailed: the YouTube
// processingFailed reason maps to media_processing_failed upstream
// (the worker interprets ProviderCode via the code, not the body).
func TestParseYouTubeErrorBody_ProcessingFailed(t *testing.T) {
	body := `{"error":{"errors":[{"reason":"processingFailed"}]}}`
	code, _, _ := parseYouTubeErrorBody(body, http.Header{})
	if code != "processingFailed" {
		t.Errorf("ProviderCode: want %q, got %q", "processingFailed", code)
	}
}

// TestRegisterProviderErrorBodyParser_YouTubeHook pins that the
// YouTube body parser is registered with the shared classifier's
// per-provider hook, so NewProviderError resolves YouTube error
// bodies through the registry (not the fallback switch). Behavior is
// identical to before the hook migration; this test guards the
// registration itself so a future refactor can't silently drop it.
func TestRegisterProviderErrorBodyParser_YouTubeHook(t *testing.T) {
	parser, ok := lookupProviderErrorBodyParser(models.PlatformYouTube)
	if !ok {
		t.Fatalf("YouTube body parser is not registered with the classifier hook")
	}
	h := http.Header{}
	h.Set("x-request-id", "youtube-req-456")
	body := `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`
	code, reqID, _ := parser(body, h)
	if code != "quotaExceeded" {
		t.Errorf("ProviderCode via hook: want %q, got %q", "quotaExceeded", code)
	}
	if reqID != "youtube-req-456" {
		t.Errorf("RequestID via hook: want %q, got %q", "youtube-req-456", reqID)
	}

	// End-to-end through the canonical constructor.
	pe := NewProviderError(models.PlatformYouTube, 403, body, h, nil)
	if pe.ProviderCode != "quotaExceeded" {
		t.Errorf("NewProviderError ProviderCode: want %q, got %q", "quotaExceeded", pe.ProviderCode)
	}
	if pe.RequestID != "youtube-req-456" {
		t.Errorf("NewProviderError RequestID: want %q, got %q", "youtube-req-456", pe.RequestID)
	}
}
