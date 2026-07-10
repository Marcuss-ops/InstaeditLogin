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
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Router handles HTTP routing and request dispatching for all platforms.
type Router struct {
	platforms       map[string]services.PlatformService
	userRepo        UserStore
	auth            *auth.Manager
	strictAuth      bool
	frontendURL     string
	allowedOrigin   []string
	workspaceStore  WorkspaceStore
	postStore       PostStore
	storageProvider StorageProvider
	maxUploadBytes  int64
}

// UserStore abstracts the user/account persistence layer so tests can
// inject a mock without a real database.
type UserStore interface {
	FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error)
	ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error)
}

// NewRouter creates a new Router with platform providers and an auth manager.
// strictAuth controls whether protected endpoints require a valid JWT.
// frontendURL, when non-empty, redirects OAuth callbacks to this origin's
// /auth/callback route with the JWT in query params. When empty, the callback
// returns JSON (useful for non-browser clients).
func NewRouter(
	platforms map[string]services.PlatformService,
	userRepo UserStore,
	authMgr *auth.Manager,
	strictAuth bool,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) *Router {
	r := &Router{
		platforms:     platforms,
		userRepo:      userRepo,
		auth:          authMgr,
		strictAuth:    strictAuth,
		frontendURL:   frontendURL,
		allowedOrigin: allowedOrigins,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Setup registers all HTTP routes and returns the handler.
func (r *Router) Setup() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", r.handleHealth)
	mux.HandleFunc("GET /api/v1/auth/{provider}/login", r.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/{provider}/callback", r.handleCallback)
	mux.HandleFunc("POST /api/v1/posts/publish", r.protected(r.handlePublishPost))
	mux.HandleFunc("POST /api/v1/posts/publish-all", r.protected(r.handlePublishAll))
	mux.HandleFunc("GET /api/v1/accounts", r.protected(r.handleListAccounts))
	mux.HandleFunc("GET /api/v1/metrics", r.handleMetrics)

	// Workspaces + posts CRUD (commit feat(api): endpoints workspaces e posts)
	mux.HandleFunc("GET /api/v1/workspaces", r.protected(r.handleListWorkspaces))
	mux.HandleFunc("POST /api/v1/workspaces", r.protected(r.handleCreateWorkspace))
	mux.HandleFunc("GET /api/v1/workspaces/{id}", r.protected(r.handleGetWorkspace))
	mux.HandleFunc("POST /api/v1/posts", r.protected(r.handleCreatePost))
	mux.HandleFunc("GET /api/v1/workspaces/{id}/posts", r.protected(r.handleListWorkspacePosts))
	mux.HandleFunc("GET /api/v1/posts/{id}", r.protected(r.handleGetPost))

	// Presigned upload URLs for heavy media files (commit feat(storage):
	// presigned upload URLs per video pesanti / immagini grandi).
	mux.HandleFunc("POST /api/v1/storage/upload-url", r.protected(r.handleCreateUploadURL))

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
	PrivacyLevel string `json:"privacy_level,omitempty"`
	CommentMode  string `json:"comment_mode,omitempty"`
	DuetMode     string `json:"duet_mode,omitempty"`
}

// protected wraps a handler so that the JWT middleware runs first.
func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.auth.Middleware(r.strictAuth, next).ServeHTTP(w, req)
	}
}

func (r *Router) handlePublishAll(w http.ResponseWriter, req *http.Request) {
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

	// Validate content_type once before iterating platforms.
	if pubReq.ContentType != "text" && pubReq.ContentType != "image" && pubReq.ContentType != "photo" && pubReq.ContentType != "video" && pubReq.ContentType != "reel" {
		writeError(w, http.StatusBadRequest, "content_type must be one of: image, video, text")
		return
	}

	// Fetch ALL connected platform accounts for this user.
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, "")
	if err != nil || len(accounts) == 0 {
		writeError(w, http.StatusNotFound, "no connected accounts found for this user")
		return
	}

	type platformResult struct {
		Platform        string `json:"platform"`
		Status          string `json:"status"`
		PlatformMediaID string `json:"platform_media_id,omitempty"`
		PlatformURL     string `json:"platform_url,omitempty"`
		Error           string `json:"error,omitempty"`
	}
	results := make([]platformResult, 0, len(accounts))
	successCount, failCount := 0, 0

	for _, acc := range accounts {
		result, err := r.publishToAccount(req.Context(), acc, &pubReq)
		pr := platformResult{Platform: acc.Platform}
		if err != nil {
			pr.Status = "error"
			pr.Error = err.Error()
			failCount++
			slog.Warn("publish-all: failed for platform", "platform", acc.Platform, "error", err)
		} else {
			pr.Status = "published"
			pr.PlatformMediaID = result.PlatformMediaID
			pr.PlatformURL = result.PlatformURL
			successCount++
		}
		results = append(results, pr)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "completed",
		"success_count": successCount,
		"fail_count":    failCount,
		"results":       results,
	})
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
// payload, and calls the platform's Publish method.
func (r *Router) publishContent(ctx context.Context, userID int64, pubReq *PublishRequest) (*models.PublishResult, error) {
	if pubReq.Platform == "" {
		pubReq.Platform = models.PlatformMeta
	}

	if _, ok := r.platforms[pubReq.Platform]; !ok {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("unsupported platform: %s", pubReq.Platform)}
	}

	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, pubReq.Platform)
	if err != nil || len(accounts) == 0 {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("no %s account linked to this user", pubReq.Platform)}
	}

	return r.publishToAccount(ctx, accounts[0], pubReq)
}

// publishToAccount handles token refresh and publishing for a specific
// platform account that has already been fetched. Used by both
// publishContent (single-platform) and handlePublishAll (multi-platform).
func (r *Router) publishToAccount(ctx context.Context, account *models.PlatformAccount, pubReq *PublishRequest) (*models.PublishResult, error) {
	p, ok := r.platforms[account.Platform]
	if !ok {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("unsupported platform: %s", account.Platform)}
	}

	// Try bearer token first (refresh-capable), then long-lived (Meta).
	oauthToken, err := p.EnsureFreshToken(ctx, account.ID, models.TokenTypeBearer, p.RefreshOAuthToken)
	if err != nil {
		if oauthToken, err = p.EnsureFreshToken(ctx, account.ID, models.TokenTypeLongLived, p.RefreshOAuthToken); err != nil {
			slog.Error("Failed to obtain a usable token after refresh",
				"error", err, "platform", account.Platform)
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

// handleMetrics serves the Prometheus exposition format.
func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
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
