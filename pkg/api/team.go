// Package api provides HTTP handlers and middleware for workspace team
// management: invites, member listing, member removal, and role-based
// authorization.
//
// FASE 2.3: Workspace team management.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TeamStore is the interface consumed by team handlers. Defined locally to
// keep the api package independent from repository imports.
type TeamStore interface {
	AddMember(workspaceID, userID int64, role string) error
	RemoveMember(workspaceID, userID int64) error
	ListMembers(workspaceID int64) ([]models.WorkspaceMember, error)
	GetRole(workspaceID, userID int64) (string, error)
	IsAdmin(workspaceID, userID int64) (bool, error)
	CreateInvite(workspaceID, invitedBy int64, email, role string) (*models.WorkspaceInvite, error)
	FindInviteByToken(token string) (*models.WorkspaceInvite, error)
	AcceptInvite(token string, userID int64) error
} // Role hierarchy: admin > editor > viewer.
var roleRank = map[string]int{
	"admin":  3,
	"editor": 2,
	"viewer": 1,
}

// requireWorkspaceRole returns middleware that checks the caller has at
// least the given role in the workspace identified by the URL param.
// The paramName is the chi path variable (e.g. "id" for /workspaces/{id}).
func (r *Router) requireWorkspaceRole(minRole, paramName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if r.teamStore == nil {
				writeError(w, http.StatusNotImplemented, "team management not configured")
				return
			}

			// Workspace id from URL param.
			wsID, ok := parsePathIDAsInt64(w, req, paramName)
			if !ok {
				return
			}

			// User id from auth context.
			id := auth.IdentityFromContext(req.Context())
			if id == nil {
				writeError(w, http.StatusUnauthorized, "missing identity")
				return
			}
			userID := id.UserID()

			role, err := r.teamStore.GetRole(wsID, userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to check role: "+err.Error())
				return
			}
			if role == "" {
				writeError(w, http.StatusForbidden, "not a member of this workspace")
				return
			}
			if roleRank[role] < roleRank[minRole] {
				writeError(w, http.StatusForbidden, "insufficient role: requires "+minRole)
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

// -----------------------------------------------------------------------
//  Route registration
// -----------------------------------------------------------------------

// registerTeamRoutes adds workspace team endpoints to the chi mux.
func (r *Router) registerTeamRoutes() {
	// Invite acceptance — requires auth (the invitee must be logged in).
	r.mux.Method(http.MethodGet, "/api/v1/invites/{token}", r.protected(r.handleAcceptInvite))

	// Team routes under /api/v1/workspaces/{id}/...
	r.mux.Route("/api/v1/workspaces/{id}", func(sr chi.Router) {
		// Member listing — any member can view.
		sr.Group(func(gr chi.Router) {
			gr.Use(r.requireWorkspaceRole("viewer", "id"))
			gr.Get("/members", r.handleListMembers)
		})

		// Invite — admin only.
		sr.Group(func(gr chi.Router) {
			gr.Use(r.requireWorkspaceRole("admin", "id"))
			gr.Post("/invites", r.handleCreateInvite)
			gr.Delete("/members/{userId}", r.handleRemoveMember)
		})
	})
}

// -----------------------------------------------------------------------
//  Handlers
// -----------------------------------------------------------------------

// handleCreateInvite handles POST /api/v1/workspaces/{id}/invites.
func (r *Router) handleCreateInvite(w http.ResponseWriter, req *http.Request) {
	wsID, _ := parsePathIDAsInt64(w, req, "id")

	id := auth.IdentityFromContext(req.Context())

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if body.Role == "" {
		body.Role = "editor"
	}
	if roleRank[body.Role] == 0 {
		writeError(w, http.StatusBadRequest, "invalid role: must be admin, editor, or viewer")
		return
	}

	invite, err := r.teamStore.CreateInvite(wsID, id.UserID(), body.Email, body.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, invite)
}

// handleAcceptInvite handles GET /api/v1/invites/{token}.
func (r *Router) handleAcceptInvite(w http.ResponseWriter, req *http.Request) {
	token := req.PathValue("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	// The invitee must be authenticated.
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "login required to accept invite")
		return
	}

	if err := r.teamStore.AcceptInvite(token, id.UserID()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "invite accepted"})
}

// handleRemoveMember handles DELETE /api/v1/workspaces/{id}/members/{userId}.
func (r *Router) handleRemoveMember(w http.ResponseWriter, req *http.Request) {
	wsID, _ := parsePathIDAsInt64(w, req, "id")
	userID, ok := parsePathIDAsInt64(w, req, "userId")
	if !ok {
		return
	}

	// Don't let the caller remove themselves (prevents locking out all admins).
	id := auth.IdentityFromContext(req.Context())
	if id != nil && id.UserID() == userID {
		writeError(w, http.StatusBadRequest, "cannot remove yourself")
		return
	}

	if err := r.teamStore.RemoveMember(wsID, userID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListMembers handles GET /api/v1/workspaces/{id}/members.
func (r *Router) handleListMembers(w http.ResponseWriter, req *http.Request) {
	wsID, _ := parsePathIDAsInt64(w, req, "id")

	members, err := r.teamStore.ListMembers(wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members: "+err.Error())
		return
	}
	if members == nil {
		members = []models.WorkspaceMember{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"members": members})
}
