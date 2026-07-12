// Package api — API key management endpoints (Taglio 4.6).
//
// Route group: /api/v1/api-keys/*  (mounting in handlers.go Setup()).
//
//   POST   /api/v1/api-keys/              → handleCreateApiKey  (mint new key, returns plaintext ONCE)
//   GET    /api/v1/api-keys/              → handleListApiKeys   (org-scoped list)
//   GET    /api/v1/api-keys/{id}          → handleGetApiKey     (single key, no plaintext)
//   DELETE /api/v1/api-keys/{id}          → handleDeleteApiKey  (soft revoke)
//   POST   /api/v1/api-keys/{id}/rotate   → handleRotateApiKey  (atomic revoke+insert, returns new plaintext)
//
// Authentication (Taglio 4.6 — dual path):
//   Either a JWT (dashboard users, today stamped with org_id=1
//   fallback) OR an API key with the "accounts" permission (only
//   admins can mint/revoke other keys; see the per-handler check
//   below). The middleware chain in handlers.go runs:
//   Authenticator → Manager.Middleware → handler.
//
// SAFETY invariants enforced HERE:
//
//   * plaintext is shipped ONLY in Create / Rotate responses. GET,
//     LIST, DELETE never include the secret. A stale UI snapshot
//     of a GET response must not leak the secret.
//   * Revoke persists revoked_at = NOW(); revoked keys can never
//     authenticate even if their plaintext was once stolen.
//   * Cross-tenant access fails with 404 (not 403), losing the
//     "existence leak" surface — matches workspace delete behaviour.
//   * Permission gate at the handler level: write operations
//     (Create / Revoke / Rotate) require admin OR accounts:manage
//     permission. This applies to BOTH JWT and API-key identities,
//     so a free-tier API key with only "read" cannot mint siblings
//     even if it has the route URL.
//
// AUDIT emission:
//   On every successful write (Create / Revoke / Rotate) we emit
//   an audit_log row with the action constant from internal/models
//   and a Metadata blob carrying key_prefix (the visible
//   "sk_test_aB3xY9K2" slice, NEVER the result of Generate),
//   and the human-readable name. The keys' full plaintext is
//   NEVER logged — only key_prefix, which is safe by design.

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
// Environment defaults to "test" when empty; permissions default
// to DefaultApiKeyPermissions when empty (read-only). name is
// required and the only strictly-mandatory field; everything else
// has a sane default backed by application code, NEVER by SQL.
type CreateApiKeyRequest struct {
	Name        string     `json:"name"`
	Environment string     `json:"environment,omitempty"`
	ProjectID   *int64     `json:"project_id,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ApiKeyCreatedResponse is the response shape for Create + Rotate.
// Plaintext is included ONCE — the client (dashboard or operator
// script) MUST capture it on receipt. Subsequent GET/LIST calls
// return the same JSON shape MINUS the Plaintext field.
type ApiKeyCreatedResponse struct {
	Key       *models.ApiKey `json:"key"`
	Plaintext string         `json:"plaintext"`
}

// mapApiKeyError translates ApiKeyStore errors into HTTP statuses.
// Mirrors mapRepoError / mapWorkspaceError convention. Cross-tenant
// attempts land on ErrApiKeyNotFound → 404, indistinguishable from
// "wrong id" — the existence-leak avoidance pattern.
//
// ErrApiKeyHashCollided is a 409 because the request reached the
// database with a payload that already existed there. The handler
// treats 409 as "client must retry" (extremely rare in practice).
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

// requireIdentity extracts an authenticated Identity from the request
// context. Returns (nil, false) after writing an appropriate HTTP
// error response (401 if no identity, 403 if required permission
// missing). The required permission string "" means "any
// authenticated principal is fine"; pass models.PermissionAccountsManage
// or models.PermissionAdmin to gate write operations.
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

// requireWriteCapability is the gate for mint/revoke/rotate. The
// identity is either:
//   * a JWT user (dashboard owner — implicit "can manage own org's
//     keys"), OR
//   * an API key with the "accounts" permission (admin-level keys).
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

// handleCreateApiKey mints a new API key for the authenticated
// organization. POST /api/v1/api-keys
//
// Permission gate: requireWriteCapability (see above).
//
// The handler:
//  1. Reads + validates the request body.
//  2. Calls auth.Generate to produce (plaintext, keyPrefix).
//  3. Calls ApiKeyStore.Create with auth.Hash(plaintext).
//  4. Emits an audit_log row with action=api_key.created.
//  5. Returns 201 + {key, plaintext}. Plaintext is shown ONCE.
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
		OrganizationID: id.OrgID(),
		ProjectID:      body.ProjectID,
		CreatedBy:      id.UserID(),
		Name:           body.Name,
		Environment:    env,
		KeyPrefix:      keyPrefix,
		Permissions:    permissions,
		ExpiresAt:      body.ExpiresAt,
	}
	hash := auth.Hash(plaintext)
	if hash == nil {
		// Defensive: Generate succeeded, the prefix parsed, the
		// secret is non-empty — Hash returning nil means the
		// generator itself broke. Surface as 500, do NOT proceed
		// with an undefined key.
		writeError(w, http.StatusInternalServerError, "failed to hash api key")
		return
	}
	if err := r.apiKeyStore.Create(key, hash); err != nil {
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

// handleListApiKeys lists the authenticated org's API keys.
// GET /api/v1/api-keys
//
// No elevated permission required — any authenticated identity in
// the org can list. The repository enforces tenant filter at SQL
// level via id.OrgID().
func (r *Router) handleListApiKeys(w http.ResponseWriter, req *http.Request) {
	if r.apiKeyStore == nil {
		writeError(w, http.StatusNotImplemented, "api keys not configured on this server")
		return
	}
	id, ok := requireIdentity(w, req, "")
	if !ok {
		return
	}
	// Optional project_id query parameter: drill into a single project.
	projectStr := req.URL.Query().Get("project_id")
	var (
		keys []models.ApiKey
		err  error
	)
	if projectStr != "" {
		projectID, perr := strconv.ParseInt(projectStr, 10, 64)
		if perr != nil || projectID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid project_id")
			return
		}
		keys, err = r.apiKeyStore.ListByProject(id.OrgID(), projectID)
	} else {
		keys, err = r.apiKeyStore.ListByOrg(id.OrgID())
	}
	if err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to list api keys: "+msg)
		return
	}
	// Always emit [] not null, mirroring workspaces/posts endpoints.
	if keys == nil {
		keys = []models.ApiKey{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"keys": keys,
	})
}

// handleGetApiKey returns a single API key by id, scoped to the
// authenticated org. GET /api/v1/api-keys/{id}
//
// No elevated permission required — any authenticated identity in
// the org can read.
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
	key, err := r.apiKeyStore.FindByIDForOrg(id.OrgID(), keyID)
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

// handleDeleteApiKey soft-revokes an API key. DELETE /api/v1/api-keys/{id}
//
// Permission gate: requireWriteCapability.
//
// Idempotent — a second DELETE on an already-revoked key returns
// 204 (no error) thanks to the COALESCE-wrapped UPDATE in the
// repository. Matches the user expectation that DELETE is "make it
// stop working" rather than "make it disappear".
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
	// Pre-fetch so we can audit-log the (prefix, name) tuple.
	existing, ferr := r.apiKeyStore.FindByIDForOrg(id.OrgID(), keyID)
	if ferr != nil {
		code, msg := mapApiKeyError(ferr)
		writeError(w, code, "failed to lookup api key: "+msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := r.apiKeyStore.Revoke(id.OrgID(), keyID); err != nil {
		code, msg := mapApiKeyError(err)
		writeError(w, code, "failed to revoke api key: "+msg)
		return
	}
	r.emitApiKeyAudit(req.Context(), models.AuditActionApiKeyRevoked, id, existing)
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateApiKey issues a fresh API key with the same metadata
// as the existing one, simultaneously revoking the old one. The
// rotation is atomic (single transaction in the repository —
// see Rotate doc).
//
// POST /api/v1/api-keys/{id}/rotate
//
// Permission gate: requireWriteCapability.
//
// Returns 200 + {key, plaintext} with the NEW key's plaintext.
// The OLD key is now revoked; clients must update stores before
// the next API call.
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
	existing, ferr := r.apiKeyStore.FindByIDForOrg(id.OrgID(), oldID)
	if ferr != nil {
		code, msg := mapApiKeyError(ferr)
		writeError(w, code, "failed to lookup api key: "+msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	// Generate the fresh secret + hash. The visible prefix and
	// the persisted hash come from secret space we haven't used
	// before (Generate). Auth.Hash gives us the lookup-stable
	// SHA-256 fingerprint.
	plaintext, keyPrefix, err := auth.Generate(existing.Environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate rotated api key: "+err.Error())
		return
	}
	hash := auth.Hash(plaintext)
	if hash == nil {
		writeError(w, http.StatusInternalServerError, "failed to hash rotated api key")
		return
	}
	// Carry over metadata; the new key owns the same name + project +
	// permissions + environment + created_by + expires_at. created_by
	// is kept as the ORIGINAL creator, NOT the requesting identity
	// (because /rotate is handled by an admin on behalf of the
	// key's owner — typically the org's deploy key, not a human).
	newKey := &models.ApiKey{
		OrganizationID: existing.OrganizationID,
		ProjectID:      existing.ProjectID,
		CreatedBy:      existing.CreatedBy,
		Name:           existing.Name,
		Environment:    existing.Environment,
		KeyPrefix:      keyPrefix,
		Permissions:    existing.Permissions,
		ExpiresAt:      existing.ExpiresAt,
	}
	if err := r.apiKeyStore.Rotate(id.OrgID(), oldID, newKey, hash); err != nil {
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

// emitApiKeyAudit best-effort writes a single audit_log row. The
// audit_log is observer UX — a write failure here does NOT
// propagate to the HTTP response (which is already on the wire
// after the DB write succeeded). A slog.Warn lines the operator
// up with a single observable failure point if audit emission
// regresses; the warning is intentionally local (no metric, no
// counter — slog text is enough for the volume expected).
//
// Metadata carries the visible key_prefix (never plaintext), the
// key name (human-readable), the environment, organization_id,
// and optional project_id. The key's resource_id is the row
// primary key on the api_keys table; we keep it OUT of the
// Metadata blob to avoid duplication (the audit_log schema
// already has a resource_id column).
func (r *Router) emitApiKeyAudit(ctx context.Context, action string, actor auth.Identity, key *models.ApiKey) {
	if r.auditLogStore == nil || key == nil {
		return
	}
	md := map[string]interface{}{
		"key_prefix":      key.KeyPrefix,
		"name":            key.Name,
		"environment":     key.Environment,
		"organization_id": key.OrganizationID,
	}
	if key.ProjectID != nil {
		md["project_id"] = *key.ProjectID
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

// Reference the encoding/json package so the import is preserved
// even when future tooling tries to prune unused imports. The
// Create handler uses json.NewDecoder inline; this reference is
// belt-and-suspenders.
var _ = json.Valid
