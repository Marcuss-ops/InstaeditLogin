package api

// OAuth pool-aware contract types and the callback test seam.

import (
	"context"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"strings"
)

type YouTubePoolAwareLogin interface {
	GetLoginURLWithPoolClient(state string, options services.OAuthLoginOptions, client *services.YouTubeOAuthClientConfig) string
}

type YouTubePoolAwareCallback interface {
	HandleCallbackWithClient(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error)
}

func (r *Router) HandleOAuthCallbackRouteForTest() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The production route is registered by chi as
		// /api/v1/auth/{provider}/callback, which populates
		// req.PathValue("provider") before invoking handleCallback.
		// This seam intentionally bypasses that middleware/route stack,
		// so reproduce only the route-param binding needed by the handler.
		// Preserve an explicitly supplied path value for tests that want
		// to exercise a custom route context.
		if req.PathValue("provider") == "" {
			const (
				prefix = "/api/v1/auth/"
				suffix = "/callback"
			)
			requestPath := req.URL.Path
			if strings.HasPrefix(requestPath, prefix) && strings.HasSuffix(requestPath, suffix) {
				provider := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
				if provider != "" && !strings.Contains(provider, "/") {
					req.SetPathValue("provider", provider)
				}
			}
		}
		r.handleCallback(w, req)
	})
}
