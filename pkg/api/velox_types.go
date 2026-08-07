package api

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ExternalDestinationStore is the persistence contract for
// external_destinations. Mirrors the WorkspaceStore / PostStore
// pattern declared in handlers.go: a local interface so tests
// can supply an in-memory fake without dragging the *sql.DB-bound
// *repository.ExternalDestinationRepository into the test fixture.
// The production wiring in the canonical bootstrap passes
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
	ListByWorkspace(ctx context.Context, workspaceID int64, enabledOnly bool) ([]models.ExternalDestination, error)
	Delete(ctx context.Context, id string) error
	// UpdateEnabledAndDefaults applies a partial update to enabled AND/OR
	// default_metadata in a SINGLE atomic UPDATE statement (COALESCE
	// preserves columns the caller didn't supply; nil/zero bytes bind
	// as SQL NULL). Closes the partial-write window that a previous
	// two-call sequence left open: a concurrent DELETE between the
	// calls could leave the row half-updated. This is the only update
	// verb exposed by the store.
	UpdateEnabledAndDefaults(ctx context.Context, id string, enabled *bool, defaults json.RawMessage) error
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

// ContractVersionDelivery is the only wire contract accepted by
// POST /internal/v1/deliveries.
const ContractVersionDelivery = "velox.delivery.v1"

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
	ContractVersion       string           `json:"contract_version,omitempty"`
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
}

// VeloxGetDeliveryResponse is the canonical body returned by
// GET /internal/v1/deliveries/{id}. It contains only the v1 delivery
// identity/state fields and canonical remote media ID; v0 aliases are
// deliberately not represented.
type VeloxGetDeliveryResponse struct {
	DeliveryID       string                 `json:"delivery_id"`
	VeloxJobID       string                 `json:"velox_job_id,omitempty"`
	Target           VeloxGetDeliveryTarget `json:"target"`
	PublishStatus    string                 `json:"publish_status"`
	ThumbnailStatus  string                 `json:"thumbnail_status"`
	YouTubeVideoID   string                 `json:"youtube_video_id,omitempty"`
	Privacy          string                 `json:"privacy,omitempty"`
	LastErrorCode    string                 `json:"last_error_code,omitempty"`
	LastErrorMessage string                 `json:"last_error_message,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// VeloxGetDeliveryTarget is the resolved channel-info block on
// the GET-delivery response. Mirrors §8's `target` sub-object:
//
//	"target": {
//	  "platform_account_id": 381,
//	  "channel_id":         "UCxxxxxxxx",
//	  "channel_name":       "Wrestling Discovery",
//	  "enabled":            true
//	}
//
// Resolution chain (3 sequential lookups, all idempotent and
// fast-indexed):
//  1. external_deliveries.external_destination_id
//     → external_destinations (platform_account_id, workspace_id)
//  2. platform_accounts      (platform_user_id, username, status, …)
//  3. workspace_channels     (enabled flag for this workspace)
//
// Partial-fidelity tolerance: any single step that fails to
// find a row returns a zero-value for the fields that depend
// on it (NOT a 500). The delivery is real even if its binding
// has been unbound since accept time; the operator dashboard
// surfaces partial targets as "binding missing; reconcile
// needed" without breaking the GET contract.
type VeloxGetDeliveryTarget struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	ChannelID         string `json:"channel_id"`
	ChannelName       string `json:"channel_name,omitempty"`
	Enabled           bool   `json:"enabled"`
}

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

// SHA-256 lowercase-hex regex. Compiled once at package init.
// 64 chars, [0-9a-f]{64}. Uppercase hex is rejected (callers
// must lowercase before submitting to match the canonical
// representation produced by sha256.Sum256().hex.EncodeToString()).
var sha256HexRegex = regexp.MustCompile(`^[a-f0-9]{64}$`)

// mimeAllowlist. Phase 1 video-only. The worker downstream
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

// VeloxDestinationUpdateAuditDeltas pins the structured metadata
// schema emitted by the audit log on PATCH
// /api/v1/integrations/velox/destinations/{id}.
//
// The JSON tags are stable contract: dashboards / ELK / Snowflake
// ingest can index on the emitted keys exactly. omitempty is
// deliberately OFF on Enabled so the key is always present -- it
// surfaces as JSON null when the PATCH body omitted the field, as
// JSON true/false when the body supplied it. The contract is
// "keys are exactly {enabled, defaults_changed}", not "keys when
// fired"; this pins the dashboard schema and removes the
// mixed-bool+sentinel-string risk flagged in the previous code
// review.
type VeloxDestinationUpdateAuditDeltas struct {
	Enabled         *bool `json:"enabled"`
	DefaultsChanged bool  `json:"defaults_changed"`
}
