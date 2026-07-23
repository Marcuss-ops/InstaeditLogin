package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExternalDeliveryStatus is the 11-value lifecycle enum on the
// external_deliveries.status column. Mirrors the named CHECK
// constraint from migration 055 (
// chk_external_deliveries_status). The DB enforces the set; this
// Go-side enum exists so the publish_worker, callback dispatcher,
// and dashboard can switch on the state with a typed value rather
// than a stringly-typed string.
//
// Lifecycle:
//
//	accepted → downloading → artifact_verified → ingest_completed
//	    → queued → publishing → published
//
// Side states (parallel to the main flow):
//
//	retry_wait     — transient error, exponential backoff
//	blocked_auth   — reauth_required on platform_account, halts
//	failed         — terminal non-recoverable
//	dead_letter    — max_attempts exhausted
//
// ─── ON-CALL DBA CAVEAT (read this BEFORE issuing a direct-SQL UPDATE) ───
//
// The Go-layer guard (CanTransitionTo + transitionMap) is BYPASSED
// when you issue a raw `UPDATE external_deliveries SET status = '...'`
// via psql or a migration script. The model layer never runs; the
// SQL CHECK only validates the value set (one of the 11 strings),
// not the transition graph. Direct-SQL repairs carry the operator's
// FULL responsibility: review the transitionMap comments below AND
// docs/OPERATIONS.md "external_deliveries status column" before any
// UPDATE. An illegal transition landed via psql will surface as
// stale-state rows in the dashboard and is impossible to detect
// automatically. Use the Go worker or admin tools (handler-level
// transitions) whenever a fix can go through them; reserve
// direct-SQL for true emergencies and document the rationale in
// the change ticket.
type ExternalDeliveryStatus string

const (
	// ExternalDeliveryStatusAccepted — POST /deliveries stored, Velox
	// received 202. The worker has not picked the row up yet.
	ExternalDeliveryStatusAccepted ExternalDeliveryStatus = "accepted"
	// ExternalDeliveryStatusDownloading — worker issued HEAD/GET against
	// the download_url from the Velox artifact reference.
	ExternalDeliveryStatusDownloading ExternalDeliveryStatus = "downloading"
	// ExternalDeliveryStatusArtifactVerified — sha256 + size + mime all
	// match, file promoted into InstaEdit storage. Worker is ready to
	// create upload_job.
	ExternalDeliveryStatusArtifactVerified ExternalDeliveryStatus = "artifact_verified"
	// ExternalDeliveryStatusIngestCompleted — upload_job created AND
	// stamped as ingest_completed (matches migration 049a's upload
	// enum value). Publish pool is eligible to claim it once
	// publish_at elapses.
	ExternalDeliveryStatusIngestCompleted ExternalDeliveryStatus = "ingest_completed"
	// ExternalDeliveryStatusQueued — publish_pool picked the row up
	// and the publish_at schedule window has opened.
	ExternalDeliveryStatusQueued ExternalDeliveryStatus = "queued"
	// ExternalDeliveryStatusPublishing — YouTube API videos.insert in
	// flight (or analogous for the non-YouTube provider normalised
	// upstream).
	ExternalDeliveryStatusPublishing ExternalDeliveryStatus = "publishing"
	// ExternalDeliveryStatusPublished — terminal success. platform_media_id
	// + platform_url populated.
	ExternalDeliveryStatusPublished ExternalDeliveryStatus = "published"
	// ExternalDeliveryStatusRetryWait — transient error, exponential
	// backoff in progress. next_attempt_at must be > NOW() for the row
	// to skip the worker pool's claim CTE.
	ExternalDeliveryStatusRetryWait ExternalDeliveryStatus = "retry_wait"
	// ExternalDeliveryStatusBlockedAuth — reauth_required on the
	// platform_account; worker halts; admin must reconnect.
	ExternalDeliveryStatusBlockedAuth ExternalDeliveryStatus = "blocked_auth"
	// ExternalDeliveryStatusFailed — terminal non-recoverable failure
	// (e.g. artifact_sha256_mismatch, JSON validation). Operator
	// intervention required.
	ExternalDeliveryStatusFailed ExternalDeliveryStatus = "failed"
	// ExternalDeliveryStatusDeadLetter — retry budget exhausted. Row is
	// persisted for ops review; no further processing.
	ExternalDeliveryStatusDeadLetter ExternalDeliveryStatus = "dead_letter"
)

// IsTerminal classifies the "no further automatic transitions
// expected" set. Mirrors the upload_job_repo classification (migration
// 049c). Used by the worker to skip ClaimBatchForPublish and by the
// dashboard to surface "what's done" vs "what's in flight".
func (s ExternalDeliveryStatus) IsTerminal() bool {
	switch s {
	case ExternalDeliveryStatusPublished,
		ExternalDeliveryStatusFailed,
		ExternalDeliveryStatusDeadLetter,
		ExternalDeliveryStatusBlockedAuth:
		return true
	}
	return false
}

// IsRetryable indicates the worker pool's ClaimBatch CTE should
// pick this row up (status IN active set). retry_wait is the sole
// non-active state that's still resumable: the row re-enters the
// claim pool when next_attempt_at elapses (a column on
// upload_jobs; the delivery journal mirrors the same semantics
// with claim_attempt_at but the publish_worker reads the
// upload_job's column directly).
func (s ExternalDeliveryStatus) IsRetryable() bool {
	switch s {
	case ExternalDeliveryStatusAccepted,
		ExternalDeliveryStatusDownloading,
		ExternalDeliveryStatusArtifactVerified,
		ExternalDeliveryStatusIngestCompleted,
		ExternalDeliveryStatusQueued,
		ExternalDeliveryStatusPublishing,
		ExternalDeliveryStatusRetryWait:
		return true
	}
	return false
}

// Next returns the canonical HAPPY-PATH one-step successor for
// the publish pipeline. Returned status is "" when no further
// transition is allowed via THIS method (terminal states OR
// side-states like retry_wait / blocked_auth). For the canonical
// state-machine graph (which includes error exits + resume
// transitions), use LegalTransitions() or CanTransitionTo().
//
// The publish-worker uses this in the no-error-exit branch
// (MarkIngested → MarkQueued → MarkPublishing when everything
// succeeds). Branching decisions (retry vs fail vs block vs
// dead-letter) use CanTransitionTo directly.
func (s ExternalDeliveryStatus) Next() ExternalDeliveryStatus {
	switch s {
	case ExternalDeliveryStatusAccepted:
		return ExternalDeliveryStatusDownloading
	case ExternalDeliveryStatusDownloading:
		return ExternalDeliveryStatusArtifactVerified
	case ExternalDeliveryStatusArtifactVerified:
		return ExternalDeliveryStatusIngestCompleted
	case ExternalDeliveryStatusIngestCompleted:
		return ExternalDeliveryStatusQueued
	case ExternalDeliveryStatusQueued:
		return ExternalDeliveryStatusPublishing
	case ExternalDeliveryStatusPublishing:
		return ExternalDeliveryStatusPublished
	}
	return ""
}

// transitionMap enumerates the LEGAL state-to-state transitions
// in the Velox→InstaEdit delivery lifecycle. The map is the
// single source of truth — CanTransitionTo + LegalTransitions
// both derive from here. SQL CHECK constraint only enforces the
// value set, not the transition graph, so the model layer is
// the contract.
//
// Every enum value MUST have an entry (possibly empty for
// terminal states); the TestTransitionMapEnumCoverage test
// guards against accidentally omitting a new enum value.
//
// OPERATOR DIRECT-SQL CAVEAT (read this if you're an on-call DBA
// fixing a stuck row via psql): the Go-layer guard is BYPASSED
// when you issue a raw UPDATE on external_deliveries. The model
// layer's ContractTransition doesn't run; the SQL CHECK only
// validates the value set. Direct-SQL repairs carry the
// operator's full responsibility: review the transition map
// above AND docs/OPERATIONS.md before writing the UPDATE. An
// illegal transition landed via psql will surface as stale-state
// rows in the dashboard and is impossible to detect automatically.
//
//	from → targets (legal successors only)
//
// Happy-path forward (the 6 stage advance transitions) are
// always present; error exits (→ retry_wait / blocked_auth /
// failed) are present from every pre-terminal state; resume
// transitions (retry_wait → downloading OR queued,
// blocked_auth → queued) and the dead_letter exit
// (retry_wait only) round out the closure.
//
// Terminal states (published / failed / dead_letter) have
// EMPTY successor maps — no outgoing transitions of any kind.
// A terminal → non-terminal transition is rejected by
// CanTransitionTo (would surface as a SQL update with rows
// affected = 1 but the journal now holds an illegal tuple).
var transitionMap = map[ExternalDeliveryStatus]map[ExternalDeliveryStatus]bool{
	ExternalDeliveryStatusAccepted: {
		ExternalDeliveryStatusDownloading: true, // happy-path forward
		ExternalDeliveryStatusRetryWait:   true, // file fetch transient (5xx, etc)
		ExternalDeliveryStatusBlockedAuth: true, // auth failure mid-fetch
		ExternalDeliveryStatusFailed:      true, // JSON validation / permanent
	},
	ExternalDeliveryStatusDownloading: {
		ExternalDeliveryStatusArtifactVerified: true,
		ExternalDeliveryStatusRetryWait:        true,
		ExternalDeliveryStatusBlockedAuth:      true,
		ExternalDeliveryStatusFailed:           true, // SHA mismatch / MIME mismatch
	},
	ExternalDeliveryStatusArtifactVerified: {
		ExternalDeliveryStatusIngestCompleted: true,
		ExternalDeliveryStatusRetryWait:       true,
		ExternalDeliveryStatusBlockedAuth:     true,
		ExternalDeliveryStatusFailed:          true, // storage upload permanent
	},
	ExternalDeliveryStatusIngestCompleted: {
		ExternalDeliveryStatusQueued:      true,
		ExternalDeliveryStatusRetryWait:   true,
		ExternalDeliveryStatusBlockedAuth: true,
		ExternalDeliveryStatusFailed:      true,
	},
	ExternalDeliveryStatusQueued: {
		ExternalDeliveryStatusPublishing:  true,
		ExternalDeliveryStatusRetryWait:   true,
		ExternalDeliveryStatusBlockedAuth: true, // platform_account reauth mid-schedule
		ExternalDeliveryStatusFailed:      true,
	},
	ExternalDeliveryStatusPublishing: {
		ExternalDeliveryStatusPublished:   true, // success
		ExternalDeliveryStatusRetryWait:   true, // transient API failure
		ExternalDeliveryStatusBlockedAuth: true, // auth refresh during upload
		ExternalDeliveryStatusFailed:      true, // quota / permanent
	},
	// Published → terminal — no outgoing transitions of any kind.
	ExternalDeliveryStatusPublished: {},

	// retry_wait has TWO legal resume targets (downloading vs
	// queued). Use CanonicalResume() to pick deterministically
	// based on download_url expiration; both are listed here
	// because the worker CAN pick either at runtime and the
	// state-machine graph must permit both.
	ExternalDeliveryStatusRetryWait: {
		ExternalDeliveryStatusDownloading: true, // re-fetch artifact on URL expiry
		ExternalDeliveryStatusQueued:      true, // resume from queue (skip re-fetch)
		ExternalDeliveryStatusBlockedAuth: true,
		ExternalDeliveryStatusFailed:      true,
		ExternalDeliveryStatusDeadLetter:  true, // retry budget exhausted
	},
	ExternalDeliveryStatusBlockedAuth: {
		ExternalDeliveryStatusQueued: true, // admin reconnect handler resumes publish
	},
	// Failed → terminal — no outgoing transitions.
	ExternalDeliveryStatusFailed: {},
	// DeadLetter → terminal — no outgoing transitions.
	ExternalDeliveryStatusDeadLetter: {},
}

// CanTransitionTo returns whether the proposed transition is
// legal per the state-machine map. Returns false for terminal
// states (no outgoing transitions), for unknown enum values
// (defensive), or when source/target is empty (caller bug — an
// empty status is never a legal transition target).
//
// Worker / handler / dashboard code MUST call this BEFORE
// external_delivery_repo.UpdateStatus; UpdateStatus accepts
// any of the 11 values silently (the SQL CHECK constraint
// only validates the value set, not the transition graph),
// so an absent guard would let a buggy transition like
// `published → accepted` regress the journal in a way that's
// invisible until the dashboard reports stale-state rows.
//
// Empty source OR empty target → false (defensive; an empty
// status indicates a caller bug — the SQL column never holds
// the empty string because the CHECK rejects it).
func (s ExternalDeliveryStatus) CanTransitionTo(target ExternalDeliveryStatus) bool {
	if s == "" || target == "" {
		return false
	}
	successors, ok := transitionMap[s]
	if !ok {
		return false
	}
	return successors[target]
}

// LegalTransitions returns the deterministically-ordered set of
// statuses that may legally follow s. Returns nil for terminal
// states. Used by:
//   - The dashboard's "what's next" hint UI (operator surface)
//   - The audit log's allowed-action surface
//   - The integration tests (full transition-graph coverage)
//
// Order is by string-sort of the status values — stable across
// process restarts and platform-independent. Don't assume a
// specific physical order; consume via the typed slice and
// string-compare if a specific order matters.
func (s ExternalDeliveryStatus) LegalTransitions() []ExternalDeliveryStatus {
	successors, ok := transitionMap[s]
	if !ok || len(successors) == 0 {
		return nil
	}
	out := make([]ExternalDeliveryStatus, 0, len(successors))
	for tgt, allowed := range successors {
		if allowed {
			out = append(out, tgt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// CanonicalResume encodes the OPERATOR-CHOSEN DEFAULT for picking
// between the two legal retry_wait resume targets (downloading
// vs queued). The worker calls this instead of asserting its
// own opinion on which resume path to take.
//
// The "Canonical" name reflects the operator-runbook default for
// the COMMON case; the worker MAY override for exceptional cases
// (download-URL-pinned retries that intentionally bypass the
// URL re-validation, manual ops interventions, etc). Both targets
// are LEGAL transitions per transitionMap, so an override that
// picks the OTHER target is fine — just confirm CanTransitionTo
// first.
//
// Rule (per Operator decision in ARCHITECTURE.md):
//
//   - download_url_valid=true: signer URL TTL hasn't elapsed, or
//     the HMAC signature is still within its 30-min validity
//     window at resume time. Worker resumes from `queued` — the
//     artifact is already in InstaEdit storage; no re-fetch
//     needed.
//   - download_url_valid=false: signed URL expired, signature
//     stale, etc. Worker re-fetches from `downloading` — the
//     artifact must be re-validated (re-hashed SHA + size + MIME)
//     before re-entering the pipeline.
//
// ASSUMPTION (callers MUST verify before calling): the caller has
// already verified that ExternalDelivery.DownloadURL is non-nil.
// The migration allows nullable DownloadURL because of "metadata
// only" deliveries that have no artifact at all; CanonicalResume
// has no visibility into the URL pointer — passing the metadata-only
// path through CanonicalResume(true) → `queued` would be a
// semantic bug (no artifact exists to skip re-fetch). Worker
// MUST special-case the metadata-only delivery BEFORE calling
// CanonicalResume, or rely on the helper's empty-return to signal
// "don't resume through this path".
//
// Returns the default resume state. Returns "" when the input is
// not retry_wait — there's no canonical resume target for other
// states (use Next() for happy-path forward or CanTransitionTo()
// for free-form transitions).
//
// The download_url_valid parameter is supplied by the worker
// after a HEAD probe against the URL's signer metadata (S3
// presigned: parse the X-Amz-Expires query param; HMAC
// endpoint: re-derive the signature TTL).
func (s ExternalDeliveryStatus) CanonicalResume(downloadURLValid bool) ExternalDeliveryStatus {
	if s != ExternalDeliveryStatusRetryWait {
		return ""
	}
	if downloadURLValid {
		return ExternalDeliveryStatusQueued
	}
	return ExternalDeliveryStatusDownloading
}

// ExternalDelivery is the 11-state lifecycle journal for POST
// /internal/v1/deliveries (Velox handoff). One row per accepted
// request. PK is TEXT (ULID with `sdel_` prefix) generated
// application-side. Idempotency via UNIQUE
// (source_system, idempotency_key) + request_sha256 body-hash for
// same-key-different-body → 409 detection.
//
// JSON-tagged with omitempty on the nullable pointers + json.RawMessage
// so the API can re-serialise for the outbound response with
// minimum empty fields.
//
// Mirrors migration 055_external_deliveries.sql.
type ExternalDelivery struct {
	ID                    string `json:"id"`                      // sdel_01J...
	SourceSystem          string `json:"source_system"`           // "velox"
	ExternalDeliveryID    string `json:"external_delivery_id"`    // upstream's id
	IdempotencyKey        string `json:"idempotency_key"`         // upstream's composite key
	ExternalDestinationID string `json:"external_destination_id"` // FK to extdst_01J...

	SourceArtifactID  string `json:"source_artifact_id"`  // upstream's artifact
	ExpectedSHA256    string `json:"expected_sha256"`     // hex 64-char
	ExpectedSizeBytes int64  `json:"expected_size_bytes"` // bytes
	ExpectedMimeType  string `json:"expected_mime_type"`  // MIME

	DownloadURL *string         `json:"download_url,omitempty"` // presigned S3 / HMAC artifact endpoint
	Metadata    json.RawMessage `json:"metadata"`               // JSONB publish envelope
	PublishAt   *time.Time      `json:"publish_at,omitempty"`   // scheduled wall-clock
	CallbackURL *string         `json:"callback_url,omitempty"` // Velox HMAC webhook

	Status        ExternalDeliveryStatus `json:"status"` // 11-value CHECK
	RequestSHA256 string                 `json:"-"`      // body hash for 409 detection; never serialised

	UploadJobID     *int64  `json:"upload_job_id,omitempty"` // FK to upload_jobs(id)
	PostID          *int64  `json:"post_id,omitempty"`       // BIGINT no FK per spec
	PlatformMediaID *string `json:"platform_media_id,omitempty"`
	PlatformURL     *string `json:"platform_url,omitempty"`

	LastErrorCode    *string `json:"last_error_code,omitempty"`
	LastErrorMessage *string `json:"last_error_message,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Durable worker-queue fields. Replaces the in-process channel
	// between the API producer and the Velox artifact downloader.
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	AttemptCount     int        `json:"attempt_count"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	LeasedByWorkerID *string    `json:"leased_by_worker_id,omitempty"`
	MaxAttempts      int        `json:"max_attempts"`
}

// VeloxDeliveryMetadata is the single typed representation of the
// JSONB publish envelope stored in external_deliveries.metadata.
// It is parsed once at the HTTP boundary (after destination defaults
// are merged) and reused by the worker instead of repeatedly decoding
// the raw JSON blob.
type VeloxDeliveryMetadata struct {
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	PrivacyStatus    string  `json:"privacy_status"`
	TargetAccountIDs []int64 `json:"target_account_ids"`
	DriveAccountID   *int64  `json:"drive_account_id"`
	FolderID         *string `json:"folder_id"`
}

// ParseVeloxDeliveryMetadata parses a raw JSONB metadata blob into a
// VeloxDeliveryMetadata. It returns an error when the blob is not a
// JSON object or cannot be decoded.
func ParseVeloxDeliveryMetadata(raw json.RawMessage) (*VeloxDeliveryMetadata, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("metadata is empty")
	}
	var m VeloxDeliveryMetadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("metadata is not valid JSON: %w", err)
	}
	return &m, nil
}

// Validate checks the metadata after destination defaults have been
// merged. It ensures the payload carries the minimum fields needed by
// the publish pipeline and that optional identifiers are well-formed.
func (m *VeloxDeliveryMetadata) Validate() error {
	if m == nil {
		return fmt.Errorf("metadata is nil")
	}
	if strings.TrimSpace(m.Title) == "" && strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("metadata must contain a non-empty title or description")
	}
	if m.PrivacyStatus != "" {
		switch m.PrivacyStatus {
		case "private", "public", "unlisted":
		default:
			return fmt.Errorf("metadata.privacy_status must be private, public or unlisted, got %q", m.PrivacyStatus)
		}
	}
	for _, id := range m.TargetAccountIDs {
		if id <= 0 {
			return fmt.Errorf("metadata.target_account_ids must contain positive IDs, got %d", id)
		}
	}
	if m.DriveAccountID != nil && *m.DriveAccountID <= 0 {
		return fmt.Errorf("metadata.drive_account_id must be positive when set")
	}
	if m.FolderID != nil && strings.TrimSpace(*m.FolderID) == "" {
		return fmt.Errorf("metadata.folder_id must be non-empty when set")
	}
	return nil
}
