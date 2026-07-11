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
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Router handles HTTP routing and request dispatching for all platforms.
//
// chi.NewRouter is used over stdlib http.ServeMux so the new /workspaces/{id}
// and /posts/{id}/... sub-routers can read URL params via chi.URLParam AND
// /auth/{provider}/login + /auth/{provider}/callback fall under the same
// wildcard pattern. chi is on go.mod (added by `go get github.com/go-chi/chi/v5`).
//
// Files in this package are split for readability:
//   - handlers.go (this file): Router struct, interfaces, NewRouter, Setup,
//     pre-existing routes (health, OAuth, publish, listAccounts, metrics,
//     CORS + logging middleware, helpers).
//   - workspaces.go: /api/v1/workspaces CRUD handlers. Require an injected
//     WorkspaceStore via WithWorkspaceStore; without it, each handler
//     returns 501 before touching the router.
//   - posts.go: /api/v1/posts CRUD handlers. Same pattern with PostStore.
//   - storage.go: /api/v1/storage/upload-url handler.
type Router struct {
	mux             *chi.Mux
	platforms       map[string]services.PlatformService
	userRepo        UserStore
	workspaceStore  WorkspaceStore
	postStore       PostStore
	storageProvider StorageProvider
	auth            *auth.Manager
	strictAuth      bool
	frontendURL     string
	allowedOrigin   []string
	maxUploadBytes  int64
}

// UserStore abstracts the user/account persistence layer so tests can
// inject a mock without a real database.
type UserStore interface {
	FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error)
	ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error)
}

// WorkspaceStore abstracts workspace persistence for the /workspaces CRUD
// handlers (workspaces.go). Includes Delete so handleDeleteWorkspace can
// map to 204 after ownership check passes.
type WorkspaceStore interface {
	Create(w *models.Workspace) error
	FindByID(id int64) (*models.Workspace, error)
	ListByOwner(ownerID int64) ([]models.Workspace, error)
	Delete(id int64) error
}

// PostStore abstracts post + post_target persistence for the /posts CRUD
// handlers (posts.go). Includes Update (used by handleSchedulePost) and
// Save (used by handleAddTarget).
//
// ListByPost was an unused candidate method from an earlier draft; it is
// not needed by any handler and not exposed here, so mock implementations
// don't have to satisfy it.
type PostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	FindByID(id int64) (*models.Post, error)
	Update(post *models.Post) error
	ListByWorkspace(workspaceID int64) ([]models.Post, error)
	Save(target *models.PostTarget) error
}

// StorageProvider abstracts presigned-URL minting so /api/v1/storage/upload-url
// stays storage-agnostic. The signature mirrors services.StorageProvider
// exactly so a real implementation can be passed through WithStorageProvider
// without adapter wrappers.
type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
}

// RouterOption mutates the Router at construction time so callers can
// inject optional dependencies without breaking the NewRouter signature.
// The functional-options pattern lets each new persistence layer
// (workspaces, posts, storage, future rate-limiters, etc.) get its own
// WithXxxStore opt without bumping args.
type RouterOption func(*Router)

// WithWorkspaceStore injects the workspace persistence layer. Without it,
// the /workspaces handlers return 501 not-implemented (see the 501-guards
// at the top of every handler in workspaces.go).
func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) { r.workspaceStore = repo }
}

// WithPostStore injects the post + post_targets persistence layer.
// Without it, the /posts handlers return 501.
func WithPostStore(repo PostStore) RouterOption {
	return func(r *Router) { r.postStore = repo }
}

// WithStorageProvider injects the presigned-URL minting layer. Without
// it, /api/v1/storage/upload-url returns 501 (storage.go's existing
// nil-check fires).
func WithStorageProvider(p StorageProvider) RouterOption {
	return func(r *Router) { r.storageProvider = p }
}

// WithMaxUploadBytes caps the per-file size the API will accept at
// /api/v1/storage/upload-url. When unset, the handler falls back to
// defaultMaxUploadBytes (200 MiB) — defined in storage.go.
func WithMaxUploadBytes(n int64) RouterOption {
	return func(r *Router) { r.maxUploadBytes = n }
}

// NewRouter constructs a Router. workspaceStore / postStore / storageProvider
// stay nil until explicitly wired by the matching WithXxx option — handlers
// 501 themselves in that case so a partially-rolled-out deployment still
// boots and reports the missing integration.
//
// strictAuth controls whether protected endpoints require a valid JWT.
// frontendURL, when non-empty, redirects OAuth callbacks to this origin's
// /auth/callback route with the JWT in query params. When empty, the
// callback returns JSON (useful for non-browser clients and curl testing).
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

// Setup registers all HTTP routes on a chi.Mux and returns the wrapped
// handler (CORS + logging middleware applied to everything, including
// OPTIONS preflight short-circuits inside corsMiddleware).
//
// All handler methods are wrapped with `http.HandlerFunc(...)` so they
// satisfy http.Handler — without the explicit conversion chi.Mux.Method
// rejects bare `func(w, req)` values. `r.protected(handler)` returns
// http.HandlerFunc directly, so it can be passed as-is.
func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()

	// Pre-existing routes — health, OAuth, publish, accounts, metrics.
	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", http.HandlerFunc(r.handleLogin))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(r.handleCallback))
	r.mux.Method(http.MethodPost, "/api/v1/posts/publish", r.protected(r.handlePublishPost))
	r.mux.Method(http.MethodPost, "/api/v1/posts/publish-all", r.protected(r.handlePublishAll))
	r.mux.Method(http.MethodGet, "/api/v1/accounts", r.protected(r.handleListAccounts))
	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))
	r.mux.Method(http.MethodPost, "/api/v1/storage/upload-url", r.protected(r.handleCreateUploadURL))

	// CRUD workspaces — every route is JWT-protected, with optional
	// ownership enforcement added by the workspaces.go handlers.
	r.mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreateWorkspace))
		sr.Get("/", r.protected(r.handleListWorkspaces))
		sr.Get("/{id}", r.protected(r.handleGetWorkspace))
		sr.Delete("/{id}", r.protected(r.handleDeleteWorkspace))
	})

	// CRUD posts — JWT-protected; ownership is enforced by reading the
	// parent workspace before any mutation (workspaces.go handles that).
	r.mux.Route("/api/v1/posts", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreatePost))
		sr.Post("/{id}/targets", r.protected(r.handleAddTarget))
		sr.Post("/{id}/schedule", r.protected(r.handleSchedulePost))
		sr.Get("/{id}", r.protected(r.handleGetPost))
		sr.Get("/workspace/{wid}", r.protected(r.handleListByWorkspace))
	})

	return r.corsMiddleware(r.loggingMiddleware(r.mux))
}

// -----------------------------------------------------------------------
// Pre-existing handlers (health, OAuth, publish, listAccounts, metrics)
// -----------------------------------------------------------------------

// handleHealth answers GET /api/v1/health with the platforms currently
// registered. Safe to call without a JWT — used by the SPA probe.
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

// handleLogin answers GET /api/v1/auth/{provider}/login by redirecting the
// browser to the provider's OAuth authorization URL. The {provider} path
// param is matched by chi's wildcard. State defaults to "<provider>_default"
// when the client doesn't supply one (so the callback can default-route).
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

// handleCallback answers GET /api/v1/auth/{provider}/callback by exchanging
// the OAuth authorization code for an access token, upserting the user,
// then either issuing a JWT and 302-redirecting to FRONTEND_URL/auth/callback
// (browser flow) or returning the JWT in JSON (curl / non-browser clients).
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

// PublishRequest is the JSON body for both /posts/publish and /posts/publish-all.
// TikTok-specific fields are ignored by other platforms.
type PublishRequest struct {
	UserID       int64  `json:"user_id"`
	Platform     string `json:"platform"`
	MediaURL     string `json:"media_url"`
	Caption      string `json:"caption"`
	ContentType  string `json:"content_type"`
	Title        string `json:"title"`
	PrivacyLevel string `json:"privacy_level,omitempty"`
	CommentMode  string `json:"comment_mode,omitempty"`
	DuetMode     string `json:"duet_mode,omitempty"`
}

// protected wraps a handler so that the JWT middleware runs first.
// r.protected(handler) returns http.HandlerFunc, so it satisfies
// http.Handler directly — no extra http.HandlerFunc(...) wrap needed.
func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.auth.Middleware(r.strictAuth, next).ServeHTTP(w, req)
	}
}

// handlePublishAll publishes the same content to EVERY connected platform
// account for the user. Partial failures are reported per-platform in the
// response; the overall status is "completed" regardless so the client can
// iterate results without crashing on a single platform's failure.
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

	if pubReq.ContentType != "text" && pubReq.ContentType != "image" && pubReq.ContentType != "photo" && pubReq.ContentType != "video" && pubReq.ContentType != "reel" {
		writeError(w, http.StatusBadRequest, "content_type must be one of: image, video, text")
		return
	}

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

// handlePublishPost publishes content to a single platform. The platform
// field selects which OAuth account to use; userID comes from the JWT
// (strict mode) or the body (legacy fallback).
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

// publishError carries an HTTP status so the handler returns the right code
// without leaking HTTP details into the business logic.
type publishError struct {
	status  int
	message string
}

func (e *publishError) Error() string { return e.message }

// handleListAccounts answers GET /api/v1/accounts (protected). Returns the
// platform accounts owned by the caller; optional ?platform=<name> filters
// to a single platform. In strict mode the user_id comes from JWT context;
// in legacy mode from the query string.
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

// -----------------------------------------------------------------------
// Middleware
// -----------------------------------------------------------------------

// loggingMiddleware logs every request method + path + remote_addr at INFO.
// Wraps the entire mux so CORS preflight (rejected by the browser before
// chi sees it) is still observable in server logs.
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

// handleMetrics serves the Prometheus exposition format. Optionally
// protected by METRICS_BASIC_AUTH_USER / METRICS_BASIC_AUTH_PASS env vars.
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

// corsMiddleware sets CORS headers for the React SPA. Crucial contract:
// only requests from `allowedOrigin` get `Access-Control-Allow-Origin` echoed
// back. Unknown origins deliberately get NO Allow-Origin header (the browser
// then refuses to read the response, blocking the request). OPTIONS
// short-circuits with 204 BEFORE the handler runs — chi would otherwise 405
// for non-routed methods.
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

// -----------------------------------------------------------------------
// Helpers (cross-cutting; used by workspaces.go + posts.go too)
// -----------------------------------------------------------------------

// parsePathIDAsInt64 extracts a numeric path parameter (e.g. `{id}`) and
// writes a 400 Bad Request if it's missing or not a positive int64.
// Returns (id, true) on success; the caller should bail if ok is false —
// the response is already written.
func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

// requireUserID resolves the caller's user_id from JWT context (or the
// fallback in non-strict mode) and writes 401 if absent. Returns
// (userID, true) on success.
func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return userID, true
}

// logAndError logs err with structured context and writes a 500 JSON
// response. Centralizes the slog+writeError pair so handlers stay short.
func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}

// resolveUserID reads the JWT-derived user id from context. In strict mode
// the fallback (e.g. a body/query user_id) is rejected — caller MUST pass a
// valid JWT. In legacy/lenient mode the fallback is honoured, useful during
// the JWT-aware frontend rollout window.
func resolveUserID(req *http.Request, fallback int64, strict bool) int64 {
	if uid, ok := auth.UserIDFromContext(req.Context()); ok && uid > 0 {
		return uid
	}
	if strict {
		return 0
	}
	return fallback
}

// writeJSON writes a single JSON response. Logs encode errors but doesn't
// surface them to the caller (we already shipped the status).
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}

// writeError writes a JSON `{"error": "<message>"}` body with the given
// status. Centralized so error responses have one schema.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
