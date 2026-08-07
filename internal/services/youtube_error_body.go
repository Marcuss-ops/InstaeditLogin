package services

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// init registers the YouTube error-body parser with the shared
// classifier's per-provider hook. The error infrastructure
// (provider_error.go) therefore never hard-codes the YouTube Data API
// error shape — the YouTube domain owns it here, which keeps the
// boundary clean for a future extraction of youtube_*.go into its own
// package.
func init() {
	RegisterProviderErrorBodyParser(models.PlatformYouTube, parseYouTubeErrorBody)
}

// parseYouTubeErrorBody extracts the YouTube Data API error fields:
//   - error.errors[0].reason (e.g. "quotaExceeded", "channelClosed",
//     "processingFailed", "uploadLimitExceeded")
//   - x-request-id header (the platform's request id)
//
// The `reason` is the actionable code; the top-level error.message
// is human-readable but we don't surface it in SafeMessage (it can
// contain user-supplied content from upload metadata).
func parseYouTubeErrorBody(body string, h http.Header) (string, string, time.Duration) {
	requestID := firstNonEmpty(h.Get("x-request-id"), h.Get("X-Request-Id"))
	var parsed struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	providerCode := ""
	if len(parsed.Error.Errors) > 0 {
		providerCode = parsed.Error.Errors[0].Reason
	}
	return providerCode, requestID, 0
}
