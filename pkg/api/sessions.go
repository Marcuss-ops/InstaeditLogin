// Package api — session management HTTP surface.
//
// Endpoints:
//   POST /api/v1/auth/refresh         — rotate refresh token, issue new pair
//   POST /api/v1/auth/logout          — revoke the current session
//   POST /api/v1/auth/logout-all      — revoke every active session for the user
//   GET  /api/v1/auth/sessions        — list active sessions (audit UI)
//   DELETE /api/v1/auth/sessions/{id} — revoke a specific session (other than the current)
//
// All endpoints live behind the JWT middleware except /auth/refresh
// and /auth/logout which are unauthenticated (the cookie IS the
// credential). /auth/refresh, /auth/logout, and /auth/logout-all are
// exempted from CSRF by router config; the session list endpoints
// are CSRF-protected because they are cookie-authenticated writes
// (DELETE in particular).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// setSessionCookie writes the 3 cookies that comprise an
// authenticated session: the access JWT (HttpOnly, short TTL),
// the refresh token (HttpOnly, long TTL), and a fresh CSRF
// token (readable by document.cookie). Called from
// /auth/exchange, /auth/refresh, and the email login/register
// endpoints.
//
// SPRINT 7.2 follow-up: was a free function
// (writeSessionCookies) that took `secure` as a positional
// bool. Refactored to a method on *Router so it can use
// r.cookieSecure + r.csrfConfig() directly (parity with
// handleExchangeCode which already uses r.csrfConfig()).
// Every call site was updated in the same commit; the
// free-function form is removed.
func (r *Router) setSessionCookie(w http.ResponseWriter, req *http.Request, res *services.StartSessionResult) {
	cfg := r.csrfConfig()
	secure := r.cookieSecure
	// Access JWT cookie (HttpOnly, short TTL).
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    res.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(time.Until(res.AccessExpiresAt).Seconds()),
	})
	// Refresh cookie (HttpOnly, long TTL).
	http.SetCookie(w, &http.Cookie{
		Name:     auth.RefreshCookieName,
		Value:    res.RefreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(time.Until(res.RefreshExpiresAt).Seconds()),
	})
	// CSRF token (readable by document.cookie). The token is
	// freshly generated on every set so the post-login token
	// cannot be guessed by a pre-login attacker (the
	// SetCSRFToken docs in csrf.go for the full rationale).
	_, _ = auth.SetCSRFToken(w, cfg)
	_ = req // reserved for future per-request cookie-domain
	// pinning (e.g. setting Domain from the request host when
	// the SPA and API share a parent domain but live on
	// different subdomains).
}

// clearSessionCookie clears the same 3 cookies and removes the
// CSRF token cookie. Called from /auth/logout, /auth/logout-all,
// /auth/refresh on session-reuse-detected, and the workspace
// switch endpoint when the user changes active workspace.
//
// SPRINT 7.2 follow-up: was a free function (clearSessionCookies).
// Now a method on *Router; same parity rationale as
// setSessionCookie.
func (r *Router) clearSessionCookie(w http.ResponseWriter) {
	cfg := r.csrfConfig()
	for _, name := range []string{auth.SessionCookieName, auth.RefreshCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   r.cookieSecure,
			SameSite: http.SameSiteNoneMode,
			MaxAge:   -1,
		})
	}
	auth.ClearCSRFToken(w, cfg)
}

func readRefreshCookie(r *http.Request) string {
	if c, err := r.Cookie(auth.RefreshCookieName); err == nil {
		return c.Value
	}
	return ""
}

// handleRefresh rotates the refresh token from the cookie. Returns
// 401 on missing/invalid/expired/reused.
func (h *Router) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if h.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured")
		return
	}
	plain := readRefreshCookie(r)
	if plain == "" {
		writeError(w, http.StatusUnauthorized, "no refresh token")
		return
	}
	res, err := h.sessionsSvc.Refresh(services.RefreshRequest{
		RefreshPlaintext: plain,
		UserAgent:        r.UserAgent(),
		IP:               clientIP(r),
	})
	if err != nil {
		switch {
		case errors.Is(err, services.ErrSessionReuse):
			h.clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "refresh token reuse detected; all sessions revoked")
		case errors.Is(err, services.ErrSessionNotFound):
			h.clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		default:
			writeError(w, http.StatusInternalServerError, "refresh failed")
		}
		return
	}
	h.setSessionCookie(w, r, res)
	w.WriteHeader(http.StatusNoContent)
}

// handleLogout revokes the session whose refresh-token hash matches
// the current cookie. Idempotent: returns 204 even when no row is
// found (already logged out).
func (h *Router) handleLogout(w http.ResponseWriter, r *http.Request) {
	if h.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured")
		return
	}
	_ = h.sessionsSvc.WithdrawFromCookie(readRefreshCookie(r))
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleLogoutAll revokes every active session for the
// authenticated user.
func (h *Router) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	if h.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured")
		return
	}
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if _, err := h.sessionsSvc.RevokeAll(uid, "logout_all"); err != nil {
		writeError(w, http.StatusInternalServerError, "logout-all failed")
		return
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// sessionListItem is the wire shape returned by GET /auth/sessions.
type sessionListItem struct {
	ID           int64  `json:"id"`
	WorkspaceID  int64  `json:"workspace_id"`
	CreatedAt    string `json:"created_at"`
	LastUsedAt   string `json:"last_used_at"`
	ExpiresAt    string `json:"expires_at"`
	RevokedAt    string `json:"revoked_at,omitempty"`
	RevokeReason string `json:"revoke_reason,omitempty"`
	Current      bool   `json:"current"`
	UserAgent    string `json:"user_agent,omitempty"`
}

// handleListSessions returns all sessions for the authenticated user
// (active and revoked), with a `current: true` flag on the one bound
// to the request's session id.
func (h *Router) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if h.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured")
		return
	}
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	currentSID, _ := auth.SessionIDFromContext(r.Context())
	rows, err := h.sessionsSvc.List(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	out := make([]sessionListItem, 0, len(rows))
	for _, s := range rows {
		item := sessionListItem{
			ID:          s.ID,
			WorkspaceID: s.WorkspaceID,
			CreatedAt:   s.CreatedAt.UTC().Format(time.RFC3339),
			LastUsedAt:  s.LastUsedAt.UTC().Format(time.RFC3339),
			ExpiresAt:   s.ExpiresAt.UTC().Format(time.RFC3339),
			UserAgent:   s.UserAgent,
		}
		if s.RevokedAt != nil {
			item.RevokedAt = s.RevokedAt.UTC().Format(time.RFC3339)
			item.RevokeReason = s.RevokeReason
		}
		if s.ID == currentSID {
			item.Current = true
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": out})
}

// handleDeleteSession revokes a specific session by id. The current
// session cannot be deleted via this endpoint (use /auth/logout) so
// the SPA cannot accidentally log itself out.
func (h *Router) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if h.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured")
		return
	}
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	currentSID, _ := auth.SessionIDFromContext(r.Context())
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/auth/sessions/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if id == currentSID {
		writeError(w, http.StatusBadRequest, "use /auth/logout to revoke the current session")
		return
	}
	if err := h.sessionsSvc.Revoke(id, uid, "user_revoked"); err != nil {
		switch {
		case errors.Is(err, services.ErrSessionForbidden):
			writeError(w, http.StatusForbidden, "session belongs to another user")
		case errors.Is(err, services.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, "session not found")
		default:
			writeError(w, http.StatusInternalServerError, "revoke failed")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientIP returns the first X-Forwarded-For hop, falling back to
// X-Real-IP, and finally to net.SplitHostPort(r.RemoteAddr). Stable
// IP hashing for rate-limit / SessionsService.IPHash requires
// stripping the ephemeral port if present (otherwise every reconnect
// from the same client produces a different hash — defeating the
// per-IP / per-workspace rate scopes).
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// withCtx is exported for tests; keeps the linter happy on packages
// that import the api package directly.
var _ = context.Background
