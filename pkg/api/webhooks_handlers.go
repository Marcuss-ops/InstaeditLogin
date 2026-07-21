// Package api — webhook HTTP handlers (SPRINT 4.2).
//
// Endpoints:
//
//	POST   /api/v1/webhooks/endpoints          (auth) — create
//	GET    /api/v1/webhooks/endpoints          (auth) — list
//	DELETE /api/v1/webhooks/endpoints/{id}     (auth) — remove
//	POST   /api/v1/webhooks/deliveries/{id}/replay (auth) — manual replay
//
// All endpoints are workspace-scoped: the JWT must be a member of
// the endpoint's workspace. The active workspace is read from the
// JWT's ws claim (UserIdentity.WorkspaceID).
//
// The dispatcher's POST work happens in a background goroutine
// (internal/worker/webhook_worker.go) — the handlers only manage
// the endpoint configuration + the manual replay flag.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// WebhookStore is the persistence contract for the webhook runtime.
// Defined inline to keep pkg/api off internal/repository imports
// for the test fakes. main.go injects *repository.WebhookRepository
// which satisfies it via duck-typing (the real methods take
// context.Context, which matches the interface signature here).
type WebhookStore interface {
	CreateEndpoint(ctx context.Context, e *repository.WebhookEndpoint) error
	ListEndpointsForWorkspace(ctx context.Context, workspaceID int64, includeDisabled bool) ([]repository.WebhookEndpoint, error)
	DeleteEndpoint(ctx context.Context, id int64) error
	FindEndpointByID(ctx context.Context, id int64) (*repository.WebhookEndpoint, error)
	MarkReplay(ctx context.Context, id int64) error
}

// registerWebhookRoutes is called from Setup() when r.webhookStore
// is wired. The endpoints are JWT-authenticated and workspace-scoped
// (the active workspace is read from the JWT, not the request body,
// to prevent cross-tenant endpoint creation).
func (r *Router) registerWebhookRoutes() {
	if r.webhookStore == nil {
		return
	}
	r.mux.Route("/api/v1/webhooks", func(sr chi.Router) {
		sr.Post("/endpoints", r.protected(r.handleCreateWebhookEndpoint))
		sr.Get("/endpoints", r.protected(r.handleListWebhookEndpoints))
		sr.Delete("/endpoints/{id}", r.protected(r.handleDeleteWebhookEndpoint))
		sr.Post("/deliveries/{id}/replay", r.protected(r.handleReplayWebhookDelivery))
	})
}

// handleCreateWebhookEndpoint creates a new endpoint for the
// active workspace. Body: { url, secret, events[], status }.
func (r *Router) handleCreateWebhookEndpoint(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	var body struct {
		URL    string   `json:"url"`
		Secret string   `json:"secret"`
		Events []string `json:"events"`
		Status string   `json:"status"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.URL == "" || body.Secret == "" {
		writeError(w, http.StatusBadRequest, "url and secret are required")
		return
	}
	if body.Status == "" {
		body.Status = "active"
	}
	if body.Status != "active" && body.Status != "disabled" {
		writeError(w, http.StatusBadRequest, "status must be 'active' or 'disabled'")
		return
	}
	ep := &repository.WebhookEndpoint{
		WorkspaceID: id.WorkspaceID(),
		URL:         body.URL,
		Secret:      body.Secret,
		Events:      body.Events,
		Status:      body.Status,
	}
	if err := r.webhookStore.CreateEndpoint(req.Context(), ep); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create endpoint: "+err.Error())
		return
	}
	// Don't echo the secret back.
	ep.Secret = ""
	writeJSON(w, http.StatusCreated, ep)
}

// handleListWebhookEndpoints returns the active endpoints for the
// active workspace. include_disabled=true query param surfaces the
// disabled ones too.
func (r *Router) handleListWebhookEndpoints(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	includeDisabled := req.URL.Query().Get("include_disabled") == "true"
	endpoints, err := r.webhookStore.ListEndpointsForWorkspace(req.Context(), id.WorkspaceID(), includeDisabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list endpoints: "+err.Error())
		return
	}
	// Don't echo the secret back.
	for i := range endpoints {
		endpoints[i].Secret = ""
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"endpoints": endpoints})
}

// handleDeleteWebhookEndpoint removes the endpoint. The active
// workspace's membership is the access guard.
func (r *Router) handleDeleteWebhookEndpoint(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	epID, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	// Workspace scope: refuse to delete an endpoint that belongs
	// to a different workspace.
	ep, err := r.webhookStore.FindEndpointByID(req.Context(), epID)
	if err != nil {
		if errors.Is(err, repository.ErrWebhookEndpointNotFound) {
			writeError(w, http.StatusNotFound, "endpoint not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load endpoint: "+err.Error())
		return
	}
	if ep.WorkspaceID != id.WorkspaceID() {
		writeError(w, http.StatusForbidden, "endpoint belongs to a different workspace")
		return
	}
	if err := r.webhookStore.DeleteEndpoint(req.Context(), epID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete endpoint: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReplayWebhookDelivery resets a 'dead' delivery for manual
// replay. Returns 200 on success, 404 if the delivery is not
// in 'dead' state or does not exist.
func (r *Router) handleReplayWebhookDelivery(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	// Parse the delivery id from the path via chi's path value
	// (the route /deliveries/{id}/replay already matched via
	// chi.Router, so the id is in req.PathValue).
	deliveryID, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || deliveryID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid delivery id")
		return
	}
	if err := r.webhookStore.MarkReplay(req.Context(), deliveryID); err != nil {
		if errors.Is(err, repository.ErrWebhookDeliveryNotFound) {
			writeError(w, http.StatusNotFound, "delivery not found or not in dead state")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to replay: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"delivery_id": deliveryID,
		"status":      "pending",
		"message":     "delivery reset; will be picked up on the next worker tick",
	})
}
