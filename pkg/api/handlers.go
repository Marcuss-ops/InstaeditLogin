package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
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
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

type Router struct {
	mux             *chi.Mux
	capabilities    *services.CapabilityRouter
	userRepo        UserStore
	workspaceStore  WorkspaceStore
	postStore       PostStore
	storageProvider StorageProvider
	auditLogStore   AuditLogStore
	auth            *auth.Manager
	vault           credentials.VaultAPI
	apiKeyStore     APIKeyStore
	oneTimeCodes    *OneTimeCodeStore
	frontendURL     string
	allowedOrigin   []string
	maxUploadBytes  int64
}

type UserStore interface {
	FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error)
	ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error)
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
	UpdatePlatformAccount(account *models.PlatformAccount) error
	DeletePlatformAccount(id int64) error
}

type WorkspaceStore interface {
	Create(w *models.Workspace) error
	FindByID(id int64) (*models.Workspace, error)
	ListByOwner(ownerID int64) ([]models.Workspace, error)
	Delete(id int64) error
}

type PostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	FindByID(id int64) (*models.Post, error)
	FindWithTargets(id int64) (*models.Post, []models.PostTarget, []models.MediaAsset, error)
	Update(post *models.Post) error
	ListByWorkspace(workspaceID int64) ([]models.Post, error)
	List(filter repository.PostFilter) (*repository.PagedPosts, error)
	Delete(id int64) error
	Save(target *models.PostTarget) error
	PublishPost(id int64) error
	SchedulePost(id int64, scheduledAt time.Time) error
	CancelPost(id int64) error
	RetryPost(id int64) error
	RetryTarget(id int64) error
	ListTargets(postID int64) ([]models.PostTarget, error)
	AddMediaAsset(asset *models.MediaAsset) error
	ListMediaAssets(postID int64) ([]models.MediaAsset, error)
}

type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
}

type AuditLogStore interface {
	Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error
}

// APIKeyStore is the narrow contract for api-key CRUD + rotation.
// Implemented by *repository.ApiKeyRepository in production.
type APIKeyStore interface {
	Create(ak *models.ApiKey, testKey bool) (string, error)
	FindByID(id int64) (*models.ApiKey, error)
	ListByProject(projectID int64) ([]models.ApiKey, error)
	Delete(id int64) error
	Rotate(id int64, testKey bool) (*models.ApiKey, string, error)
}

type RouterOption func(*Router)

func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) { r.workspaceStore = repo }
}
func WithPostStore(repo PostStore) RouterOption {
	return func(r *Router) { r.postStore = repo }
}
func WithStorageProvider(p StorageProvider) RouterOption {
	return func(r *Router) { r.storageProvider = p }
}
func WithMaxUploadBytes(n int64) RouterOption {
	return func(r *Router) { r.maxUploadBytes = n }
}
func WithAuditLogStore(store AuditLogStore) RouterOption {
	return func(r *Router) { r.auditLogStore = store }
}
func WithOneTimeCodeStore(s *OneTimeCodeStore) RouterOption {
	return func(r *Router) { r.oneTimeCodes = s }
}

// WithCredentialVault injects the central credential vault. The Router
// REQUIRES this to be set (via main.go) before serving
// handleCallback / handlePublishPost / handlePublishAll — the call
// sites panic with a nil-pointer dereference if it's missing, which is
// the desired fail-fast behaviour for a misconfigured main.go. Tests
// inject a mockCredentialVault via this same option.
//
// Taglio 2.2: renamed from WithTokenService. The vault centralises
// AES-256-GCM encryption, persistence, refresh (with Postgres advisory
// locks), and revocation — no provider or consumer needs to know how
// tokens are stored.
func WithCredentialVault(v credentials.VaultAPI) RouterOption {
	return func(r *Router) { r.vault = v }
}
func WithApiKeyStore(repo APIKeyStore) RouterOption {
	return func(r *Router) { r.apiKeyStore = repo }
}

func NewRouter(
	capRouter *services.CapabilityRouter,
	userRepo UserStore,
	authMgr *auth.Manager,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) *Router {
	r := &Router{
		capabilities:  capRouter,
		userRepo:      userRepo,
		auth:          authMgr,
		oneTimeCodes:  NewOneTimeCodeStore(60 * time.Second),
		frontendURL:   frontendURL,
		allowedOrigin: allowedOrigins,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()
	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", http.HandlerFunc(r.handleLogin))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(r.handleCallback))
	r.mux.Method(http.MethodPost, "/api/v1/auth/exchange", http.HandlerFunc(r.handleExchangeCode))
	r.mux.Method(http.MethodGet, "/api/v1/auth/me", r.protected(r.handleMe))
	r.mux.Method(http.MethodPost, "/api/v1/auth/logout", http.HandlerFunc(r.handleLogout))
	r.mux.Method(http.MethodPost, "/api/v1/posts/publish", r.protected(r.handlePublishPost))
	r.mux.Method(http.MethodPost, "/api/v1/posts/publish-all", r.protected(r.handlePublishAll))
	r.mux.Method(http.MethodGet, "/api/v1/accounts", r.protected(r.handleListAccounts))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/{id}", r.protected(r.handleGetAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/validate", r.protected(r.handleValidateAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/reconnect", r.protected(r.handleReconnectAccount))
	r.mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", r.protected(r.handleDeleteAccount))
	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))
	r.mux.Method(http.MethodPost, "/api/v1/storage/upload-url", r.protected(r.handleCreateUploadURL))
	r.mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreateWorkspace))
		sr.Get("/", r.protected(r.handleListWorkspaces))
		sr.Get("/{id}", r.protected(r.handleGetWorkspace))
		sr.Delete("/{id}", r.protected(r.handleDeleteWorkspace))
	})
	r.mux.Route("/api/v1/posts", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreatePost))
		sr.Get("/workspace/{wid}", r.protected(r.handleListByWorkspace))
		sr.Get("/{id}", r.protected(r.handleGetPost))
		sr.Post("/{id}/schedule", r.protected(r.handleSchedulePost))
		sr.Post("/{id}/targets", r.protected(r.handleAddTarget))
	})
	r.mux.Route("/api/v1/projects/{pid}/keys", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreateApiKey))
		sr.Get("/", r.protected(r.handleListApiKeys))
		sr.Get("/{kid}", r.protected(r.handleGetApiKey))
		sr.Delete("/{kid}", r.protected(r.handleDeleteApiKey))
		sr.Post("/{kid}/rotate", r.protected(r.handleRotateApiKey))
	})
	return r.corsMiddleware(r.loggingMiddleware(r.mux))
}

// ----------------------------------------------------------------------- Handlers

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"service":   "InstaEditLogin",
		"version":   "2.0.0",
		"platforms": r.capabilities.Names(),
	})
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	state, err := generateOAuthState(w, provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start oauth flow")
		return
	}
	http.Redirect(w, req, p.GetLoginURL(state), http.StatusFound)
}

func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
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
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing state parameter")
		return
	}
	if err := verifyOAuthState(w, req, provider, state); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state: "+err.Error())
		return
	}
	profile, tokenData, err := p.HandleCallback(req.Context(), state, code)
	if err != nil {
		metrics.RecordOAuthLoginError(provider, metrics.ErrorKind(err))
		writeError(w, http.StatusInternalServerError, "authentication failed: "+err.Error())
		return
	}
	metrics.RecordOAuthLoginSuccess(provider)
	user, account, err := r.userRepo.FindOrCreateUserByPlatform(profile, provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save user: "+err.Error())
		return
	}
	// Taglio 2.2: token persistence goes through CredentialVault.Save.
	if err := r.vault.Save(req.Context(), account.ID, tokenData); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save token: "+err.Error())
		return
	}
	// Taglio 1.2: do NOT return the JWT in the URL or the response body.
	// Instead, generate a one-time code bound to {userID, name, username, jwtExp},
	// redirect the browser to /auth/callback?code=...&provider=..., and let
	// the SPA POST that code to /api/v1/auth/exchange which sets the
	// HttpOnly session cookie.
	expiresAt := time.Now().Add(24 * time.Hour)
	var authCode string
	authCode, err = r.oneTimeCodes.Generate(ExchangePayload{
		UserID:    user.ID,
		Name:      user.Name,
		Username:  account.Username,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue one-time code")
		return
	}
	if r.frontendURL != "" {
		q := url.Values{}
		q.Set("code", authCode)
		q.Set("provider", provider)
		http.Redirect(w, req, strings.TrimRight(r.frontendURL, "/")+"/auth/callback?"+q.Encode(), http.StatusFound)
		return
	}
	// No frontend configured (test/CLI mode): return the code in the body
	// so the caller can manually POST it to /api/v1/auth/exchange.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "code_issued",
		"provider":   provider,
		"code":       authCode,
		"user_id":    user.ID,
		"name":       user.Name,
		"account_id": account.ID,
	})
}

// handleExchangeCode exchanges a one-time code (from /auth/callback?code=...)
// for an HttpOnly session cookie. The code is single-use and 60s TTL; on
// success the cookie is set and 204 is returned. The SPA's /auth/callback
// page calls this immediately on mount, then redirects to /dashboard.
func (r *Router) handleExchangeCode(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	payload, err := r.oneTimeCodes.Consume(body.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}
	jwtToken, _, _, err := r.auth.Issue(payload.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}
	metrics.IncJWTIssued()
	// SameSite=None is required because the SPA is on a different host
	// (Vercel) than the API backend. Secure=true is required by browsers
	// for SameSite=None. HttpOnly keeps the JWT out of document.cookie
	// so an XSS in the SPA cannot exfiltrate it.
	sameSite := http.SameSiteNoneMode
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    jwtToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: sameSite,
		MaxAge:   int(time.Until(payload.ExpiresAt).Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current user identity. Used by the SPA on every page
// load to learn who's logged in (no JWT in localStorage anymore).
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	// For now return just the user_id; the SPA can call /api/v1/accounts
	// for richer profile data. Future: extend the userRepo with FindByID.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": userID,
	})
}

// handleLogout clears the session cookie. 204 on success.
func (r *Router) handleLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
	w.WriteHeader(http.StatusNoContent)
}

type PublishRequest struct {
	Platform     string `json:"platform"`
	MediaURL     string `json:"media_url"`
	Caption      string `json:"caption"`
	ContentType  string `json:"content_type"`
	Title        string `json:"title"`
	PrivacyLevel string `json:"privacy_level,omitempty"`
	CommentMode  string `json:"comment_mode,omitempty"`
	DuetMode     string `json:"duet_mode,omitempty"`
}

func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.auth.Middleware(next).ServeHTTP(w, req)
	}
}

// --- publish handlers ---

func (r *Router) handlePublishAll(w http.ResponseWriter, req *http.Request) {
	var pubReq PublishRequest
	if err := json.NewDecoder(req.Body).Decode(&pubReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
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
		Platform, Status, PlatformMediaID, PlatformURL, Error string
	}
	var results []platformResult
	var okCount, failCount int
	for _, acc := range accounts {
		result, err := r.publishToAccount(req.Context(), acc, &pubReq)
		pr := platformResult{Platform: acc.Platform}
		if err != nil {
			pr.Status = "error"
			pr.Error = err.Error()
			failCount++
		} else {
			pr.Status = "published"
			pr.PlatformMediaID = result.PlatformMediaID
			pr.PlatformURL = result.PlatformURL
			okCount++
		}
		results = append(results, pr)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "completed", "success_count": okCount, "fail_count": failCount, "results": results,
	})
}

func (r *Router) handlePublishPost(w http.ResponseWriter, req *http.Request) {
	var pubReq PublishRequest
	if err := json.NewDecoder(req.Body).Decode(&pubReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	result, err := r.publishContent(req.Context(), userID, &pubReq)
	if err != nil {
		var pe *publishError
		if errors.As(err, &pe) {
			writeError(w, pe.status, pe.message)
		} else {
			writeError(w, http.StatusInternalServerError, "failed to publish: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "published", "platform": pubReq.Platform,
		"platform_media_id": result.PlatformMediaID, "platform_url": result.PlatformURL,
	})
}

func (r *Router) publishContent(ctx context.Context, userID int64, pubReq *PublishRequest) (*models.PublishResult, error) {
	if pubReq.Platform == "" {
		pubReq.Platform = models.PlatformInstagram
	}
	if _, ok := r.capabilities.OAuth(pubReq.Platform); !ok {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("unsupported platform: %s", pubReq.Platform)}
	}
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, pubReq.Platform)
	if err != nil || len(accounts) == 0 {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("no %s account linked to this user", pubReq.Platform)}
	}
	return r.publishToAccount(ctx, accounts[0], pubReq)
}

func (r *Router) publishToAccount(ctx context.Context, account *models.PlatformAccount, pubReq *PublishRequest) (*models.PublishResult, error) {
	// Taglio 2.1: per-capability lookups. We need the OAuthProvider (for
	// token refresh via the vault) AND the Publisher (for the actual
	// call). A platform missing either cannot be published to.
	oauth, oauthOK := r.capabilities.OAuth(account.Platform)
	publisher, pubOK := r.capabilities.Publisher(account.Platform)
	if !oauthOK || !pubOK {
		return nil, &publishError{http.StatusNotFound, fmt.Sprintf("unsupported platform: %s", account.Platform)}
	}
	// Taglio 2.2: adapt the provider's RefreshOAuthToken method into a
	// credentials.TokenRefresher closure. The vault only knows the
	// function signature; the provider type stays out of the vault.
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	// Try Bearer first (refresh-capable), then LongLived (Meta-style re-exchange).
	oauthToken, err := r.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		if oauthToken, err = r.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher); err != nil {
			return nil, &publishError{http.StatusUnauthorized, "no valid token: " + err.Error()}
		}
	}
	payload := models.PublishPayload{
		Text: pubReq.Caption, Title: pubReq.Title,
		PrivacyLevel: pubReq.PrivacyLevel, CommentMode: pubReq.CommentMode, DuetMode: pubReq.DuetMode,
	}
	switch pubReq.ContentType {
	case "video", "reel":
		payload.VideoURL = pubReq.MediaURL
	case "image", "photo":
		payload.ImageURL = pubReq.MediaURL
	case "text":
	default:
		return nil, &publishError{http.StatusBadRequest, "content_type must be one of: image, video, text"}
	}
	return publisher.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
}

type publishError struct {
	status  int
	message string
}

func (e *publishError) Error() string { return e.message }

func (r *Router) handleListAccounts(w http.ResponseWriter, req *http.Request) {
	userID, ok := requireUserID(w, req, r)
	if !ok {
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
	writeJSON(w, http.StatusOK, map[string]interface{}{"accounts": accounts})
}

// handleGetAccount / handleValidateAccount / handleReconnectAccount / handleDeleteAccount
// are stubs returning 501 (Taglio 1.4 will land the real implementations).
func (r *Router) handleGetAccount(w http.ResponseWriter, req *http.Request) {
	writeError(w, http.StatusNotImplemented, "account by id: stub (Taglio 1.4)")
}
func (r *Router) handleValidateAccount(w http.ResponseWriter, req *http.Request) {
	writeError(w, http.StatusNotImplemented, "validate account: stub (Taglio 1.4)")
}
func (r *Router) handleReconnectAccount(w http.ResponseWriter, req *http.Request) {
	writeError(w, http.StatusNotImplemented, "reconnect account: stub (Taglio 1.4)")
}
func (r *Router) handleDeleteAccount(w http.ResponseWriter, req *http.Request) {
	writeError(w, http.StatusNotImplemented, "delete account: stub (Taglio 1.4)")
}

// ----------------------------------------------------------------------- Middleware

func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.Info("HTTP request", "method", req.Method, "path", req.URL.Path, "remote_addr", req.RemoteAddr)
		next.ServeHTTP(w, req)
	})
}

func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	user := os.Getenv("METRICS_BASIC_AUTH_USER")
	pass := os.Getenv("METRICS_BASIC_AUTH_PASS")
	if user != "" && pass != "" {
		u, p, ok := req.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	metrics.Handler().ServeHTTP(w, req)
}

func (r *Router) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(r.allowedOrigin))
	for _, o := range r.allowedOrigin {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				// Taglio 1.2: include Cookie so the browser is allowed to
				// send the HttpOnly session cookie. Access-Control-Allow-Credentials
				// is required when the browser uses credentials:'include'.
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
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

// ----------------------------------------------------------------------- Helpers

func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	uid, ok := auth.UserIDFromContext(req.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return uid, true
}

const (
	oauthStateCookiePrefix = "oauth_state_"
	oauthStateMaxAge       = 10 * time.Minute
)

func OAuthStateCookieName(provider string) string { return oauthStateCookiePrefix + provider }

func generateOAuthState(w http.ResponseWriter, provider string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth state rand failed: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: state, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
	return state, nil
}

func verifyOAuthState(w http.ResponseWriter, req *http.Request, provider, stateParam string) error {
	c, err := req.Cookie(OAuthStateCookieName(provider))
	if err != nil {
		return fmt.Errorf("oauth state cookie missing for provider %q", provider)
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(stateParam)) != 1 {
		return fmt.Errorf("oauth state mismatch for provider %q (CSRF protection)", provider)
	}
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
	return nil
}

func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
