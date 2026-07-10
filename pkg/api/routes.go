package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Router handles HTTP routing and request dispatching for all platforms.
type Router struct {
	platforms     map[string]services.PlatformService
	userRepo      *repository.UserRepository
	auth          *auth.Manager
	strictAuth    bool
	frontendURL   string
	allowedOrigin []string
}

// NewRouter creates a new Router with platform providers and an auth manager.
// strictAuth controls whether protected endpoints require a valid JWT.
// frontendURL, when non-empty, redirects OAuth callbacks to this origin's
// /auth/callback route with the JWT in query params. When empty, the callback
// returns JSON (useful for non-browser clients).
func NewRouter(
	platforms map[string]services.PlatformService,
	userRepo *repository.UserRepository,
	authMgr *auth.Manager,
	strictAuth bool,
	frontendURL string,
	allowedOrigins []string,
) *Router {
	return &Router{
		platforms:     platforms,
		userRepo:      userRepo,
		auth:          authMgr,
		strictAuth:    strictAuth,
		frontendURL:   frontendURL,
		allowedOrigin: allowedOrigins,
	}
}

// Setup registers all HTTP routes and returns the handler.
func (r *Router) Setup() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", r.handleHealth)
	mux.HandleFunc("GET /api/v1/auth/{provider}/login", r.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/{provider}/callback", r.handleCallback)
	mux.HandleFunc("POST /api/v1/posts/publish", r.protected(r.handlePublishPost))
	mux.HandleFunc("GET /api/v1/accounts", r.protected(r.handleListAccounts))
	mux.HandleFunc("GET /api/v1/metrics", r.handleMetrics)

	return r.corsMiddleware(r.loggingMiddleware(mux))
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	platforms := make([]string, 0, len(r.platforms))
	for p := range r.platforms {
		platforms = append(platforms, p)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"service":   "InstaEditLogin",
		"version":   "2.0.0",
		"platforms": platforms,
	})
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.platforms[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}

	state := req.URL.Query().Get("state")
	if state == "" {
		state = provider + "_default"
	}

	http.Redirect(w, req, p.GetLoginURL(state), http.StatusFound)
}

func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.platforms[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}

	code := req.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	state := req.URL.Query().Get("state")
	slog.Info("OAuth callback received", "provider", provider, "state", state)

	profile, tokenData, err := p.HandleCallback(req.Context(), state, code)
	if err != nil {
		slog.Error("OAuth callback failed", "provider", provider, "error", err)
		metrics.RecordOAuthLoginError(provider, metrics.ErrorKind(err))
		writeError(w, http.StatusInternalServerError, "authentication failed: "+err.Error())
		return
	}
	metrics.RecordOAuthLoginSuccess(provider)

	user, account, err := r.userRepo.FindOrCreateUserByPlatform(profile, provider)
	if err != nil {
		slog.Error("Failed to upsert user", "provider", provider, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save user: "+err.Error())
		return
	}

	if err := p.SaveEncryptedToken(account.ID, tokenData); err != nil {
		slog.Error("Failed to save token", "provider", provider, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save token: "+err.Error())
		return
	}

	jwtToken, _, jwtExp, err := r.auth.Issue(user.ID)
	if err != nil {
		slog.Error("Failed to issue JWT", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}
	metrics.IncJWTIssued()

	expiresAt := jwtExp.UTC().Format(time.RFC3339)

	// When FrontendURL is configured, redirect the browser to the SPA callback
	// page. The JWT is passed via query string; the SPA is responsible for
	// moving it out of the URL into localStorage and redirecting again with
	// replace:true (so it never lands in browser history).
	//
	// When FrontendURL is empty, fall back to the original JSON response so
	// non-browser clients (curl, Postman, Go integration tests) keep working.
	if r.frontendURL != "" {
		q := url.Values{}
		q.Set("jwt", jwtToken)
		q.Set("provider", provider)
		q.Set("user_id", fmt.Sprintf("%d", user.ID))
		q.Set("name", user.Name)
		q.Set("username", account.Username)
		q.Set("expires_at", expiresAt)
		redirectURL := strings.TrimRight(r.frontendURL, "/") + "/auth/callback?" + q.Encode()
		slog.Info("OAuth callback redirect", "provider", provider, "to", redirectURL)
		http.Redirect(w, req, redirectURL, http.StatusFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "authenticated",
		"provider":       provider,
		"user_id":        user.ID,
		"name":           user.Name,
		"jwt_token":      jwtToken,
		"jwt_expires_at": expiresAt,
		"account": map[string]interface{}{
			"id":               account.ID,
			"platform":         account.Platform,
			"platform_user_id": account.PlatformUserID,
			"username":         account.Username,
		},
	})
}

type PublishRequest struct {
	UserID       int64  `json:"user_id"`
	Platform     string `json:"platform"`
	MediaURL     string `json:"media_url"`
	Caption      string `json:"caption"`
	ContentType  string `json:"content_type"`
	Title        string `json:"title"`

	// TikTok-specific post options. Ignored by other platforms.
	// PrivacyLevel: "PUBLIC_TO_EVERYONE" | "MUTUAL_FOLLOW_FRIENDS" | "SELF_ONLY"
	// CommentMode:   "allow_all" (default) | "no_comments"
	// DuetMode:      "allow" (default) | "no_duet"
	PrivacyLevel string `json:"privacy_level,omitempty"`
	CommentMode  string `json:"comment_mode,omitempty"`
	DuetMode     string `json:"duet_mode,omitempty"`
}

// protected wraps a handler so that the JWT middleware runs first.
//
// In strict mode (default), missing/invalid Authorization causes a 401 and
// the handler is never reached. In legacy rollback mode (STRICT_JWT_AUTH=false)
// the handler still runs; the resolved user id comes from the JWT context
// when present, or from `user_id` in the body/query otherwise — see
// resolveUserID for the precedence rules.
func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.auth.Middleware(r.strictAuth, next).ServeHTTP(w, req)
	}
}

func (r *Router) handlePublishPost(w http.ResponseWriter, req *http.Request) {
	var pubReq PublishRequest
	if err := json.NewDecoder(req.Body).Decode(&pubReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	userID := resolveUserID(req, pubReq.UserID, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	result, err := r.publishContent(req.Context(), userID, &pubReq)
	if err != nil {
		var pe *publishError
		if errors.As(err, &pe) {
			writeError(w, pe.status, pe.message)
		} else {
			slog.Error("Failed to publish", "error", err, "platform", pubReq.Platform)
			writeError(w, http.StatusInternalServerError, "failed to publish: "+err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":            "published",
		"platform":          pubReq.Platform,
		"platform_media_id": result.PlatformMediaID,
		"platform_url":      result.PlatformURL,
	})
}

// publishContent resolves the platform, account, and token, builds the
// payload, and calls the platform's Publish method. All the publish
// business logic lives here so handlePublishPost stays a thin handler.
func (r *Router) publishContent(ctx context.Context, userID int64, pubReq *PublishRequest) (*models.PublishResult, error) {
	if pubReq.Platform == "" {
		pubReq.Platform = models.PlatformMeta
	}

	p, ok := r.platforms[pubReq.Platform]
	if !ok {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("unsupported platform: %s", pubReq.Platform)}
	}

	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, pubReq.Platform)
	if err != nil || len(accounts) == 0 {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("no %s account linked to this user", pubReq.Platform)}
	}
	account := accounts[0]

	// Try bearer token first (refresh-capable), then long-lived (Meta).
	oauthToken, err := p.EnsureFreshToken(ctx, account.ID, models.TokenTypeBearer, p.RefreshOAuthToken)
	if err != nil {
		if oauthToken, err = p.EnsureFreshToken(ctx, account.ID, models.TokenTypeLongLived, p.RefreshOAuthToken); err != nil {
			slog.Error("Failed to obtain a usable token after refresh",
				"error", err, "user_id", userID, "platform", pubReq.Platform)
			return nil, &publishError{http.StatusUnauthorized, "no valid token (refresh failed; please re-authenticate): " + err.Error()}
		}
	}

	payload := models.PublishPayload{
		Text:         pubReq.Caption,
		Title:        pubReq.Title,
		PrivacyLevel: pubReq.PrivacyLevel,
		CommentMode:  pubReq.CommentMode,
		DuetMode:     pubReq.DuetMode,
	}

	switch pubReq.ContentType {
	case "video", "reel":
		payload.VideoURL = pubReq.MediaURL
	case "image", "photo":
		payload.ImageURL = pubReq.MediaURL
	case "text":
		// text-only post
	default:
		return nil, &publishError{http.StatusBadRequest, "content_type must be one of: image, video, text"}
	}

	return p.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
}

// publishError is an error that carries an HTTP status code so the handler
// can return the appropriate response without leaking HTTP details into the
// business logic.
type publishError struct {
	status  int
	message string
}

func (e *publishError) Error() string { return e.message }

func (r *Router) handleListAccounts(w http.ResponseWriter, req *http.Request) {
	fallback := int64(0)
	if s := req.URL.Query().Get("user_id"); s != "" {
		var n int64
		if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
			fallback = n
		}
	}
	userID := resolveUserID(req, fallback, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusBadRequest, "user_id query parameter required")
		return
	}

	platform := req.URL.Query().Get("platform")

	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, platform)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}

	if accounts == nil {
		accounts = []*models.PlatformAccount{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"accounts": accounts,
	})
}

func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.Info("HTTP request",
			"method", req.Method,
			"path", req.URL.Path,
			"remote_addr", req.RemoteAddr,
		)
		next.ServeHTTP(w, req)
	})
}

// handleMetrics serves the Prometheus exposition format. By default the
// endpoint is open so scrapers (Prometheus, Datadog agents, etc.) running
// inside the VPC can read it without coordination. Set
// METRICS_BASIC_AUTH_USER + METRICS_BASIC_AUTH_PASS in the environment to
// gate the endpoint with HTTP Basic Auth (constant-time comparison).
func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	// Both env vars must be configured; otherwise the endpoint is open
	// (this matches the Prometheus "scrape from inside the VPC" default).
	// Only gate when BOTH are set so a half-configured deployment doesn't
	// accidentally accept Authorization headers with an empty cred half.
	user := os.Getenv("METRICS_BASIC_AUTH_USER")
	pass := os.Getenv("METRICS_BASIC_AUTH_PASS")
	if user != "" && pass != "" {
		u, p, ok := req.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	metrics.Handler().ServeHTTP(w, req)
}

// corsMiddleware adds CORS headers for browser clients (the React SPA).
// We allow the configured frontend origin (and any extra origins from
// CORS_ALLOWED_ORIGINS) and only expose headers/methods the API actually
// uses. Preflight OPTIONS requests are short-circuited with 204.
func (r *Router) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(r.allowedOrigin))
	for _, o := range r.allowedOrigin {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		origin := req.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Credentials", "false")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// resolveUserID prefers the authenticated user id placed in the request
// context by the JWT middleware. In strict mode that's the only source: if
// no JWT was presented, middleware already rejected with 401 and this
// function should never see user_id == 0 here. The defensive `if strict`
// branch therefore returns 0.
//
// In LEGACY mode (STRICT_JWT_AUTH=false) the function falls back to the
// caller-provided value (typically the `user_id` JSON field or query
// parameter). This is what lets old clients that don't send Authorization
// headers keep working during the migration window.
func resolveUserID(req *http.Request, fallback int64, strict bool) int64 {
	if uid, ok := auth.UserIDFromContext(req.Context()); ok && uid > 0 {
		return uid
	}
	if strict {
		return 0
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
