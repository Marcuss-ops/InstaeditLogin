package models

import (
	"encoding/json"
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

// Next returns the canonical next-state successor for the publish
// pipeline. Returned status is "" when no further transition is
// allowed (terminal states). The publish-worker uses this in
// MarkIngested → MarkQueued → MarkPublishing transitions.
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
	ID                   string                 `json:"id"`                       // sdel_01J...
	SourceSystem         string                 `json:"source_system"`            // "velox"
	ExternalDeliveryID   string                 `json:"external_delivery_id"`     // upstream's id
	IdempotencyKey       string                 `json:"idempotency_key"`          // upstream's composite key
	ExternalDestinationID string                `json:"external_destination_id"` // FK to extdst_01J...

	SourceArtifactID     string                 `json:"source_artifact_id"`       // upstream's artifact
	ExpectedSHA256       string                 `json:"expected_sha256"`          // hex 64-char
	ExpectedSizeBytes    int64                  `json:"expected_size_bytes"`      // bytes
	ExpectedMimeType     string                 `json:"expected_mime_type"`       // MIME

	DownloadURL          *string                `json:"download_url,omitempty"`   // presigned S3 / HMAC artifact endpoint
	Metadata             json.RawMessage        `json:"metadata"`                 // JSONB publish envelope
	PublishAt            *time.Time             `json:"publish_at,omitempty"`     // scheduled wall-clock
	CallbackURL          *string                `json:"callback_url,omitempty"`   // Velox HMAC webhook

	Status               ExternalDeliveryStatus `json:"status"`                   // 11-value CHECK
	RequestSHA256        string                 `json:"-"`                        // body hash for 409 detection; never serialised

	UploadJobID          *int64                 `json:"upload_job_id,omitempty"`  // FK to upload_jobs(id)
	PostID               *int64                 `json:"post_id,omitempty"`        // BIGINT no FK per spec
	PlatformMediaID      *string                `json:"platform_media_id,omitempty"`
	PlatformURL          *string                `json:"platform_url,omitempty"`

	LastErrorCode        *string                `json:"last_error_code,omitempty"`
	LastErrorMessage     *string                `json:"last_error_message,omitempty"`

	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	CompletedAt          *time.Time             `json:"completed_at,omitempty"`
}
