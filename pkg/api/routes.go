package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Router handles HTTP routing and request dispatching for all platforms.
type Router struct {
	platforms  map[string]services.PlatformService
	userRepo   *repository.UserRepository
	auth       *auth.Manager
	strictAuth bool
}

// NewRouter creates a new Router with platform providers and an auth manager.
// strictAuth controls whether protected endpoints require a valid JWT.
func NewRouter(
	platforms map[string]services.PlatformService,
	userRepo *repository.UserRepository,
	authMgr *auth.Manager,
	strictAuth bool,
) *Router {
	return &Router{
		platforms:  platforms,
		userRepo:   userRepo,
		auth:       authMgr,
		strictAuth: strictAuth,
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

	return r.loggingMiddleware(mux)
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

	profile, tokenData, err := p.HandleCallback(code)
	if err != nil {
		slog.Error("OAuth callback failed", "provider", provider, "error", err)
		writeError(w, http.StatusInternalServerError, "authentication failed: "+err.Error())
		return
	}

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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "authenticated",
		"provider":       provider,
		"user_id":        user.ID,
		"name":           user.Name,
		"jwt_token":      jwtToken,
		"jwt_expires_at": jwtExp.UTC().Format(time.RFC3339),
		"account": map[string]interface{}{
			"id":               account.ID,
			"platform":         account.Platform,
			"platform_user_id": account.PlatformUserID,
			"username":         account.Username,
		},
	})
}

type PublishRequest struct {
	UserID      int64  `json:"user_id"`
	Platform    string `json:"platform"`
	MediaURL    string `json:"media_url"`
	Caption     string `json:"caption"`
	ContentType string `json:"content_type"`
	Title       string `json:"title"`
}

// protected wraps a handler so that the JWT middleware runs first.
// In strict mode, missing/invalid Authorization causes a 401. In lenient
// (rollback) mode, the handler is allowed to read user_id from body/query.
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
	if pubReq.Platform == "" {
		pubReq.Platform = models.PlatformMeta
	}

	p, ok := r.platforms[pubReq.Platform]
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported platform: "+pubReq.Platform)
		return
	}

	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, pubReq.Platform)
	if err != nil || len(accounts) == 0 {
		writeError(w, http.StatusNotFound, "no "+pubReq.Platform+" account linked to this user")
		return
	}
	account := accounts[0]

	// Try bearer token first (refresh-capable), then long-lived (Meta).
	oauthToken, err := p.EnsureFreshToken(req.Context(), account.ID, models.TokenTypeBearer, p.RefreshOAuthToken)
	if err != nil {
		if oauthToken, err = p.EnsureFreshToken(req.Context(), account.ID, models.TokenTypeLongLived, p.RefreshOAuthToken); err != nil {
			slog.Error("Failed to obtain a usable token after refresh",
				"error", err, "user_id", userID, "platform", pubReq.Platform)
			writeError(w, http.StatusUnauthorized,
				"no valid token (refresh failed; please re-authenticate): "+err.Error())
			return
		}
	}

	payload := models.PublishPayload{
		Text:  pubReq.Caption,
		Title: pubReq.Title,
	}

	switch pubReq.ContentType {
	case "video", "reel":
		payload.VideoURL = pubReq.MediaURL
	case "image", "photo":
		payload.ImageURL = pubReq.MediaURL
	case "text":
		// text-only post
	default:
		writeError(w, http.StatusBadRequest, "content_type must be one of: image, video, text")
		return
	}

	result, err := p.Publish(req.Context(), oauthToken.AccessToken, account.PlatformUserID, payload)
	if err != nil {
		slog.Error("Failed to publish", "error", err, "platform", pubReq.Platform)
		writeError(w, http.StatusInternalServerError, "failed to publish: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":            "published",
		"platform":          pubReq.Platform,
		"platform_media_id": result.PlatformMediaID,
		"platform_url":      result.PlatformURL,
	})
}

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

// resolveUserID prefers the JWT-authenticated user id placed in context by
// the middleware. In lenient (non-strict) mode it falls back to a value
// provided in the body/query for backwards compatibility during the rollout.
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
