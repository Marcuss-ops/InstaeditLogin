// Package editor implements the Editor BFF module. It exposes a
// single bounded catch-all route under /api/v1/editor/* that proxies
// the Dark Editor SPA's API calls to the Velox master. The route is
// protected by InstaEdit's session authentication and CSRF middleware;
// the module signs a short-lived control JWT so the Velox master can
// verify the request came from the InstaEdit BFF and enforce workspace
// scoping.
//
// DESIGN:
//   - The browser never talks directly to the Velox master.
//   - InstaEdit owns the user's session and workspace context.
//   - Every request is signed with a workspace-scoped control JWT.
//   - The path under /api/v1/editor is forwarded verbatim under
//     /api/v1/instaedit/editor on the Velox master.
package editor

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// ProxyClient is the narrow contract the EditorBFFModule needs from
// the veloxclient. It is implemented by *veloxclient.Client.
//
// scopes MUST be non-empty: the transport client fails closed on an
// empty slice (the historical all-scopes-superset fallback was
// removed — the scope decision lives HERE at the call site, where it
// is auditable, not hidden in the transport client).
type ProxyClient interface {
	Proxy(ctx context.Context, method, path string, userID, workspaceID int64, body io.Reader, contentType string, scopes []string) (*http.Response, error)
}

// scopesForMethod maps the proxied HTTP method to the control-JWT
// scopes signed into the outbound request (per-operation grants,
// architect verdict Q2). Reads carry only editor.project.read;
// mutating methods carry the write-side grants. Velox's per-route
// middleware accepts "exact OR superset" (HasAllScopes), so a write
// route needing only editor.project.write is satisfied while a read
// token can never mutate.
func scopesForMethod(method string) []string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return []string{veloxcontract.ScopeEditorProjectRead}
	default:
		return []string{
			veloxcontract.ScopeEditorProjectWrite,
			veloxcontract.ScopeEditorAssetUpload,
			veloxcontract.ScopeYouTubeSessionPublish,
		}
	}
}

// Deps carries the injectable dependencies for the editor BFF module.
type Deps struct {
	Client         ProxyClient
	AuthMiddleware func(http.Handler) http.Handler
	CSRFMiddleware func(http.Handler) http.Handler
}

// EditorBFFModule mounts the /api/v1/editor/* catch-all proxy.
// Registration is a no-op when the Router has no editor client wired.
type EditorBFFModule struct {
	deps Deps
}

// NewEditorBFFModule creates the module.
func NewEditorBFFModule(deps Deps) *EditorBFFModule {
	return &EditorBFFModule{deps: deps}
}

// Register mounts the editor BFF routes on mux.
func (m *EditorBFFModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	handler := m.proxyHandler()
	if m.deps.CSRFMiddleware != nil {
		handler = m.deps.CSRFMiddleware(handler)
	}
	if m.deps.AuthMiddleware != nil {
		handler = m.deps.AuthMiddleware(handler)
	}
	mux.Handle("/api/v1/editor/*", handler)
}

// proxyHandler returns an http.Handler that proxies any request under
// /api/v1/editor/{path} to the Velox master under
// /api/v1/instaedit/editor/{path}.
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

		// The chi route is /api/v1/editor/*; rctx.URL.Pattern is the
		// matched pattern. We want the wildcard suffix after the prefix.
		path := req.URL.Path
		const prefix = "/api/v1/editor"
		if len(path) >= len(prefix) {
			path = path[len(prefix):]
		}

		contentType := req.Header.Get("Content-Type")
		resp, err := m.deps.Client.Proxy(req.Context(), req.Method, path, userID, workspaceID, req.Body, contentType, scopesForMethod(req.Method))
		if err != nil {
			slog.Error("editor bff: proxy failed", "workspace_id", workspaceID, "err", err)
			writeError(w, http.StatusBadGateway, "upstream editor call failed")
			return
		}
		defer resp.Body.Close()

		// Copy headers.
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

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
