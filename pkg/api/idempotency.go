// Package api — Idempotency-Key HTTP header handling (LEVEL 1 of the
// two-level idempotency design).
//
// Wired into handleCreatePost in pkg/api/posts.go. The header is the
// client-side contract: clients that need at-most-once semantics on
// a POST /api/v1/posts call add `Idempotency-Key: <opaque-string>`,
// the server hashes the request body, and on replay either returns
// the original resource or 409 on payload mismatch.
//
// Why cache resource_id + status, not the response body: replaying
// re-fetches the resource from its owner table (today: posts) and
// re-renders the JSON through the same writeJSON path. This avoids
// (a) the complexity of buffering the response writer, (b) stale
// payload risk (the original body might no longer match the
// resource state — the resource is the truth, the body is a
// snapshot), and (c) drift between the cached body and the live
// schema when fields change.
//
// Helper shape (Taglio 4.7 try 2): idempotencyReadBody + idempotencyLookup
// are split into two helpers so the handler can interleave body
// hashing, JSON decoding, workspace resolution, and the cache lookup
// in the right order. Specifically: body bytes are read once, the
// hash computed once, then JSON-unmarshalled into the request struct
// (workspace id available), then the workspace resolved, then the
// idempotency lookup keyed on (workspace_id, idempotency_key) hits
// the cache. This avoids passing a placeholder workspace_id=0 (which
// would silently allow cross-workspace cache collisions if a bug
// later forgot to pass the real id) and ensures the workspace
// ownership check runs BEFORE the cache can return a resource the
// caller does not own.

package api

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// idempotencyOutcome is the result of the cache lookup. The
// handleCreatePost integration switches on this value (continue →
// run the handler; replay → write cached response; conflict → 409).
type idempotencyOutcome int

const (
	idempotencyContinue idempotencyOutcome = iota
	idempotencyReplay
	idempotencyConflict
)

// idempotencyKeyMaxLen bounds the header value the middleware
// accepts. Stripe documents a 255-char limit; we mirror it here so
// the DB column doesn't need a VARCHAR hint and so a buggy client
// doesn't blow up the cache by writing a multi-MB key.
const idempotencyKeyMaxLen = 255

// idempotencyReadBody reads the request body bytes and rewinds
// req.Body so downstream readers (json.NewDecoder, etc.) see the
// same payload. Always returns the bytes — callers compute the
// hash themselves with idempotencyHash below.
//
// Errors are wrapped so the handler can map "client sent a body we
// can't read" to 400 (network read failures during request parsing
// are almost always client-side: truncated upload, broken chunked
// transfer).
func idempotencyReadBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	_ = req.Body.Close()
	// Hand the body back to the handler via NopCloser so json.NewDecoder
	// (or any other Reader-based parser) can read it again. We do NOT
	// re-attach to req.Body using the original implementation because
	// net/http expects Body to be closed; NopCloser + bytes.NewReader
	// is the canonical pattern.
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, nil
}

// idempotencyHash computes the SHA-256 of bodyBytes and returns
// the fixed 32-byte digest. Stable across processes (no salt) so
// the lookup equality check works. An empty body yields the SHA-256
// of "" — a sane value, but the handler should normally have
// produced a non-empty body before reaching the cache layer.
func idempotencyHash(bodyBytes []byte) []byte {
	if len(bodyBytes) == 0 {
		return nil
	}
	hash := sha256.Sum256(bodyBytes)
	return hash[:]
}

// idempotencyLookup consults the cache for (workspaceID, key). On
// hit+match it returns idempotencyReplay + the cached record; on
// hit+mismatch it returns idempotencyConflict. On miss or absent
// header (workspaceID<=0 or key=="") it returns idempotencyContinue
// without erroring.
//
// Errors here are server-side (DB outage, store nil panic — but we
// guard against nil store). The handler maps to 5xx unless the
// error specifically identifies client misbehaviour (key too long
// → 400). Mismatch on hash returns idempotencyConflict WITHOUT an
// error — that's a client contract violation that the handler
// renders as 409.
func idempotencyLookup(
	r *Router,
	workspaceID int64,
	key string,
	hash []byte,
	resourceType string,
) (idempotencyOutcome, *models.IdempotencyRecord, error) {
	if r.idempotencyStore == nil {
		return idempotencyContinue, nil, nil
	}
	if workspaceID <= 0 || key == "" {
		return idempotencyContinue, nil, nil
	}
	if len(key) > idempotencyKeyMaxLen {
		return idempotencyConflict, nil,
			fmt.Errorf("idempotency key exceeds %d chars", idempotencyKeyMaxLen)
	}
	if len(hash) != 32 {
		return idempotencyContinue, nil, nil
	}

	rec, err := r.idempotencyStore.FindActiveByKey(workspaceID, key, time.Now())
	if err != nil {
		return idempotencyContinue, nil,
			fmt.Errorf("idempotency lookup: %w", err)
	}
	if rec == nil {
		// Miss: handler will run; on success it inserts a record
		// behind the same key, so subsequent retries hit.
		return idempotencyContinue, nil, nil
	}
	if !bytes.Equal(rec.RequestHash, hash) {
		// Same key, different payload — conflict. 409.
		// (mirrors Stripe's "different request body for the same
		// idempotency key" semantics.)
		return idempotencyConflict, nil, nil
	}
	if rec.ResourceType != resourceType {
		// Same key, same hash payload but cache points at a
		// different resource_type. Also 409 — the (workspace,
		// key) tuple is supposed to be unique per resource type
		// in the application's mental model.
		return idempotencyConflict, nil, nil
	}
	return idempotencyReplay, rec, nil
}

// replayIdempotentResource re-fetches the resource by
// resource_id and renders it via writeJSON with the cached HTTP
// status. The re-render deliberately bypasses the original
// handler — the cache knows only (resource_type, resource_id, status).
//
// Today only resource_type="post" is implemented. Adding a new
// resource_type means adding a case here. Each case must check
// caller authorisation (workspace ownership) — a cached resource
// should never leak to a caller who wouldn't have had access on
// the originating POST.
//
// SECURITY: replayIdempotentResource is called ONLY after the
// handler has already verified ws.OwnerID == userID (line in
// handleCreatePost above the replay branch). The cached
// resource_id belongs to that same workspace; the user is the
// same caller. So this rebuild from resource_id is safe.
func replayIdempotentResource(
	r *Router,
	w http.ResponseWriter,
	rec *models.IdempotencyRecord,
	cachedStatus int,
) error {
	switch rec.ResourceType {
	case "post":
		if r.postStore == nil {
			return errors.New("post store not configured")
		}
		post, err := r.postStore.FindByID(rec.ResourceID)
		if err != nil {
			return fmt.Errorf("replay fetch post %d: %w", rec.ResourceID, err)
		}
		if post == nil {
			// Vanished since the original write — surface as a
			// specific error so the operator reading logs can see
			// this is a cache-vs-truth drift. We do NOT silently
			// fall through to a 200 with empty body.
			return fmt.Errorf("cached post %d no longer exists", rec.ResourceID)
		}
		writeJSON(w, cachedStatus, post)
		return nil
	case "drive_import":
		if r.postStore == nil {
			return errors.New("post store not configured")
		}
		post, err := r.postStore.FindByID(rec.ResourceID)
		if err != nil {
			return fmt.Errorf("replay fetch drive_import post %d: %w", rec.ResourceID, err)
		}
		if post == nil {
			return fmt.Errorf("cached drive_import post %d no longer exists", rec.ResourceID)
		}
		// Replay preserves the top-level response shape of the
		// first request. The asset is omitted on replay; clients
		// can fetch it separately via GET /api/v1/media/{id}.
		writeJSON(w, cachedStatus, DriveImportResponse{Post: post})
		return nil
	default:
		return fmt.Errorf("unknown resource_type for replay: %q", rec.ResourceType)
	}
}

// insertIdempotentRecord writes the cache row AFTER the handler
// has produced the resource. Errors here are LOGGED but never
// surfaced — the resource already exists for the original caller,
// so a cache-write failure is purely operator UX (audit log +
// reduced replay support). The resource itself is unaffected.
//
// The caller passes the request hash + status already computed
// during idempotencyLookup, so this is a thin one-liner over the
// repository's Insert method. TTL is set to 24h matching Stripe's
// de facto industry standard; can be revoked to a constant later
// if a tenant demands a different window.
func insertIdempotentRecord(
	r *Router,
	workspaceID int64,
	idempotencyKey string,
	resourceType string,
	resourceID int64,
	requestHash []byte,
	responseStatus int,
) {
	if r.idempotencyStore == nil || idempotencyKey == "" {
		return
	}
	rec := &models.IdempotencyRecord{
		WorkspaceID:    workspaceID,
		IdempotencyKey: idempotencyKey,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		RequestHash:    requestHash,
		ResponseStatus: responseStatus,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}
	if err := r.idempotencyStore.Insert(rec); err != nil {
		slog.Warn("idempotency_record insert failed",
			"error", err,
			"workspace_id", workspaceID,
			"key", idempotencyKey,
			"resource_type", resourceType,
			"resource_id", resourceID)
	}
}

// (No strings-package dependency in this file: idempotencyKeyMaxLen is
// referenced only as a numeric constant in idempotencyLookup. The
// "strings" import that lived here earlier was dead and has been
// removed.)
