package api

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

// ExternalDestinationStore is the persistence contract for
// external_destinations. Mirrors the WorkspaceStore / PostStore
// pattern declared in handlers.go: a local interface so tests
// can supply an in-memory fake without dragging the *sql.DB-bound
// *repository.ExternalDestinationRepository into the test fixture.
// The production wiring in cmd/server/main.go passes
// repository.NewExternalDestinationRepository(db) which satisfies
// this contract.
//
// Method scope expanded in Phase 2 to include Create — the
// user-facing endpoint POST /api/v1/integrations/velox/destinations
// (pkg/api/admin_velox_destinations.go) writes new rows; the
// service-to-service Velox validate path reads. Mutators beyond
// Create (Update* / Delete) live on the SAME repository struct
// but are NOT in this interface today; they can be lifted here
// when the operator-side UI grows an "unlink this channel"
// surface.
type ExternalDestinationStore interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDestination, error)
	Create(ctx context.Context, d *models.ExternalDestination) error
}

// ExternalDeliveryStore is the persistence contract for
// external_deliveries. Mirrors the ExternalDestinationStore
// pattern above. Method scope at Phase 1 = Insert + GetByID —
// Insert is the write-path of POST /deliveries; GetByID is the
// read-path of GET /internal/v1/deliveries/{id} (Velox
// reconciliation/poll when the callback channel drops a
// packet).
//
// Reads beyond GetByID (GetByIdempotencyKey,
// GetByExternalDeliveryID, ListByStatus) and other mutators
// (UpdateStatus, LinkUploadJob) live on the SAME
// *repository.ExternalDeliveryRepository struct but are NOT
// in this interface because the API surface today only writes
// (POST /deliveries) AND reads by primary key (GET
// /deliveries/{id}); reconciliation workers poll primary
// keys, not idempotency- or status-keyed indexes. A future
// /admin/velox/deliveries endpoint could expand the interface
// once the operator-side UI needs status filtering.
//
// POST /deliveries uses the three-way Insert outcome (fresh
// insert vs same-SHA reuse vs different-SHA
// ErrIdempotencyConflict) implemented via pg_advisory_xact_lock
// + SELECT + INSERT in the same transaction (see the repo
// doc-comment for the full idempotency semantics). GET
// /deliveries/{id} returns (nil, nil) on miss — the handler
// turns that into 404 with no existence leak; a non-nil error
// from the repo surfaces as 500 (operator-pageable DB
// incident, not a clean 404).
type ExternalDeliveryStore interface {
	Insert(ctx context.Context, e *models.ExternalDelivery, rawBody []byte) (*models.ExternalDelivery, error)
	GetByID(ctx context.Context, id string) (*models.ExternalDelivery, error)
}

// Compile-time assertions the production repository satisfies
// both interfaces. Catches schema drift at go vet time, not at
// runtime. Both interfaces narrow what the handlers can call;
// the repo struct HAS MORE methods but does not expose them
// through these interfaces.
var (
	_ ExternalDestinationStore = (*repository.ExternalDestinationRepository)(nil)
	_ ExternalDeliveryStore    = (*repository.ExternalDeliveryRepository)(nil)
)

// VeloxDeliverArtifactRequest is the request body shape for
// POST /internal/v1/deliveries. Mirrors the user's spec from
// the architectural doc:
//
//	{
//	  "external_delivery_id": "delivery_8cc0f",
//	  "idempotency_key":      "delivery_8cc0f|destination_12",
//	  "external_destination_id": "extdst_01JABC",
//	  "artifact": {
//	    "artifact_id":  "artifact_01JXYZ",
//	    "sha256":       "e5f2c235...",
//	    "size_bytes":   184729302,
//	    "mime_type":    "video/mp4",
//	    "download_url": "https://velox.internal/artifacts/..."
//	  },
//	  "metadata": {
//	    "title":         "Titolo del video",
//	    "description":   "Descrizione",
//	    "tags":          ["tag1", "tag2"],
//	    "privacy_status":"private",
//	    "language":      "it"
//	  },
//	  "publish_at":   "2026-07-20T18:00:00Z",
//	  "callback_url": "https://velox.internal/api/internal/..."
//	}
//
// Field naming aligns byte-for-byte with the upstream's JSON
// (snake_case + dotted-nested `artifact` envelope). The
// optional fields use omitempty + pointer-to-string time so
// the round-trip JSON shape matches what Velox POSTs (a
// missing publish_at serialises back as null in the next
// GET-equivalent, never as 0001-01-01).
type VeloxDeliverArtifactRequest struct {
	ExternalDeliveryID    string           `json:"external_delivery_id"`
	IdempotencyKey        string           `json:"idempotency_key"`
	ExternalDestinationID string           `json:"external_destination_id"`
	Artifact              VeloxArtifactRef `json:"artifact"`
	Metadata              json.RawMessage  `json:"metadata"`
	PublishAt             *time.Time       `json:"publish_at,omitempty"`
	CallbackURL           *string          `json:"callback_url,omitempty"`
}

// VeloxArtifactRef is the nested `artifact` envelope from
// VeloxDeliverArtifactRequest. One-shot artifact triple (id +
// sha + size + mime) plus optional download_url.
type VeloxArtifactRef struct {
	ArtifactID  string  `json:"artifact_id"`
	SHA256      string  `json:"sha256"`
	SizeBytes   int64   `json:"size_bytes"`
	MimeType    string  `json:"mime_type"`
	DownloadURL *string `json:"download_url,omitempty"`
}

// VeloxDeliverArtifactResponse is the 202 body shape for
// POST /internal/v1/deliveries. Returned for both fresh insert
// AND same-SHA replay — the `already_exists` boolean tells the
// caller which path fired without requiring them to inspect
// status_code (always 202) or social_delivery_id (always the
// same canonical row id).
type VeloxDeliverArtifactResponse struct {
	SocialDeliveryID string `json:"social_delivery_id"`
	Status           string `json:"status"` // always "accepted"
	AlreadyExists    bool   `json:"already_exists"`
}

// handleGetInternalDelivery implements
// GET /internal/v1/deliveries/{id} for the Velox integration
// contract. Velox uses this for reconciliation/poll when its
// callback channel drops a packet (network blip, peer restarts,
// webhook 5xx storm). Returns a small JSON shape with the
// delivery's authoritative state at lookup time.
//
// 404 is reserved for "id never accepted" — distinct from "we
// accepted then lost it" semantics. We deliberately collapse
// unknown-id and rejected/cancelled rows into 404 so the
// caller cannot use the response to enumerate row ids.
//
// 401 (Bearer missing) AND 403 (token mismatch) AND 503 (token
// not configured) are emitted by the internalVeloxAuth
// middleware BEFORE this handler runs; the spec is satisfied
// via the middleware's existing behaviour, no per-handler code.
func (r *Router) handleGetInternalDelivery(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "delivery id required")
		return
	}

	delivery, err := r.externalDeliveries.GetByID(req.Context(), id)
	if err != nil {
		slog.Error("velox get delivery: lookup failed",
			"social_delivery_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "delivery lookup failed")
		return
	}
	if delivery == nil {
		writeError(w, http.StatusNotFound, "delivery not found")
		return
	}

	resp := VeloxGetDeliveryResponse{
		ID:        delivery.ID,
		Status:    string(delivery.Status),
		CreatedAt: delivery.CreatedAt,
		UpdatedAt: delivery.UpdatedAt,
	}
	// Surface LastErrorCode + Message verbatim. omitempty drops
	// the field on rows that haven't seen a failed transition
	// yet (the brand-new accepted row).
	if delivery.LastErrorCode != nil {
		resp.LastErrorCode = *delivery.LastErrorCode
		// retry_wait_reason mirrors last_error_code ONLY when
		// status == retry_wait — the operator's "why is this
		// sitting in retry?" question is answered by this field.
		// In any other state the field is empty.
		if delivery.Status == models.ExternalDeliveryStatusRetryWait {
			resp.RetryWaitReason = *delivery.LastErrorCode
		}
	}
	if delivery.LastErrorMessage != nil {
		resp.LastErrorMessage = *delivery.LastErrorMessage
	}
	if delivery.PlatformMediaID != nil {
		resp.PlatformMediaID = *delivery.PlatformMediaID
	}
	if delivery.PlatformURL != nil {
		resp.PlatformURL = *delivery.PlatformURL
	}
	// published_at is ONLY set when the row reached the published
	// terminal state. For other terminal states (failed,
	// dead_letter) the user spec explicitly maps "published_at"
	// to the success path.
	if delivery.Status == models.ExternalDeliveryStatusPublished &&
		delivery.CompletedAt != nil {
		resp.PublishedAt = delivery.CompletedAt
	}

	slog.Info("velox get delivery",
		"social_delivery_id", delivery.ID,
		"status", delivery.Status,
	)
	writeJSON(w, http.StatusOK, resp)
}

// VeloxDeliverArtifactConflictResponse is the 409 body shape
// when a same-idempotency_key-different-SHA replay arrives.
// Distinct shape from the standard writeError envelope so callers
// can pattern-match the field names reliably. 422 paths use the
// standard {error: "validation: ..."} envelope so operators
// see a uniform shape; ONLY the 409 uses this structured body.
type VeloxDeliverArtifactConflictResponse struct {
	Error          string `json:"error"`           // "idempotency_key_conflict"
	Code           string `json:"code"`            // "idempotency_key_conflict"
	IdempotencyKey string `json:"idempotency_key"` // the conflicting key
}

// SHA-256 lowercase-hex regex. Compiled once at package init.
// 64 chars, [0-9a-f]{64}. Uppercase hex is rejected (callers
// must lowercase before submitting to match the canonical
// representation produced by sha256.Sum256().hex.EncodeToString()).

// veloxDestinationNotFoundBody is the single source of truth for
// the 404 response body when a destination is missing. Both
// handleCreateInternalDelivery (POST /deliveries) and
// handleValidateInternalDestination (POST /destinations/{id}/validate)
// use this exact text so a probe hitting either path gets an
// indistinguishable body — closes the cross-handler body-shape
// asymmetry that the code-reviewer flagged as a residual
// status-code-oracle surface (status code is uniform but body
// was not, allowing an attacker to distinguish routes by the
// presence/absence of the ID-quoted prefix).
//
// Deliberately bare (no destination_id quoted in the body):
// the request URL already carries the id, so writing it into
// the body widens the oracle surface for no diagnostic gain.
const veloxDestinationNotFoundBody = "destination not found"

var sha256HexRegex = regexp.MustCompile(`^[a-f0-9]{64}$`)

// MIME allowlist. Phase 1 video-only. The worker downstream
// uses this same allowlist (or shares a constant) to short-
// circuit unsupported ingest; matching here + there prevents
// a delivery row's expected_mime_type from disagreeing with
// the actual streamed bytes' detected mime_type.
//
//	video/mp4        — MP4 container, canonical for YouTube ingest
//	video/quicktime  — MOV container (Apple ecosystem export)
//	video/webm       — WebM (low-bitrate alternative)
//	video/x-matroska — MKV (less common but spec'd)
var mimeAllowlist = map[string]bool{
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-matroska": true,
}

// --- /internal/v1/destinations/{id}/validate rate limiter ----------
//
// validateRateLimiter is a per-destination-id sliding-window
// rate limiter. The Velox peer requests validate before every
// delivery; a worker stuck in a hot-loop (e.g. exponential-retry
// without backoff, mis-wired destination id) could fire hundreds of
// validates per second against a single destination. The limiter
// rejects with 429 + Retry-After so the peer can spread its retry
// load without saturating the InstaEdit DB.
//
// Sliding window: an entry per destination_id is allocated the
// first time take(key) is called. Each subsequent call within the
// window increments the slot's count; when count exceeds limit,
// take returns (allowed=false, retryAfter=time-until-slot.resetAt).
// After resetAt elapses, the next take re-allocates the slot.
//
// Concurrency: sync.Mutex serialises the read+increment so a
// single destination_id's counter is atomic. Across distinct
// destination_ids, contention is bounded by the mutex held
// briefly during take(); the operation is O(1) and the hot-path
// (limit-check without DB) does not block on database I/O.
//
// nil *validateRateLimiter is the documented "no limit"
// sentinel — handlers check `if r.veloxValidateRateLimiter != nil`
// before consulting it, so a process that never wired
// WithVeloxValidateRateLimit behaves as before.
type validateRateLimiter struct {
	mu     sync.Mutex
	slots  map[string]*validateSlot
	limit  int
	window time.Duration
}

// validateSlot tracks one destination's request count within a
// sliding window. resetAt is the moment the count resets to zero
// and the slot becomes eligible for `limit` fresh requests.
type validateSlot struct {
	count   int
	resetAt time.Time
}

// take increments the counter for key and returns whether the
// request is allowed. retryAfter is the time until the current
// window resets; nonzero ONLY when allowed=false (Retry-After
// header value). On a nil receiver this is a no-op pass-through
// (mirrors the documented "no limit" sentinel).
func (l *validateRateLimiter) take(key string) (allowed bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	slot, ok := l.slots[key]
	if !ok || now.After(slot.resetAt) {
		l.slots[key] = &validateSlot{count: 1, resetAt: now.Add(l.window)}
		return true, 0
	}
	if slot.count >= l.limit {
		return false, slot.resetAt.Sub(now)
	}
	slot.count++
	return true, 0
}

// defaultValidateRateLimit is the production-bound default
// (60 validations per minute per destination_id). Operators can
// override via WithVeloxValidateRateLimit(...).
const defaultValidateRateLimit = 60

// defaultValidateRateWindow is the sliding-window duration for
// the production default. Aligned with minute-bucket semantics
// so dashboards that group by minute report stable counts.
const defaultValidateRateWindow = 1 * time.Minute

// WithVeloxValidateRateLimit wires a custom per-destination-id
// rate limit on the validate endpoint. Passing 0 disables the
// limiter (handler becomes a no-op take). Most production
// deployments can skip this option and rely on the default
// constants above; tests USE this option to set a low limit
// (e.g. 2) so the 429 path is reachable in O(few) requests.
func WithVeloxValidateRateLimit(limit int, window time.Duration) RouterOption {
	if limit <= 0 || window <= 0 {
		// Zero / negative → disable the limiter. Saves a
		// heap allocation in main.go for the "no limit"
		// deployment shape.
		return func(r *Router) { r.veloxValidateRateLimiter = nil }
	}
	rl := &validateRateLimiter{
		slots:  make(map[string]*validateSlot),
		limit:  limit,
		window: window,
	}
	return func(r *Router) { r.veloxValidateRateLimiter = rl }
}

// maxDeliveryBodyBytes is the hard cap on POST /deliveries body
// size. Phase 1 metadata-only deliveries (the artifact is
// referenced by download_url, not uploaded inline) cap the
// JSON envelope at 8 MB — vast margin for text-keyed publish
// payloads (titles, descriptions, tag arrays) without risking
// OOM on a flooded Velox producer. The artifact itself
// streams via the separate download_url call downstream.
const maxDeliveryBodyBytes = 8 * 1024 * 1024

// veloxSourceSystemTag is the source_system column value for
// all Phase 1 Velox handoffs. Hardcoded today; future migration
// to per-router config (when Dropbox joins the same code path)
// will lift this into a WithVeloxSourceSystem option.
const veloxSourceSystemTag = "velox"

// generateVeloxDestinationID mints a unique opaque ULID-shaped id
// for external_destinations.id. Strategy mirrors
// generateVeloxDeliveryID with a different prefix: 7-char prefix
// ("extdst_") + 3-char ULID legacy timestamp segment ("01J") +
// 16 bytes (128 bits) of crypto-rand encoded as 26-char base32
// (StdEncoding without padding). Total = 36 chars.
//
// Used by the user-facing POST
// /api/v1/integrations/velox/destinations (Phase 2). The
// repository's Create method stores the byte payload verbatim;
// callers consume the opaque id as a stable reference (the
// Velox service-to-surface references the same id in
// /internal/v1/destinations/{id}/validate + /internal/v1/deliveries).
//
// Returns (id, error). Errors only occur on crypto/rand.Read
// failures (extremely rare; usually means the OS entropy source
// is broken — fatal at boot, but defensive here so the handler
// returns 500 instead of panicking).
func generateVeloxDestinationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("velox destination id mint: rand.Read: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "extdst_01J" + strings.ToLower(encoded), nil
}

// generateVeloxDeliveryID mints a unique opaque ULID-shaped id
// for the social_delivery_id column. Strategy: 5-char prefix
// ("sdel_") + 3-char ULID legacy timestamp segment ("01J")
// + 16 bytes (128 bits) of crypto-rand encoded as 26-char
// base32 (StdEncoding without padding). Total = 34 chars.
//
// NOT a true ULID — the "01J" segment is a fixed marker in
// this implementation (no time-decoding). The collision
// surface is 2^128, more than enough for any realistic
// volume. Phase 1 keeps the prefix opaque; a future
// migration to true ULID decoding (for time-sortable social
// delivery ids) is dropped in transparently because the
// prefix "sdel_" stays the same.
//
// Returns (id, error). Errors only occur on
// crypto/rand.Read failures (extremely rare; usually means
// the OS entropy source is broken — fatal at boot, but
// defensive here so the handler returns 500 instead of
// panicking).
func generateVeloxDeliveryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("velox delivery id mint: rand.Read: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "sdel_01J" + strings.ToLower(encoded), nil
}

// isNonEmptyJSONObject returns true when raw is a non-empty
// JSON object (lenient — accepts trailing/leading whitespace).
// Rejects empty objects ("{}"), arrays ("[1,2]"), null, and
// non-object types. The handler uses this to enforce the
// "metadata must be a non-empty JSON object" rule so the
// downstream publish_worker has at least one field to extract
// (a literally-empty metadata envelope carries no useful info
// for the publish pipeline).
func isNonEmptyJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	// Strip leading/trailing whitespace.
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return false
	}
	return len(m) > 0
}

// handleCreateInternalDelivery implements
// POST /internal/v1/deliveries for the Velox integration
// contract.
//
// IDEMPOTENCY CONTRACT (per user spec):
//  1. Compute request_sha256 = sha256(raw_body_bytes) inside
//     external_delivery_repo.Insert (the Insert computes the
//     hex from rawBody internally).
//  2. INSERT path look-and-write happens under a single
//     pg_advisory_xact_lock so concurrent replays serialise.
//  3. SAME SHA → reuse social_delivery_id, return 202 with
//     already_exists=true.
//  4. DIFFERENT SHA → 409 with structured
//     VeloxDeliverArtifactConflictResponse body
//     (error/code/idempotency_key).
//  5. NOT FOUND → INSERT new row, return 202 with
//     already_exists=false.
//
// The three-way outcome is detected in the handler by comparing
// the returned record's ID to the minted ID: equal → freshly
// inserted by THIS request; different → reused from a previous
// row (same idempotency_key + same SHA pre-existed).
//
// SLA — 500ms p99 target (per user spec). The Insert with
// pg_advisory_xact_lock + SELECT + maybe INSERT is bounded
// by the lock-holder's transaction speed (typically 50-150ms
// on healthy Postgres). We add a 5s ctx timeout as a safety
// cap; an Insert > 300ms is logged WARN so operators can
// alert on slow path without paging.
//
// VALIDATION CHAIN (fast-fail-first, ordered by error cost):
//  1. Authorization (Bearer middleware, 401/403/503)
//  2. Body cap (8 MB → 413 if over)
//  3. JSON parsing (400 if malformed)
//  4. idempotency_key presence + length ≤ 256 (422)
//  5. artifact.sha256 regex (422)
//  6. artifact.size_bytes > 0 (422)
//  7. artifact.mime_type allowlist (422)
//  8. metadata non-empty JSON object (422)
//  9. external_destination_id present in DB (422)
//  10. Insert call (3-way outcome)
//
// All 422 paths funnel through writeError so callers see the
// uniform {"error": "validation: ..."} envelope. The 409
// conflict is the ONLY structured-body response in this
// handler — by design, so callers can distinguish
// "validate-and-fix" (422) from "permanent conflict, don't
// retry" (409).
func (r *Router) handleCreateInternalDelivery(w http.ResponseWriter, req *http.Request) {
	if r.externalDeliveries == nil {
		writeError(w, http.StatusNotImplemented, "internal velox delivery store not configured")
		return
	}
	if r.externalDestinations == nil {
		writeError(w, http.StatusInternalServerError, "external destination store not configured")
		return
	}

	// Defensive ctx cap. 5s is well above the 500ms p99 SLA — the
	// Insert is bounded by transactional speed. The cap means a
	// runaway DB never blocks the handler indefinitely.
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	// Step 2 — body cap via http.MaxBytesReader. The Reader
	// returns *http.MaxBytesError on truncation, which we
	// detect with errors.As (not string match).
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxDeliveryBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			slog.Warn("velox deliver: body too large",
				"limit_bytes", maxDeliveryBodyBytes)
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d MB", maxDeliveryBodyBytes/(1024*1024)))
			return
		}
		slog.Error("velox deliver: body read failed", "err", err)
		writeError(w, http.StatusInternalServerError, "body read failed")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	// Step 3 — JSON parse. Decode on a COPY of the raw bytes
	// because the Insert needs the raw bytes for SHA
	// computation; the unmarshal call below does NOT consume
	// the body iterator (it's a fresh Unmarshal pass).
	var veloxReq VeloxDeliverArtifactRequest
	if err := json.Unmarshal(body, &veloxReq); err != nil {
		slog.Warn("velox deliver: json unmarshal failed", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Step 4 — idempotency_key presence + max length.
	if veloxReq.IdempotencyKey == "" {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key is required")
		return
	}
	if len(veloxReq.IdempotencyKey) > 256 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key exceeds 256 characters")
		return
	}

	// Step 5 — artifact.sha256 lowercase hex regex.
	if !sha256HexRegex.MatchString(veloxReq.Artifact.SHA256) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.sha256 must be 64 lowercase hex characters")
		return
	}

	// Step 6 — artifact.size_bytes positive.
	if veloxReq.Artifact.SizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.size_bytes must be > 0")
		return
	}

	// Step 7 — mime allowlist (4 video formats).
	if !mimeAllowlist[veloxReq.Artifact.MimeType] {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("validation: artifact.mime_type %q not supported (allowed: video/mp4, video/quicktime, video/webm, video/x-matroska)",
				veloxReq.Artifact.MimeType))
		return
	}

	// Step 8 — metadata non-empty JSON object.
	if !isNonEmptyJSONObject(veloxReq.Metadata) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: metadata must be a non-empty JSON object")
		return
	}

	// Step 9 — external_destination_id must exist.
	dest, err := r.externalDestinations.GetByID(ctx, veloxReq.ExternalDestinationID)
	if err != nil {
		// Sentinel-aware: a not-found sentinel from the
		// destination repo is the SAME 404 a missing-row
		// produces, so the caller can't distinguish them
		// (closes a status-code-oracle / existence-leak path:
		// without this branch, the 500/404 split on missing
		// destinations would let a malicious Velox peer iterate
		// IDs and enumerate which are live).
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox deliver: destination lookup failed",
			"external_destination_id", veloxReq.ExternalDestinationID,
			"err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		// Spec mandate: a missing destination row is NOT a
		// payload validation failure (the body IS well-formed) —
		// return 404 because the resource the request refers to
		// doesn't exist. This matches the user spec
		// "ErrExternalDeliveryNotFound → 404 (se destination_id
		// manca)" + collapses with the /validate 404 "destination
		// not found" so the Velox consumer treats both
		// identically and a missing row never leaks a 422/500
		// distinction.
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}

	// Step 9b — workspace lookup. Produces the WorkspaceID +
	// OwnerUserID the producer-side carryover binds onto the
	// worker.VeloxDownloadJob channel item. Uses the SAME
	// workspaceStore the /validate handler relies on (mirroring
	// that handler's Step 11 wiring) so a missing-or-renamed
	// workspace row produces the same map at both entry points.
	//
	// Failure modes (VELOX CONTRACT: 5xx == transient == retry):
	//   - r.workspaceStore == nil: WARN-log + use dest-derived
	//     workspace ID + UserID=0 placeholder. Production
	//     always wires this store, so the fallback path
	//     fires only in tests / lightweight configs.
	//   - transient lookup failure: 503 Service Unavailable.
	//   - workspace row genuinely missing: 503 (rare; treat
	//     as operator-fixable misconfiguration).
	//
	// Cost: single indexed PK lookup, ~5ms typical. Insert
	// dominates the request budget; this adds ~10% to p99.
	var ws *models.Workspace
	if r.workspaceStore != nil {
		looked, wsErr := r.workspaceStore.FindByID(dest.WorkspaceID)
		if wsErr != nil || looked == nil {
			slog.Warn("velox deliver: workspace lookup transient failure",
				"external_destination_id", veloxReq.ExternalDestinationID,
				"workspace_id", dest.WorkspaceID, "err", wsErr)
			writeError(w, http.StatusServiceUnavailable, "workspace lookup transient; retry")
			return
		}
		ws = looked
	} else {
		slog.Warn("velox deliver: workspaceStore not configured; using dest.WorkspaceID placeholder",
			"external_destination_id", veloxReq.ExternalDestinationID,
			"workspace_id", dest.WorkspaceID)
		// Fallback so the downstream enqueue can still derive
		// WorkspaceID from the destination row. OwnerID is unknown
		// when the store is unwired; zero is the only safe value.
		ws = &models.Workspace{
			ID:      dest.WorkspaceID,
			OwnerID: 0,
		}
	}

	// Step 10 — mint social_delivery_id (ULID-shaped opaque).
	mintedID, err := generateVeloxDeliveryID()
	if err != nil {
		slog.Error("velox deliver: id mint failed", "err", err)
		writeError(w, http.StatusInternalServerError, "id mint failed")
		return
	}

	// Build the ExternalDelivery record. The Insert computes
	// RequestSHA256 from rawBody internally (no double-read).
	delivery := &models.ExternalDelivery{
		ID:                    mintedID,
		SourceSystem:          veloxSourceSystemTag,
		ExternalDeliveryID:    veloxReq.ExternalDeliveryID,
		IdempotencyKey:        veloxReq.IdempotencyKey,
		ExternalDestinationID: veloxReq.ExternalDestinationID,
		SourceArtifactID:      veloxReq.Artifact.ArtifactID,
		ExpectedSHA256:        veloxReq.Artifact.SHA256,
		ExpectedSizeBytes:     veloxReq.Artifact.SizeBytes,
		ExpectedMimeType:      veloxReq.Artifact.MimeType,
		DownloadURL:           veloxReq.Artifact.DownloadURL,
		Metadata:              veloxReq.Metadata,
		PublishAt:             veloxReq.PublishAt,
		CallbackURL:           veloxReq.CallbackURL,
		Status:                models.ExternalDeliveryStatusAccepted,
	}

	// Insert with rawBody so repo computes SHA from the EXACT
	// bytes (no serialization round-trip mismatch possible).
	t0 := time.Now()
	inserted, err := r.externalDeliveries.Insert(ctx, delivery, body)
	elapsed := time.Since(t0)
	if elapsed > 300*time.Millisecond {
		slog.Warn("velox deliver: insert slow",
			"elapsed_ms", elapsed.Milliseconds(),
			"idempotency_key", veloxReq.IdempotencyKey)
	}

	if err != nil {
		// 3-way outcome: ErrIdempotencyConflict → 409 structured.
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			var existingID string
			if inserted != nil {
				existingID = inserted.ID
			}
			slog.Info("velox deliver: replay with different sha rejected",
				"idempotency_key", veloxReq.IdempotencyKey,
				"existing_social_delivery_id", existingID,
			)
			writeJSON(w, http.StatusConflict, VeloxDeliverArtifactConflictResponse{
				Error:          "idempotency_key_conflict",
				Code:           "idempotency_key_conflict",
				IdempotencyKey: veloxReq.IdempotencyKey,
			})
			return
		}
		slog.Error("velox deliver: insert failed",
			"err", err, "idempotency_key", veloxReq.IdempotencyKey)
		writeError(w, http.StatusInternalServerError, "delivery persist failed")
		return
	}

	// 3-way outcome — fresh (mintedID == inserted.ID) vs
	// replay (mintedID != inserted.ID). ALWAYS 202.
	alreadyExists := inserted.ID != mintedID
	slog.Info("velox deliver: accepted",
		"social_delivery_id", inserted.ID,
		"idempotency_key", veloxReq.IdempotencyKey,
		"already_exists", alreadyExists,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	// Async download-job enqueue. Non-blocking + nil-safe so the
	// handler MUST NOT stall on a backed-up worker pool AND test
	// fake routers (which never wire the channel) don't crash. On
	// overflow we LOG warn + drop the enqueue: the row is already
	// accepted in the DB, and a periodic reaper that scans rows in
	// status='accepted' with download NOT_STARTED older than
	// downloadStuckAfter picks up abandoned deliveries.
	//
	// DownloadURL is flattened *string → string via the nil-check
	// to avoid a deref panic on metadata-only deliveries where
	// artifact.download_url is omitempty in the wire contract.
	//
	// Producer-side carryovers (UserID, WorkspaceID, Title, Caption,
	// DefaultPrivacyLevel, PublishAt) are bound from the authed
	// request scope. The downloader reads the upload_job.targets via
	// a downstream publish-time query against external_delivery
	// metadata, so target_account_ids is NOT forwarded through the
	// channel; the publish side resolves targets from the JSONB
	// metadata column on the external_deliveries row.
	if r.downloadJobCh != nil {
		var downloadURL string
		if veloxReq.Artifact.DownloadURL != nil {
			downloadURL = *veloxReq.Artifact.DownloadURL
		}
		job := worker.VeloxDownloadJob{
			ExternalDeliveryID:  inserted.ID,
			UserID:              ws.OwnerID,
			WorkspaceID:         ws.ID,
			Title:               extractVeloxMetaString(veloxReq.Metadata, "title"),
			Caption:             extractVeloxMetaString(veloxReq.Metadata, "description"),
			DefaultPrivacyLevel: extractVeloxMetaString(veloxReq.Metadata, "privacy_status"),
			ArtifactSHA256:      veloxReq.Artifact.SHA256,
			SizeBytes:           veloxReq.Artifact.SizeBytes,
			MimeType:            veloxReq.Artifact.MimeType,
			// SourceID is bound in the consumer (NOT here) because the
			// consumer must read the CANONICAL row from external_delivery
			// (step 1 GetByID). The channel-side downloadURL is best-
			// effort and may diverge under peer race.
			DownloadURL: downloadURL,
			PublishAt:   veloxReq.PublishAt,
		}
		select {
		case r.downloadJobCh <- job:
			// Queued. Worker drains.
		default:
			slog.Warn("velox deliver: download job queue full; reaper will pick up",
				"social_delivery_id", inserted.ID)
		}
	}

	writeJSON(w, http.StatusAccepted, VeloxDeliverArtifactResponse{
		SocialDeliveryID: inserted.ID,
		Status:           "accepted",
		AlreadyExists:    alreadyExists,
	})
}

// extractVeloxMetaString best-effort-parses the JSONB metadata blob
// the producer carries from Velox. Returns "" when the blob is empty,
// unparsable, or the requested key is missing / non-string. Used
// to forward title / description / privacy_status to the downloader's
// worker.VeloxDownloadJob payload (the publish cascade downstream
// reads those values off the upload_job row).
//
// Best-effort is intentional: the producer accepts arbitrary
// metadata and only validates the SHA256/size/mime externally-
// controlled triple. An unparsable metadata blob would surface
// at the YouTube publish call as "missing title" — operator-
// diagnosable via the dashboard without breaking the 500ms SLA.
func extractVeloxMetaString(metadata json.RawMessage, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(metadata, &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// handleValidateInternalDestination implements
// POST /internal/v1/destinations/{id}/validate for the Velox
// integration contract.
//
// RATIONALE — five server-side checks:
//
//  1. Destination row exists.
//  2. Destination row enabled = TRUE.
//  3. Workspace row exists (workspaces has no archived_at column;
//     "attivo" maps to "row present"; FindByID non-nil == active).
//  4. Platform_account exists.
//  5. Platform_account NOT in reauth_required — both signals
//     (status enum + reauth_required_at timestamp) checked
//     defense-in-depth.
//
// All dependent stores (workspaceStore + userRepo) are read
// from Router fields DIRECTLY (not via a captured config
// struct). This avoids an option-order trap: a RouterOption
// that snapshots r.workspaceStore at option-call time would
// capture nil if the option order is wrong. The Router fields
// are always current at handler-time.
//
// Inconsistency note: a reauth_required destination returns 404
// (not 422) because the canonical Velox contract treats
// non-usable destinations as if they don't exist — the peer's
// only sane response is to drop the destination and reissue
// the URL with a fresh id. Returning a distinct status would
// leak existence.
//
// TOKEN REFRESHABILITY — see the file-level doc-comment at the
// registerInternalVeloxRoutes helper for the full rationale:
// /validate is a fast poll that DOES NOT touch the credential
// vault. Trust chain:
//   - platform_account.status = 'active'
//   - platform_account.reauth_required_at IS NULL
//
// A stale active-but-revoked-by-provider grant surfaces at
// publish time (publish_worker decrypts, refreshes, gets a 4xx,
// propagates to external_deliveries.status='blocked_auth').
// Phase-1 trust this near-miss rate; a future Taglio can add
// oauth_connections.last_validated_at as a freshness probe.
//
// RESPONSE — Velox consumes only the HTTP status code per
// spec; diagnostic JSON is OPT-IN via:
//
//   - ?diagnostic=true query parameter
//   - X-Velox-Diagnostic: true request header
//
// Both must be explicit "true" so a peer misconfiguration
// doesn't accidentally trigger the body variant (Velox's
// request layer forwards all headers by default; the explicit
// true gate avoids accidental triggering).
func (r *Router) handleValidateInternalDestination(w http.ResponseWriter, req *http.Request) {
	if r.externalDestinations == nil {
		writeError(w, http.StatusNotImplemented, "internal velox store not configured")
		return
	}
	id := req.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "destination id required")
		return
	}

	// 0. Per-destination rate limit. Runs BEFORE any DB lookup
	// so a Velox hot-loop on a single id is rejected cheaply
	// without saturating the destination / workspace /
	// platform_account downstreams. 429 + Retry-After header
	// signals the peer to spread its retry load.
	if r.veloxValidateRateLimiter != nil {
		allowed, retryAfter := r.veloxValidateRateLimiter.take(id)
		if !allowed {
			seconds := int(retryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			slog.Warn("velox validate: rate limit exceeded",
				"destination_id", id, "retry_after_seconds", seconds)
			writeError(w, http.StatusTooManyRequests,
				fmt.Sprintf("rate limit exceeded; retry after %d seconds", seconds))
			return
		}
	}

	// 1. Destination lookup.
	dest, err := r.externalDestinations.GetByID(req.Context(), id)
	if err != nil {
		// Mirror of handleCreateInternalDelivery's sentinel-aware
		// 404: production repos wrap the missing-row case as
		// (nil, ErrExternalDestinationNotFound); the validate-side
		// mock returns (nil, nil) for missing rows, so the L862
		// nil-dest branch covers tests. Real production code
		// hits this branch on missing rows and we MUST map it
		// to 404 (not 500) to keep the validate path consistent
		// with the POST path — a 500 here would let a probe
		// iterate IDs and enumerate which are live.
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox validate: destination lookup failed",
			"id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil || !dest.Enabled {
		// Disabled = 404 (uniform with not-found; doesn't leak
		// existence).
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}

	// 2. Workspace lookup. Read directly from Router field —
	// avoids the option-order trap of capturing values at
	// WithExternalDestinationStore call time.
	if r.workspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	ws, err := r.workspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		slog.Error("velox validate: workspace lookup failed",
			"workspace_id", dest.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// 3. Platform_account lookup. Same direct-from-Router pattern.
	if r.userRepo == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	pa, err := r.userRepo.FindPlatformAccountByID(dest.PlatformAccountID)
	if err != nil {
		slog.Error("velox validate: platform_account lookup failed",
			"platform_account_id", dest.PlatformAccountID, "err", err)
		writeError(w, http.StatusInternalServerError, "platform_account lookup failed")
		return
	}
	if pa == nil {
		writeError(w, http.StatusNotFound, "platform_account not found")
		return
	}
	// Both reauth signals must be checked (migration 005
	// added reauth_required_at; status enum is the canonical
	// signal). They are redundant by design — checking both
	// ensures a partial migration that updates one without
	// the other still surfaces here.
	if pa.Status == "reauth_required" || pa.ReauthRequiredAt != nil {
		slog.Warn("velox validate: destination has reauth_required channel",
			"destination_id", id, "platform_account_id", pa.ID)
		writeError(w, http.StatusNotFound, "destination requires reauth")
		return
	}

	// P1 deletion check: refuse explicitly-cancelled accounts
	// (status=AccountStatusRevoked OR AccountStatusDisconnected).
	// These mean the user took an explicit action to terminate
	// the OAuth grant, so keeping the destination
	// enabled-but-unusable would surface as a publish-time
	// blocked_auth. Returning 404 here gives Velox the same
	// "destination not found" signal as a removed row so the
	// worker reissues with a fresh id (matches the
	// reauth_required collapse semantics documented at the
	// file header).
	//
	// The check uses the typed AccountStatus* constants from
	// internal/models/user.go — they ARE the canonical string
	// aliases ("revoked", "disconnected"); checking the model
	// constants instead of bare literals removes the
	// maintenance trap of a literal drifting from the canonical
	// value during a future status-rename migration.
	if pa.Status == models.AccountStatusRevoked ||
		pa.Status == models.AccountStatusDisconnected {
		slog.Warn("velox validate: destination has cancelled channel",
			"destination_id", id, "platform_account_id", pa.ID,
			"status", pa.Status)
		writeError(w, http.StatusNotFound, "destination cancelled")
		return
	}

	// 4. Diagnostic JSON trigger (explicit operator opt-in only).
	diagnostic := req.URL.Query().Get("diagnostic") == "true" ||
		req.Header.Get("X-Velox-Diagnostic") == "true"

	if diagnostic {
		writeJSON(w, http.StatusOK, VeloxValidateDestinationResponse{
			Valid:         true,
			DestinationID: dest.ID,
			Status:        "active",
			Platform:      pa.Platform,
		})
		return
	}

	// 5. Happy path: 204 No Content. Velox consumes only the
	// status code per spec.
	w.WriteHeader(http.StatusNoContent)
}

// VeloxValidateDestinationResponse is the diagnostic-mode body
// returned when ?diagnostic=true OR X-Velox-Diagnostic: true is
// set on the request. Stable shape — operators monitoring
// pattern-match on the values. Mirrors the user's spec tuple
// `{valid, destination_id, status, platform}` verbatim.
type VeloxValidateDestinationResponse struct {
	Valid         bool   `json:"valid"`
	DestinationID string `json:"destination_id"`
	Status        string `json:"status"`
	Platform      string `json:"platform"`
} // VeloxGetDeliveryResponse is the body returned by
// GET /internal/v1/deliveries/{id}. Mirrors the user's spec
// verbatim:
//
//	{
//	  "id":                 "sdel_01JABCDEX..."  // mirrors the URL path id (canonical social_delivery_id)
//	  "status":             "queued"|"published"|"failed"|"dead_letter"|...
//	  "retry_wait_reason":  "auth_token_expired"        // populated only when status == retry_wait
//	  "last_error_code":    "auth_error"                // typed code from classifyUploadError
//	  "last_error_message": "401 Unauthorized: invalid_grant"
//	  "platform_media_id":  "dQw4w9WgXcQ"               // e.g. YouTube video id, set on terminal publish
//	  "platform_url":       "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
//	  "published_at":       "2026-07-20T18:03:21Z"      // set ONLY when status == published
//	  "created_at":         "2026-07-20T17:59:42Z"      // row insert time (always set on a real row)
//	  "updated_at":         "2026-07-20T18:03:21Z"      // last UpdateStatus time (always set on a real row)
//	}
//
// Field taxonomy:
//   - id                  : mirrors the URL path id (the canonical sdel_01J...
//     social_delivery_id). Surrendering the same id on
//     the response body lets Velox correlate the round
//     trip without remembering which path it queried —
//     useful for client-side cache-key generation and
//     log aggregation where the response body is the
//     canonical record (NOT the URL).
//   - status              : mirrors models.ExternalDeliveryStatus string rep
//   - retry_wait_reason   : derived from last_error_code when status == retry_wait;
//     empty string in all other states (operators reading
//     the GET body know to ignore the field unless status is
//     retry_wait)
//   - last_error_code     : whatever UpdateStatus most recently stamped; empty
//     before any error transition
//   - last_error_message  : human-readable counterpart to last_error_code
//   - platform_media_id   : populated from platform_provider after publish
//   - platform_url        : same
//   - published_at        : populated from completed_at IF status == published;
//     empty for any other state (failed/deleted/completed-but-
//     not-published are not "published_at")
//   - created_at          : row's INSERT time (set by repo: NOW() at insert).
//     Always present on a real row because the migration
//     timestamp column is NOT NULL.
//   - updated_at          : row's last UpdateStatus stamp (set by repo:
//     NOW() on every UpdateStatus). Always present on a
//     real row for the same reason; diverges from created_at
//     after the worker's first transition.
//
// The id + created_at + updated_at trio pin the response to the
// row's audit reality (when did this row arrive, when was it last
// touched). Velox's reconciliation/poll endpoint needs these so it
// can SKIP a payload whose (id, status) tuple matches an earlier
// poll without re-fetching auxiliary tables — the timestamps let
// the peer implement a "ignore stale updates older than X" filter
// in O(1) without an extra database round trip.
//
// The omitempty tags keep the JSON shape minimal: a brand-new row
// returns {"id":"...","status":"accepted","created_at":"...","created_at":"..."}.
// The shape is forward-compat: future fields (e.g.,
// "scheduled_publish_at" from PublishAt) slot in without breaking
// existing consumers.
type VeloxGetDeliveryResponse struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	RetryWaitReason  string     `json:"retry_wait_reason,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorMessage string     `json:"last_error_message,omitempty"`
	PlatformMediaID  string     `json:"platform_media_id,omitempty"`
	PlatformURL      string     `json:"platform_url,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// registerInternalVeloxRoutes wires the /internal/v1
// service-to-service routes. Called from Router.Setup().
// Refuses to register if the per-route dependencies aren't
// wired (matches the WorkspaceStore / PostStore nil-guard
// pattern) — a server without WithExternalDestinationStore +
// WithVeloxAPIToken returns 404 for /internal/v1/* paths so
// the operator sees a clear "route not registered" rather
// than a 500.
//
// Per-route dependency requirements:
//   - destinations/{id}/validate: externalDestinations +
//     veloxAPIToken (workspaceStore + userRepo required at
//     handler-time for the full happy path; checked inline).
//   - deliveries: externalDestinations + externalDeliveries
//   - veloxAPIToken (all three required AT register-time;
//     the handler's defensive nil-checks also catch a
//     misordered wiring).
//
// Boot-time guard rationale: if VELOX_API_TOKEN is empty OR
// the destination store IS unwired, the middleware returns
// 503 on every request. Better to NOT register the route at
// all so the operator sees a 404 in the logs and traces back
// the env config. Subsequent env rotation (process restart
// re-loads) restores the route.
func (r *Router) registerInternalVeloxRoutes() {
	if r.externalDestinations == nil || r.veloxAPIToken == "" {
		return
	}
	r.mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate",
		r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination)))
	if r.externalDeliveries != nil {
		r.mux.Method(http.MethodPost, "/internal/v1/deliveries",
			r.internalVeloxAuth(http.HandlerFunc(r.handleCreateInternalDelivery)))
		// GET /internal/v1/deliveries/{id} — Velox reconciliation/poll.
		// The id is the social_delivery_id minted by handleCreateInternalDelivery
		// (shape: "sdel_01J…"). Velox polls this endpoint when its
		// outbound callback channel drops a packet (network blip,
		// peer restarts, 502 storm). Same Bearer auth as the other
		// /internal/v1 routes — the middleware separately handles
		// 401 missing-header / 403 token-mismatch before this handler
		// runs.
		r.mux.Method(http.MethodGet, "/internal/v1/deliveries/{id}",
			r.internalVeloxAuth(http.HandlerFunc(r.handleGetInternalDelivery)))
	}
}

// WithExternalDestinationStore wires
// *repository.ExternalDestinationRepository into the Router.
// Following the WorkspaceStore / PostStore nil-guard pattern:
// when the option is omitted, /internal/v1 routes return 404
// (the helper refuses to register them). Production wiring
// in internal/bootstrap.Wire passes
// repository.NewExternalDestinationRepository(db).
//
// Plus WithVeloxAPIToken AND the user/workspace stores MUST
// be wired for the validate handler's full happy path. Calling
// only this option but not WithVeloxAPIToken leaves the route
// un-registered. cmd/server/main.go is responsible for
// wiring all three (or all four, including WithWorkspaceStore
// + WithUserStore which are normally wired earlier).
func WithExternalDestinationStore(s ExternalDestinationStore) RouterOption {
	return func(r *Router) { r.externalDestinations = s }
}

// WithExternalDeliveryStore wires
// *repository.ExternalDeliveryRepository into the Router for
// POST /internal/v1/deliveries. Mirrors
// WithExternalDestinationStore: when omitted, the deliveries
// route is not registered (the registerInternalVeloxRoutes
// helper nil-guards). The validate route is unaffected —
// only the deliveries route depends on this option.
//
// Production wiring in internal/bootstrap.Wire passes the
// SAME *repository.ExternalDeliveryRepository struct that
// repos/backend already uses for handler-side lookups; the
// struct is BOTH an ExternalDestinationStore (its
// GetByID method) AND an ExternalDeliveryStore (its Insert
// method) per the compile-time assertions above.
func WithExternalDeliveryStore(s ExternalDeliveryStore) RouterOption {
	return func(r *Router) { r.externalDeliveries = s }
}
