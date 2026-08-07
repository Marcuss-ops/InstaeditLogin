package editor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/editorlaunch"
)

const (
	LaunchTokenPath         = "/api/v1/editor/launch"
	LaunchTokenExchangePath = "/api/v1/editor/launch/exchange"
)

type LaunchTokenIssuer interface {
	Issue(userID, workspaceID int64, projectID string, scopes []string) (string, editorlaunch.Claims, error)
	IssueSession(userID, workspaceID int64, projectID string, scopes []string) (string, editorlaunch.Claims, error)
	Verify(raw, projectID, requiredScope string) (*editorlaunch.Claims, error)
	Consume(raw, projectID, requiredScope string) (*editorlaunch.Claims, error)
	VerifySession(raw, projectID, requiredScope string) (*editorlaunch.Claims, error)
}

type launchRequest struct {
	ProjectID string `json:"project_id"`
}

type launchResponse struct {
	Token       string `json:"launch_token"`
	ExpiresAt   int64  `json:"expires_at"`
	ProjectID   string `json:"project_id"`
	WorkspaceID int64  `json:"workspace_id"`
	UserID      int64  `json:"user_id"`
}

func (m *EditorBFFModule) launchHandler(issuer LaunchTokenIssuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		identity := auth.IdentityFromContext(req.Context())
		if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() <= 0 {
			writeError(w, http.StatusUnauthorized, "missing identity")
			return
		}
		var body launchRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		projectID := strings.TrimSpace(body.ProjectID)
		if projectID == "" || m.deps.AuthorizeProject == nil {
			writeError(w, http.StatusNotFound, "editor project context not found")
			return
		}
		if err := m.deps.AuthorizeProject(req.Context(), identity.UserID(), identity.WorkspaceID(), projectID, false); err != nil {
			writeError(w, http.StatusNotFound, "editor project context not found")
			return
		}
		token, claims, err := issuer.Issue(identity.UserID(), identity.WorkspaceID(), projectID, []string{editorlaunch.ScopeRead, editorlaunch.ScopeWrite})
		if err != nil {
			if errors.Is(err, editorlaunch.ErrSecretNotConfigured) {
				writeError(w, http.StatusServiceUnavailable, "editor launch authentication unavailable")
				return
			}
			writeError(w, http.StatusInternalServerError, "issue editor launch token")
			return
		}
		writeLaunchJSON(w, http.StatusCreated, launchResponse{Token: token, ExpiresAt: claims.ExpiresAt, ProjectID: claims.ProjectID, WorkspaceID: claims.WorkspaceID, UserID: claims.UserID})
	})
}

func (m *EditorBFFModule) exchangeHandler(issuer LaunchTokenIssuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Token     string `json:"launch_token"`
			ProjectID string `json:"project_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 32<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		claims, err := issuer.Consume(body.Token, strings.TrimSpace(body.ProjectID), editorlaunch.ScopeRead)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired editor launch token")
			return
		}
		token, sessionClaims, err := issuer.IssueSession(claims.UserID, claims.WorkspaceID, claims.ProjectID, claims.Scopes)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "editor session authentication unavailable")
			return
		}
		writeLaunchJSON(w, http.StatusCreated, launchResponse{Token: token, ExpiresAt: sessionClaims.ExpiresAt, ProjectID: sessionClaims.ProjectID, WorkspaceID: sessionClaims.WorkspaceID, UserID: sessionClaims.UserID})
	})
}

func writeLaunchJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
