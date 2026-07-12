package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
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
	mux              *chi.Mux
	capabilities     *services.CapabilityRouter
	userRepo         UserStore
	workspaceStore   WorkspaceStore
	postStore        PostStore
	storageProvider  StorageProvider
	mediaStore       MediaStore
	auditLogStore    AuditLogStore
	auth             *auth.Manager
	apiKeyAuth       *auth.Authenticator
	apiKeyStore      ApiKeyStore
	idempotencyStore IdempotencyStore
	vault            credentials.VaultAPI
	oneTimeCodes     *OneTimeCodeStore
	frontendURL      string
	allowedOrigin    []string
	maxUploadBytes   int64
	rateLimiter      *rateLimiter // FASE 1.2: per-IP token bucket
	authEmailSvc     AuthEmailStore
	teamStore        TeamStore
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
	Update(post *models.Post) error
	ListByWorkspace(workspaceID int64) ([]models.Post, error)
	Delete(id int64) error
	SaveTarget(target *models.PostTarget) error
	PublishPost(id int64) error
	CancelPost(id int64) error
	RetryPost(id int64) error
	RetryTarget(id int64) error
}

// ApiKeyStore mirrors the subset of repository.ApiKeyRepository that
// the API layer + Authenticator middleware actually depend on.
type ApiKeyStore interface {
	Create(key *models.ApiKey, hash []byte) error
	FindByIDForWorkspace(wsID, id int64) (*models.ApiKey, error)
	FindByHash(hash []byte) (*models.ApiKey, error)
	ListByWorkspace(wsID int64) ([]models.ApiKey, error)
	Revoke(wsID, id int64) error
	MarkUsed(wsID, id int64) error
	UpdateName(wsID, id int64, name string) error
	Rotate(wsID, oldID int64, newKey *models.ApiKey, newHash []byte) error
}

// IdempotencyStore mirrors the two methods the /api/v1/posts
// handler (handleCreatePost) needs:
//
//   - FindActiveByKey — pre-handler lookup. Returns (nil, nil)
//     on miss OR on expired rows (so the middleware treats expired
//     records as a normal miss and lets the handler run).
//   - Insert — post-handler write. Persists (workspace_id, key,
//     hash) so subsequent replays hit the same row.
//
// The contract is intentionally narrow: no Update, no Delete from
// the API layer; the table is append-only from this side. Expired
// rows are evicted by a CRON sweeper that lands in a future Taglio.
//
// Same pattern as PostStore / WorkspaceStore / ApiKeyStore: an
// interface local to pkg/api so handlers depend on the contract,
// not on the *sql.DB-bound concrete type. Tests can pass an
// in-memory fake.
type IdempotencyStore interface {
	FindActiveByKey(workspaceID int64, key string, now time.Time) (*models.IdempotencyRecord, error)
	Insert(rec *models.IdempotencyRecord) error
}

type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
	// VerifyUpload (Taglio 3.2) HEADs the object and returns
	// server-reported content-type + size for /complete verification.
	VerifyUpload(ctx context.Context, key string) (contentType string, sizeBytes int64, err error)
	// AssetURL (Taglio 3.2) returns the trusted internal URL for an
	// uploaded asset. The publish flow goes through this — the
	// platform API never sees a user-controlled URL.
	AssetURL(key string) string
}

type AuditLogStore interface {
	Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error
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

// WithIdempotencyStore injects the idempotency_records persistence
// layer. The /api/v1/posts handler (handleCreatePost) consults this
// when an Idempotency-Key request header is present. Without this
// option wired, Idempotency-Key headers are silently ignored — the
// handler falls through to the no-cache path. Production wiring
// must include this option (see cmd/server/main.go).
func WithIdempotencyStore(s IdempotencyStore) RouterOption {
	return func(r *Router) { r.idempotencyStore = s }
}

// WithApiKeyAuthenticator injects the API-key middleware used on
// /api/v1/api-keys/* routes. When set, requests with Authorization:
// Bearer sk_test_…/sk_live_… are authenticated against the api_keys
// table by Authenticator.Middleware; non-sk_ requests pass through
// to the existing JWT/cookie chain. When NOT set, the API-key routes
// behave as JWT-only (existing behaviour; the apiKeyStore is
// independently wired by WithApiKeyStore).
//
// Optional in main.go today so existing dev environments without
// per-tenant API keys keep working — production deployments always
// set it (cmd/server/main.go constructs one via
// auth.NewApiKeyAuthenticator(apiKeyRepo)).
func WithApiKeyAuthenticator(a *auth.Authenticator) RouterOption {
	return func(r *Router) { r.apiKeyAuth = a }
}

// WithApiKeyStore injects the api_keys persistence layer. The
// /api/v1/api-keys/* handlers require this to be wired; otherwise
// they return 501 Not Implemented at runtime, mirroring the
// postStore / workspaceStore nil-guard pattern. The interface is
// local to this package so test fixtures can supply an in-memory
// fake without dragging the repository import into pkg/api tests.
func WithApiKeyStore(s ApiKeyStore) RouterOption {
	return func(r *Router) { r.apiKeyStore = s }
}

// WithCredentialVault injects the central credential vault. The Router
// REQUIRES this to be set (via main.go) before serving
// handleCallback — the call site panics with a nil-pointer dereference
// if it's missing, which is the desired fail-fast behaviour for a
// misconfigured main.go. Tests
// inject a mockCredentialVault via this same option.
//
// Taglio 2.2: renamed from WithTokenService. The vault centralises
// AES-256-GCM encryption, persistence, refresh (with Postgres advisory
// locks), and revocation — no provider or consumer needs to know how
// tokens are stored.
func WithCredentialVault(v credentials.VaultAPI) RouterOption {
	return func(r *Router) { r.vault = v }
}

// WithAuthEmailService injects the email/password auth service for SaaS
// registration, login, email verification, and password reset endpoints.
// When not set, /api/v1/auth/register, /login, /verify, /forgot-password,
// and /reset-password return 501 Not Implemented.
func WithAuthEmailService(svc AuthEmailStore) RouterOption {
	return func(r *Router) { r.authEmailSvc = svc }
}

// WithTeamStore injects the workspace team repository for member/invite
// management. When not set, /api/v1/workspaces/{id}/members, /invites,
// and /api/v1/invites/{token} return 501 Not Implemented.
func WithTeamStore(s TeamStore) RouterOption {
	return func(r *Router) { r.teamStore = s }
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
		rateLimiter:   newRateLimiter(), // FASE 1.2: per-IP token bucket
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()
	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))

	// FASE 2.2: email/password auth routes (when configured).
	if r.authEmailSvc != nil {
		r.registerAuthEmailRoutes()
	}

	// FASE 2.3: workspace team management (when configured).
	if r.teamStore != nil {
		r.registerTeamRoutes()
	}

	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", http.HandlerFunc(r.handleLogin))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(r.handleCallback))
	r.mux.Method(http.MethodPost, "/api/v1/auth/exchange", http.HandlerFunc(r.handleExchangeCode))
	r.mux.Method(http.MethodGet, "/api/v1/auth/me", r.protected(r.handleMe))
	r.mux.Method(http.MethodPost, "/api/v1/auth/logout", http.HandlerFunc(r.handleLogout))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/{id}", r.protected(r.handleGetAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/validate", r.protected(r.handleValidateAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/reconnect", r.protected(r.handleReconnectAccount))
	r.mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", r.protected(r.handleDeleteAccount))
	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))
	// Taglio 3.2: the old /api/v1/storage/upload-url endpoint is
	// replaced by /api/v1/media/presign (see pkg/api/media.go). The
	// new endpoint is part of a 3-step presigned upload flow that
	// removes arbitrary media_url from public post payloads.
	r.mux.Method(http.MethodPost, "/api/v1/media/presign", r.protected(r.handlePresignMedia))
	r.mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", r.protected(r.handleCompleteMedia))
	r.mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreateWorkspace))
		sr.Get("/", r.protected(r.handleListWorkspaces))
		sr.Get("/{id}", r.protected(r.handleGetWorkspace))
		sr.Delete("/{id}", r.protected(r.handleDeleteWorkspace))
	})
	r.mux.Route("/api/v1/posts", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreatePost))
		sr.Get("/", r.protected(r.handleListPosts))
		sr.Get("/workspace/{wid}", r.protected(r.handleListByWorkspace))
		sr.Get("/{id}", r.protected(r.handleGetPost))
		sr.Patch("/{id}", r.protected(r.handlePatchPost))
		sr.Delete("/{id}", r.protected(r.handleDeletePost))
		sr.Post("/{id}/publish", r.protected(r.handlePublishPostID))
		sr.Post("/{id}/schedule", r.protected(r.handleSchedulePost))
		sr.Post("/{id}/cancel", r.protected(r.handleCancelPost))
		sr.Post("/{id}/retry", r.protected(r.handleRetryPost))
		sr.Get("/{id}/targets", r.protected(r.handleGetPostTargets))
		sr.Post("/{id}/targets", r.protected(r.handleAddTarget))
	})
	r.mux.Route("/api/v1/post-targets", func(sr chi.Router) {
		sr.Post("/{id}/retry", r.protected(r.handleRetryTarget))
	})

	// /api/v1/api-keys/* — Taglio 4.6 tenant API key management.
	//
	// Middleware order on this sub-router:
	//   1. Authenticator (if wired) — authenticates sk_test_/sk_live_
	//      Bearer tokens and deposits ApiKeyIdentity in context.
	//      Pass-through for non-sk_ requests, so JWT/cookie auth runs
	//      next.
	//   2. JWT/cookie auth (existing r.auth) — authenticates JWT/cookie
	//      sessions, deposits UserIdentity in context.
	//   3. Handler — reads IdentityFromContext (works for both),
	//      dispatches on IsAPIKey / HasPermission as needed.
	//
	// Skipping Authenticator (when WithApiKeyAuthenticator was not
	// called) means API-key-only clients can't authenticate; the
	// JWT/cookie path remains available so dashboard-like flows
	// still work in dev.
	r.mux.Route("/api/v1/api-keys", func(sr chi.Router) {
		if r.apiKeyAuth != nil {
			sr.Use(func(next http.Handler) http.Handler {
				return r.apiKeyAuth.Middleware(next)
			})
		}
		sr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				r.auth.Middleware(next).ServeHTTP(w, req)
			})
		})
		sr.Post("/", r.handleCreateApiKey)
		sr.Get("/", r.handleListApiKeys)
		sr.Get("/{id}", r.handleGetApiKey)
		sr.Delete("/{id}", r.handleDeleteApiKey)
		sr.Post("/{id}/rotate", r.handleRotateApiKey)
	})
	// FASE 1.2: rate limiter is the outermost middleware so it
	// protects ALL routes (public + protected) from abuse.
	return r.rateLimiter.middleware(r.corsMiddleware(r.loggingMiddleware(r.mux)))
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

func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.auth.Middleware(next).ServeHTTP(w, req)
	}
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
