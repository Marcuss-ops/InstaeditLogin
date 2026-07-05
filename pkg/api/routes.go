package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Router handles HTTP routing and request dispatching.
type Router struct {
	cfg          *config.Config
	oauthService *services.FacebookOAuthService
	metaService  *services.MetaService
	userRepo     *repository.UserRepository
}

// NewRouter creates a new Router.
func NewRouter(cfg *config.Config, oauth *services.FacebookOAuthService, meta *services.MetaService, userRepo *repository.UserRepository) *Router {
	return &Router{
		cfg:          cfg,
		oauthService: oauth,
		metaService:  meta,
		userRepo:     userRepo,
	}
}

// Setup registers all HTTP routes and returns the handler.
func (r *Router) Setup() http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /api/v1/health", r.handleHealth)

	// Auth routes
	mux.HandleFunc("GET /api/v1/auth/login", r.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/callback", r.handleCallback)

	// Post publishing
	mux.HandleFunc("POST /api/v1/posts/publish", r.handlePublishPost)

	// Wrap with logging middleware
	return r.loggingMiddleware(mux)
}

// handleHealth returns a simple health check response.
func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "InstaEditLogin",
		"version": "1.0.0",
	})
}

// handleLogin redirects the user to Meta's OAuth login page.
func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	state := req.URL.Query().Get("state")
	if state == "" {
		state = "default"
	}

	loginURL := r.oauthService.GetLoginURL(state)
	http.Redirect(w, req, loginURL, http.StatusFound)
}

// handleCallback processes the OAuth callback from Meta.
func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	state := req.URL.Query().Get("state")
	slog.Info("OAuth callback received", "state", state)

	user, err := r.oauthService.HandleCallback(code)
	if err != nil {
		slog.Error("OAuth callback failed", "error", err)
		writeError(w, http.StatusInternalServerError, "authentication failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "authenticated",
		"user_id": user.ID,
		"name":    user.Name,
	})
}

// PublishRequest is the request body for publishing content.
type PublishRequest struct {
	UserID      int64  `json:"user_id"`
	MediaURL    string `json:"media_url"`
	Caption     string `json:"caption"`
	ContentType string `json:"content_type"` // "image" or "video"
}

// handlePublishPost publishes content to Instagram via Meta API.
func (r *Router) handlePublishPost(w http.ResponseWriter, req *http.Request) {
	var pubReq PublishRequest
	if err := json.NewDecoder(req.Body).Decode(&pubReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if pubReq.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if pubReq.MediaURL == "" {
		writeError(w, http.StatusBadRequest, "media_url is required")
		return
	}

	// Get decrypted token for the user
	oauthToken, err := r.oauthService.GetDecryptedToken(pubReq.UserID, "long_lived")
	if err != nil {
		slog.Error("Failed to get decrypted token", "error", err, "user_id", pubReq.UserID)
		writeError(w, http.StatusUnauthorized, "no valid token found: "+err.Error())
		return
	}

	// Get user's Instagram accounts
	accounts, err := r.userRepo.ListInstagramAccountsByUser(pubReq.UserID)
	if err != nil || len(accounts) == 0 {
		writeError(w, http.StatusNotFound, "no Instagram account linked to this user")
		return
	}

	instagramUserID := accounts[0].InstagramUserID

	// Publish based on content type
	var mediaID string
	switch pubReq.ContentType {
	case "video", "reel":
		mediaID, err = r.metaService.PublishVideo(oauthToken.AccessToken, instagramUserID, pubReq.MediaURL, pubReq.Caption)
	default:
		mediaID, err = r.metaService.PublishPhoto(oauthToken.AccessToken, instagramUserID, pubReq.MediaURL, pubReq.Caption)
	}

	if err != nil {
		slog.Error("Failed to publish content", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to publish: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "published",
		"media_id": mediaID,
	})
}

// loggingMiddleware logs all incoming HTTP requests.
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

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

