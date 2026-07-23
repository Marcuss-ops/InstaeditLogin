package services

import (
	"net/http/httptest"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

func youtubeTestCfg() *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{
			YouTubeClientID:     "test-youtube-client-id",
			YouTubeClientSecret: "test-youtube-client-secret-min32ch",
			YouTubeRedirectURI:  "http://localhost:8080/api/v1/auth/youtube/callback",
		},
	}
}

func newTestYouTubeService(srv *httptest.Server) *YouTubeOAuthService {
	cfg := youtubeTestCfg()
	svc, _ := NewYouTubeOAuthService(cfg, ProviderDependencies{HTTPClient: testClient(srv)})
	return svc
}

// --- helpers ---

func containsScope(scopes, target string) bool {
	for _, s := range splitScopes(scopes) {
		if s == target {
			return true
		}
	}
	return false
}

func splitScopes(scopes string) []string {
	var result []string
	for _, s := range splitBySpace(scopes) {
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func splitBySpace(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	return result
}

func containsPrompt(prompt, target string) bool {
	for _, p := range splitBySpace(prompt) {
		if p == target {
			return true
		}
	}
	return false
}

// =============================================================================
// P1#6 — Resumable upload chunk size + Retry-After-aware backoff
// =============================================================================

// youtubeTestSvcUpload wires a tiny chunk size + tiny backoff for the
// upload-loop tests so 200-byte files exercise multiple PUTs and the
// suite stays fast (real production defaults: 16 MB chunks, 1s/5m
// backoff — far too slow for unit tests). The cfg-driven load path
// is identical to production; only the values differ.
func youtubeTestSvcUpload(srv *httptest.Server) *YouTubeOAuthService {
	cfg := youtubeTestCfg()
	cfg.Worker.YouTubeUploadChunkBytes = 64
	cfg.Worker.YouTubeUploadMaxRetries = 3
	cfg.Worker.YouTubeUploadBackoffBaseMs = 1
	cfg.Worker.YouTubeUploadBackoffCapMs = 5
	svc, _ := NewYouTubeOAuthService(cfg, ProviderDependencies{HTTPClient: testClient(srv)})
	return svc
}
