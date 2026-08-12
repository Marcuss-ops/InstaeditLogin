// Package editor implements the Editor BFF module. It exposes a
// single bounded catch-all route under /api/v1/editor/* that proxies
// the InstaEditor SPA's API calls to the Velox master.
package editor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/editorlaunch"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// ProxyClient is the narrow contract the EditorBFFModule needs from
// the veloxclient. It is implemented by *veloxclient.Client.
type ProxyClient interface {
	Proxy(ctx context.Context, method, path string, userID, workspaceID int64, body io.Reader, contentType string, scopes []string) (*http.Response, error)
}

type projectProxyClient interface {
	ProxyForProject(ctx context.Context, method, path string, userID, workspaceID int64, projectID string, body io.Reader, contentType string, scopes []string) (*http.Response, error)
}

// scopesForPath maps an editor operation to the narrow editor grant.
func scopesForPath(method, path string) []string {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return []string{veloxcontract.ScopeVeloxEditorRead}
	}
	return []string{veloxcontract.ScopeVeloxEditorWrite}
}

// Deps carries the injectable dependencies for the editor BFF module.
type Deps struct {
	Client            ProxyClient
	AuthMiddleware    func(http.Handler) http.Handler
	CSRFMiddleware    func(http.Handler) http.Handler
	AuthorizeProject  func(ctx context.Context, userID, workspaceID int64, projectID string, write bool) error
	LaunchTokenIssuer LaunchTokenIssuer
}

type EditorBFFModule struct {
	deps Deps
}

func NewEditorBFFModule(deps Deps) *EditorBFFModule {
	return &EditorBFFModule{deps: deps}
}

func (m *EditorBFFModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	if m.deps.LaunchTokenIssuer != nil {
		launch := m.launchHandler(m.deps.LaunchTokenIssuer)
		if m.deps.CSRFMiddleware != nil {
			launch = m.deps.CSRFMiddleware(launch)
		}
		if m.deps.AuthMiddleware != nil {
			launch = m.deps.AuthMiddleware(launch)
		}
		mux.Method(http.MethodPost, LaunchTokenPath, launch)
		mux.Method(http.MethodPost, LaunchTokenExchangePath, m.exchangeHandler(m.deps.LaunchTokenIssuer))
	}
	handler := m.proxyHandler()
	if m.deps.CSRFMiddleware != nil {
		handler = m.deps.CSRFMiddleware(handler)
	}
	handler = m.editorAuthMiddleware(handler)
	mux.Handle("/api/v1/editor/*", handler)
}

func (m *EditorBFFModule) proxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		identity := auth.IdentityFromContext(req.Context())
		if identity == nil {
			writeError(w, http.StatusUnauthorized, "missing identity")
			return
		}
		userID := identity.UserID()
		workspaceID := identity.WorkspaceID()
		if userID <= 0 || workspaceID <= 0 {
			writeError(w, http.StatusForbidden, "session missing workspace or user scope")
			return
		}

		path := req.URL.Path
		const prefix = "/api/v1/editor"
		if len(path) >= len(prefix) {
			path = path[len(prefix):]
		}
		projectID := projectIDFromEditorPath(path)
		if projectID == "" {
			// A launch-session bearer is already project-scoped. Allow
			// editor support endpoints (uploads, presets, folders, etc.)
			// that do not repeat the project in their URL to inherit that
			// authenticated project, while still rejecting unscoped
			// cookie-authenticated requests.
			if claims := editorlaunch.ClaimsFromContext(req.Context()); claims != nil {
				projectID = claims.ProjectID
			} else {
				writeError(w, http.StatusNotFound, "editor project context not found")
				return
			}
		}
		if m.deps.AuthorizeProject == nil {
			writeError(w, http.StatusServiceUnavailable, "editor project authorization unavailable")
			return
		}
		write := req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions
		if err := m.deps.AuthorizeProject(req.Context(), userID, workspaceID, projectID, write); err != nil {
			writeError(w, http.StatusNotFound, "editor project context not found")
			return
		}

		projectClient, ok := m.deps.Client.(projectProxyClient)
		if !ok {
			writeError(w, http.StatusNotImplemented, "project-scoped editor bridge unavailable")
			return
		}
		resp, err := projectClient.ProxyForProject(req.Context(), req.Method, path, userID, workspaceID, projectID, req.Body, req.Header.Get("Content-Type"), scopesForPath(req.Method, path))
		if err != nil {
			slog.Error("editor bff: proxy failed", "workspace_id", workspaceID, "err", err)
			writeError(w, http.StatusBadGateway, "upstream editor call failed")
			return
		}
		// A newly-created editor session has no persisted canvas document yet.
		// Velox correctly returns 404 for that empty state, but the editor
		// intentionally falls back to the InstaEdit session thumbnail. Return a
		// typed empty-document response so that expected bootstrap state does not
		// appear as a failed request in the browser console.
		if req.Method == http.MethodGet && strings.HasSuffix(path, "/document") && resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"document_exists":false,"canvas_json":{"width":1920,"height":1080,"objects":[]}}`)
			return
		}
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			slog.Error("editor bff: copy upstream response failed", "workspace_id", workspaceID, "err", err)
		}
	})
}

func (m *EditorBFFModule) editorAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if auth.IdentityFromContext(req.Context()) != nil {
			next.ServeHTTP(w, req)
			return
		}
		projectID := projectIDFromEditorPath(strings.TrimPrefix(req.URL.Path, "/api/v1/editor"))
		if m.deps.LaunchTokenIssuer != nil {
			raw := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
			if raw != "" {
				scope := editorlaunch.ScopeRead
				if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
					scope = editorlaunch.ScopeWrite
				}
				if claims, err := m.deps.LaunchTokenIssuer.VerifySession(raw, projectID, scope); err == nil {
					ctx := editorlaunch.WithClaims(req.Context(), claims)
					ctx = auth.WithIdentity(ctx, auth.NewUserIdentity(claims.UserID, claims.WorkspaceID, 0))
					next.ServeHTTP(w, req.WithContext(ctx))
					return
				}
			}
		}
		if m.deps.AuthMiddleware != nil {
			m.deps.AuthMiddleware(next).ServeHTTP(w, req)
			return
		}
		writeError(w, http.StatusUnauthorized, "missing identity")
	})
}

var editorProjectIDPattern = regexp.MustCompile(`^(?:ve_|vx_)[A-Za-z0-9_-]{1,125}$`)

func projectIDFromEditorPath(path string) string {
	const prefix = "/projects/"
	if !strings.HasPrefix(path, prefix) || strings.ContainsAny(path, "\r\n") {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	projectID, _, _ := strings.Cut(remainder, "/")
	projectID = strings.TrimSpace(projectID)
	if !editorProjectIDPattern.MatchString(projectID) {
		return ""
	}
	return projectID
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
