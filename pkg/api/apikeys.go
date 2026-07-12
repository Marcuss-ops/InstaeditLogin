// Package api — API key management endpoints (Taglio 4.6 / Taglio 5c).
//
// Route group: /api/v1/api-keys/*  (mounting in handlers.go Setup()).
//
//   POST   /api/v1/api-keys/              → handleCreateApiKey
//   GET    /api/v1/api-keys/              → handleListApiKeys
//   GET    /api/v1/api-keys/{id}          → handleGetApiKey
//   DELETE /api/v1/api-keys/{id}          → handleDeleteApiKey
//   POST   /api/v1/api-keys/{id}/rotate   → handleRotateApiKey
//
// SAFETY invariants:
//   * plaintext is shipped ONLY in Create / Rotate responses.
//   * Revoke persists revoked_at = NOW(); revoked keys can never authenticate.
//   * Cross-workspace access fails with 404 (not 403).
//   * Writer operations (Create/Revoke/Rotate) require admin OR accounts:manage.
//
// Taglio 5c: tenant anchor is WorkspaceID (was OrganizationID). ProjectID removed.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// CreateApiKeyRequest is the JSON body for POST /api/v1/api-keys.
type CreateApiKeyRequest struct {
	Name        string     `json:"name"`
	Environment string     `json:"environment,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ApiKeyCreatedResponse is the response shape for Create + Rotate.
// Plaintext is included ONCE.
type ApiKeyCreatedResponse struct {
	Key       *models.ApiKey `json:"key"`
	Plaintext string         `json:"plaintext"`
}

func mapApiKeyError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, repository.ErrApiKeyNotFound):
		return http.StatusNotFound, "api key not found"
	case errors.Is(err, repository.ErrApiKeyHashCollided):
		return http.StatusConflict, "api key hash collision; retry"
	default:
		return http.StatusInternalServerError, "failed to process api key: " + err.Error()
	}
}

func requireIdentity(w http.ResponseWriter, req *http.Request, requiredPerm string) (auth.Identity, bool) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing authentication")
		return nil, false
	}
	if requiredPerm != "" && !id.HasPermission(requiredPerm) {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("missing required permission: %s", requiredPerm))
		return nil, false
	}
	return id, true
}

func requireWriteCapability(w http.ResponseWriter, req *http.Request) (auth.Identity, bool) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing authentication")
		return nil, false
	}
	if id.IsAPIKey() && !id.HasPermission(models.PermissionAccountsManage) {
		writeError(w, http.StatusForbidden,
			"api key requires the \""+models.PermissionAccountsManage+"\" permission to manage keys")
		return nil, false
	}
	return id, true
}

// --- Handlers ---------------------------------------------------------------

func (r *Router) handleCreateApiKey(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireWriteCapability(w, req)
	if !ok {
		return
	}
	var body CreateApiKeyRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	env := body.Environment
	if env == "" {
		env = models.ApiKeyEnvironmentTest
	}
	if !models.IsApiKeyEnvironment(env) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("environment must be %q or %q",
				models.ApiKeyEnvironmentTest, models.ApiKeyEnvironmentLive))
		return
	}
	permissions := body.Permissions
	if len(permissions) == 0 {
		permissions = models.DefaultApiKeyPermissions
	}
	if ok, unknown := models.ValidateApiKeyPermissions(permissions); !ok {
		writeError(w, http.StatusBadRequest, "unknown permission: "+unknown)
		return
	}

	plaintext, keyPrefix, err := auth.Generate(env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate api key: "+err.Error())
		return
	}
	key := &models.ApiKey{
		WorkspaceID: id.WorkspaceID(),
		CreatedBy:   id.UserID(),
		Name:        body.Name,
		Environment: env,
		KeyPrefix:   keyPrefix,
		Permissions: permissions,
		ExpiresAt:   body.ExpiresAt,
	}
	h := auth.Hash(plaintext)
	if h == nil {
		writeError(w, http.StatusInternalServerError, "failed to hash api key")
		return
	}
	if err := r.apiKeyStore.Create(key, h); err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to create api key: "+msg)
		return
	}
	r.emitApiKeyAudit(req.Context(), models.AuditActionApiKeyCreated, id, key)
	writeJSON(w, http.StatusCreated, ApiKeyCreatedResponse{
		Key:       key,
		Plaintext: plaintext,
	})
}

func (r *Router) handleListApiKeys(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireIdentity(w, req, "")
	if !ok {
		return
	}
	keys, err := r.apiKeyStore.ListByWorkspace(id.WorkspaceID())
	if err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to list api keys: "+msg)
		return
	}
	if keys == nil {
		keys = []models.ApiKey{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"keys": keys,
	})
}

func (r *Router) handleGetApiKey(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireIdentity(w, req, "")
	if !ok {
		return
	}
	keyID, perr := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if perr != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid api key id")
		return
	}
	key, err := r.apiKeyStore.FindByIDForWorkspace(id.WorkspaceID(), keyID)
	if err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to get api key: "+msg)
		return
	}
	if key == nil {
		writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	writeJSON(w, http.StatusOK, key)
}

func (r *Router) handleDeleteApiKey(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireWriteCapability(w, req)
	if !ok {
		return
	}
	keyID, perr := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if perr != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid api key id")
		return
	}
	existing, ferr := r.apiKeyStore.FindByIDForWorkspace(id.WorkspaceID(), keyID)
	if ferr != nil {
		code, msg := mapApiKeyError(ferr)
		writeError(w, code, "failed to lookup api key: "+msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := r.apiKeyStore.Revoke(id.WorkspaceID(), keyID); err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to revoke api key: "+msg)
		return
	}
	r.emitApiKeyAudit(req.Context(), models.AuditActionApiKeyRevoked, id, existing)
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleRotateApiKey(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireWriteCapability(w, req)
	if !ok {
		return
	}
	oldID, perr := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if perr != nil || oldID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid api key id")
		return
	}
	existing, ferr := r.apiKeyStore.FindByIDForWorkspace(id.WorkspaceID(), oldID)
	if ferr != nil {
		code, msg := mapApiKeyError(ferr)
		writeError(w, code, "failed to lookup api key: "+msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	plaintext, keyPrefix, err := auth.Generate(existing.Environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate rotated api key: "+err.Error())
		return
	}
	h := auth.Hash(plaintext)
	if h == nil {
		writeError(w, http.StatusInternalServerError, "failed to hash rotated api key")
		return
	}
	newKey := &models.ApiKey{
		WorkspaceID: existing.WorkspaceID,
		CreatedBy:   existing.CreatedBy,
		Name:        existing.Name,
		Environment: existing.Environment,
		KeyPrefix:   keyPrefix,
		Permissions: existing.Permissions,
		ExpiresAt:   existing.ExpiresAt,
	}
	if err := r.apiKeyStore.Rotate(id.WorkspaceID(), oldID, newKey, h); err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to rotate api key: "+msg)
		return
	}
	r.emitApiKeyAudit(req.Context(), models.AuditActionApiKeyRotated, id, newKey)
	writeJSON(w, http.StatusOK, ApiKeyCreatedResponse{
		Key:       newKey,
		Plaintext: plaintext,
	})
}

func (r *Router) emitApiKeyAudit(ctx context.Context, action string, actor auth.Identity, key *models.ApiKey) {
	if r.auditLogStore == nil || key == nil {
		return
	}
	md := map[string]interface{}{
		"key_prefix":   key.KeyPrefix,
		"name":         key.Name,
		"environment":  key.Environment,
		"workspace_id": key.WorkspaceID,
	}
	if actor != nil {
		md["actor_id"] = actor.UserID()
		if actor.IsAPIKey() {
			md["actor_kind"] = "api_key"
			md["actor_key_id"] = actor.KeyID()
		} else {
			md["actor_kind"] = "user"
		}
	}
	resourceType := "api_key"
	resourceID := strconv.FormatInt(key.ID, 10)
	actorID := ""
	if actor != nil {
		actorID = strconv.FormatInt(actor.UserID(), 10)
	}
	if err := r.auditLogStore.Log(ctx, action, actorID, resourceType,
		resourceID, md); err != nil {
		slog.Warn("api key audit emit failed", "action", action,
			"key_id", key.ID, "error", err)
	}
}

var _ = json.Valid
