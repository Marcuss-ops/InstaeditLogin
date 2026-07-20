package models

import "time"

// DeliverySession is the Task 8/10 row representation of the
// delivery_sessions table. It captures the resumable-upload state
// of a DeliveryProvider invocation (today: Google Drive; future:
// S3, MinIO, Velox callback retries) so a worker crash mid-upload
// can RESUME from the persisted offset and a retry after a hard
// failure does not create a duplicate Drive file.
//
// Lifecycle:
//
//	initiated  — POST /upload/drive/v3/files returned a session
//	             URI; uploaded_bytes=0; about to stream chunk 1.
//	uploading  — at least one chunk PUT acknowledged (HTTP 308 +
//	             Range header); uploaded_bytes reflects the
//	             server-confirmed offset (NOT what we sent — what
//	             the server accepted).
//	completed  — final 200 received; remote_file_id + remote_url
//	             stamped; session_uri_encrypted cleared so the
//	             row doesn't carry a dead URI.
//	failed     — error_message + error_code stamped; row is a
//	             retry candidate (counter increments on each
//	             retry attempt, gated by app-property dedupe).
//	expired    — Google's 7-day resumable session TTL elapsed
//	             between restarts. full re-initiation required
//	             on the next Deliver call.
//
// Identity (deliverable_type, idempotency_key) is the canonical
// dedupe pair: the database UNIQUE constraint enforces it (a
// retry of the same logical delivery cannot create a second
// row). The fields Mirror the migration 057 column list verbatim —
// adding a column there requires a mirrored edit here AND in
// the repository's scanDeliverySession helper.
type DeliverySession struct {
	// ID is the database-assigned identity. Auto-populated by the
	// repository on Create; callers leave it zero on the way in.
	ID int64

	// DeliverableType is the provider key (e.g. "google_drive"
	// today; future "s3", "minio", "velox_ack"). Combined with
	// IdempotencyKey it's the table-level UNIQUE guarantee.
	DeliverableType string

	// IdempotencyKey is the stable identifier of the LOGICAL
	// delivery (typically fmt.Sprintf("post_target_%d",
	// target.ID)). Two Deliver calls with the same key MUST
	// resolve to the same terminal Drive file ID.
	IdempotencyKey string

	// State is one of: "initiated", "uploading", "completed",
	// "failed", "expired". The CHK schema constraint enforces
	// the set; the Go-side validation here is defensive.
	State DeliverySessionState

	// SessionURIEncrypted is the ciphertext (base64 of the
	// SessionEncryptor output) of the resumable session URI.
	// Empty after State == "completed" so the row doesn't carry
	// a stale, dead URI on disk.
	SessionURIEncrypted string

	// UploadedBytes is the server-confirmed byte count. After
	// each chunk PUT the destination persists the Range header
	// the server returned so the NEXT attempt resumes at the
	// right offset even if a chunk PUT ACK was lost in flight.
	UploadedBytes int64

	// TotalBytes is the source artifact size — stamped at Create
	// time so the chunk loop knows when the upload is complete.
	TotalBytes int64

	// ChunkSize is the PUT chunk size in bytes (Drive minimum is
	// 256 KiB / 262144; production default is 16 MiB).
	ChunkSize int64

	// MIMEType is the X-Upload-Content-Type derived from the
	// source (video/mp4 for YouTube-typical assets). Echoed to
	// Drive's mimeType field on the file metadata POST body.
	MIMEType string

	// FolderID is the destination Shared Drive folder the file
	// will be placed in. Optional; when empty the destination
	// uploads to the operator's My Drive root.
	FolderID string

	// Filename is the resolved filename (after DriveFilename
	// template expansion; today a literal "%s.mp4" pattern).
	Filename string

	// AppProperties is the JSONB blob under appProperties.*
	// on the Drive file. Always includes
	// "instaedit_delivery_id"=<idempotency_key> so an
	// app-property-based idempotency lookup can recover a
	// surviving file after a hard row wipe.
	AppProperties map[string]string

	// RemoteFileID is the Drive file ID after the final 200
	// ACK. Emptypost-upload-verify; populated once state flips
	// to "completed".
	RemoteFileID string

	// RemoteURL is https://drive.google.com/file/d/<id>/view
	// or the webViewLink the server returned. Echoed onto
	// DeliveryResult.RemoteURL so the dashboard surfaces a
	// clickable link for operators.
	RemoteURL string

	// WorkerID is the worker that CURRENTLY owns the lease.
	// Cleared (NULL) on terminal state transitions.
	WorkerID string

	// LeaseExpiresAt is the lease deadline for the worker
	// holding in-flight progress claims. NULL when no lease
	// is held (immediately after Create or after terminal
	// transition).
	LeaseExpiresAt *time.Time

	// ExpiresAt is the Drive resumable session URI TTL. Drive
	// session IDs expire after 7 days; the destination refuses
	// to use a persisted URI past this deadline (transitions
	// to "expired" + re-initiation on next Deliver call).
	ExpiresAt *time.Time

	// ErrorMessage is the latest failure detail. Stamped on
	// each failed chunk attempt; cleared on successful resume.
	ErrorMessage string

	// ErrorCode is the typed sentinel key for the failure
	// ("drive_session_expired", "drive_session_lost", etc.)
	// for dashboard surfacing. Cleared on successful resume.
	ErrorCode string

	// AttemptCount is the number of Deliver() calls that have
	// touched this row. Gated by app-property dedupe so a
	// retry that finds an existing file does NOT increment.
	AttemptCount int

	// Version is the optimistic-concurrency token. Every
	// UPDATE ... WHERE id = $N AND version = $X RETURNING
	// version+1 ensures concurrent workers cannot overwrite
	// each other's progress. Initialised to 1 on Create;
	// the repository's UpdateWithVersion bumps it on each
	// successful write.
	Version int

	// CreatedAt / UpdatedAt are stamped by the database.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DeliverySessionState is the typed enum matching the schema CHK
// constraint on delivery_sessions.state. Mirrors the DB set 1:1
// so any future state addition requires an explicit extension here
// AND a corresponding test that catches drift at compile time.
type DeliverySessionState string

const (
	// DeliverySessionStateInitiated: POST returned a session URI;
	// no bytes sent yet. Persists session_uri_encrypted; uploaded_bytes=0.
	DeliverySessionStateInitiated DeliverySessionState = "initiated"
	// DeliverySessionStateUploading: at least one chunk PUT acknowledged.
	// Persists uploaded_bytes from the Range-header round-trip.
	DeliverySessionStateUploading DeliverySessionState = "uploading"
	// DeliverySessionStateCompleted: terminal success. remote_file_id
	// + remote_url stamped; session_uri_encrypted cleared.
	DeliverySessionStateCompleted DeliverySessionState = "completed"
	// DeliverySessionStateFailed: terminal failure (chunk loop budget
	// exhausted, server rejected payload, etc.). Retry-eligible.
	DeliverySessionStateFailed DeliverySessionState = "failed"
	// DeliverySessionStateExpired: Google's 7-day TTL elapsed; the
	// persisted session URI must be discarded + the destination must
	// POST a fresh initiate. Retry-eligible (treated as 'failed' by
	// the chunk loop; flagged distinctly so the operator can see the
	// TTL breach separately from transient failures).
	DeliverySessionStateExpired DeliverySessionState = "expired"
)

// IsTerminal returns true for states that MUST NOT be re-entered by
// Deliver (completed; failed + retry budget exhausted = dead_letter
// would be the next state but not in this naming). "failed" and
// "expired" are NON-terminal so a retry can resume from the persisted
// offset; "completed" and "expired" are high-confidence terminal for
// driving the dispatch decision (skip upload).
func (s DeliverySessionState) IsTerminal() bool {
	switch s {
	case DeliverySessionStateCompleted, DeliverySessionStateExpired:
		return true
	default:
		return false
	}
}
