package api

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// driveBatchJitterMaxSeconds caps how far in the future a scheduled post
// can be. 7 days is large enough for a typical folder (~30 videos *
// ~5h stagger = ~6 days), small enough that a misconfigured batch
// doesn't park posts for weeks. NOTE: this is a SILENT cap — if a
// cumulative schedule would push past it, jobs are clamped (without
// telling the caller). Operators wanting a longer horizon can bump
// this constant; consider surfacing it in Note if you do.
const driveBatchJitterMaxSeconds = 7 * 24 * 60 * 60

// DriveBatchImportRequest is the body for
// POST /api/v1/media/import/drive/folder.
//
// Lists every video in a (public or accessible) Google Drive folder,
// creates one upload_job per video, and schedules the resulting posts
// with a CUMULATIVE random gap. The first job's scheduled_at is NOW
// so the publish_worker picks it up on the next tick (≈1s) — the
// user can therefore watch the first publish happen end-to-end within
// ~1 minute of the call. Subsequent jobs are scheduled at
// job[i-1].scheduled_at + rand(min_jitter, max_jitter) so the gap
// between consecutive posts is randomised (anti-pattern detection on
// each platform).
//
// Folders with >200 videos: Drive's API returns at most 200 per call.
// The response includes `next_page_token`; the caller re-invokes the
// endpoint with `page_token` set AND `cursor_scheduled_at` set to the
// PREVIOUS response's `last_scheduled_at` so the cumulative stagger
// continues uninterrupted across page boundaries. This avoids the
// "all of page-2 publishes back-to-back" anti-pattern when split.
type DriveBatchImportRequest struct {
	// FolderID is the Drive folder id (the part after /folders/ in the
	// share URL).
	FolderID string `json:"folder_id"`
	// DriveAccountID is optional. When set, the user's linked Drive
	// OAuth grant is used to list the folder (works for folders the
	// user has access to, including private/shared). When zero, the
	// folder must be public and the server must have GOOGLE_DRIVE_API_KEY
	// configured at the deployment level.
	DriveAccountID int64 `json:"drive_account_id"`
	// WorkspaceID is the workspace that will own the scheduled posts.
	WorkspaceID int64 `json:"workspace_id"`
	// FacebookAccountID is the platform_accounts.id of the Facebook
	// Page (each Page = one platform_account; from DiscoverAccounts on
	// OAuth connect).
	FacebookAccountID int64 `json:"facebook_account_id"`
	// Title is optional. If set, every post uses this exact title; if
	// empty, the Drive file's name is used per post so the user can tell
	// them apart on their Page timeline.
	Title string `json:"title"`
	// CaptionPrefix is prepended to every post caption. Final caption
	// is `CaptionPrefix` + ` - ` + filename (or just the filename if
	// no prefix). Empty prefix means the caption is just the filename.
	CaptionPrefix string `json:"caption_prefix"`
	// MinJitterSeconds is the MINIMUM gap between consecutive scheduled
	// posts. Defaults to 10800 (3h) when zero.
	MinJitterSeconds int `json:"min_jitter_seconds"`
	// MaxJitterSeconds is the MAXIMUM gap between consecutive scheduled
	// posts. Defaults to 16200 (4.5h) when zero. Must be >= min.
	MaxJitterSeconds int `json:"max_jitter_seconds"`
	// PageToken is the Drive `nextPageToken` from the previous page's
	// response, for folders with more than 200 items. Empty for the
	// first page.
	PageToken string `json:"page_token"`
	// CursorScheduledAt is the timestamp from which the cumulative
	// stagger starts. SHOULD be set on subsequent pages (= the
	// last_scheduled_at from the previous response) so the random
	// 3-4.5h gap precedes the FIRST post on this page (preventing a
	// back-to-back cliff at page boundaries). Defaults to NOW() when
	// empty (acceptable for the first page only \u2014 a follow-up call
	// without cursor_scheduled_at collapses the gap).
	CursorScheduledAt *time.Time `json:"cursor_scheduled_at"`
}

// DriveBatchImportResponse returns the scheduled jobs.
type DriveBatchImportResponse struct {
	FolderID            string    `json:"folder_id"`
	ScheduledCount      int       `json:"scheduled_count"`
	TotalRuntimeSeconds int       `json:"total_runtime_estimate_seconds"`
	FirstPublishAt      time.Time `json:"first_publish_at"`
	LastScheduledAt     time.Time `json:"last_scheduled_at"`
	// NextPageToken is ALWAYS emitted (no `omitempty`) so callers can
	// reliably distinguish "got everything (token === "")" from "you
	// forgot to read it". The earlier omitempty hid the boundary case
	// where Drive returned `nextPageToken: ""` exactly.
	NextPageToken          string                 `json:"next_page_token"`
	Entries                []DriveBatchImportItem `json:"entries"`
	NeedsGoogleDriveAPIKey bool                   `json:"needs_google_drive_api_key,omitempty"`
	NeedsDriveAccount      bool                   `json:"needs_drive_account,omitempty"`
	// CursorClampedToNow is set to true when the supplied cursor_scheduled_at
	// was in the past (>1min) and the handler had to clamp it to NOW.
	// The SPA can surface this as a CTA ("looks like your cursor was
	// stale; this page re-anchored to now — verify the schedule on the
	// timeline view before publishing. The previous jobs are unaffected
	// — they're already queued"). omitempty so the happy path stays
	// quiet.
	CursorClampedToNow bool   `json:"cursor_clamped_to_now,omitempty"`
	Note               string `json:"note,omitempty"`
}

// DriveBatchImportItem describes one queued upload_job.
type DriveBatchImportItem struct {
	Index         int       `json:"index"`
	DriveFileID   string    `json:"drive_file_id"`
	Name          string    `json:"name"`
	MimeType      string    `json:"mime_type"`
	JobID         int64     `json:"job_id"`
	PublishAt     time.Time `json:"scheduled_at"`
	RelativeHours float64   `json:"relative_hours_from_now"`
}

// DriveBatchStatusResponse is the dashboard-friendly aggregate for a
// single Drive folder batch import. The endpoint is `/api/v1/media/
// import/drive/batch/status?folder_id=…` and is meant to be polled: a
// freshly-started import may lag 0-30s behind reality because the
// upload worker has its own tick.
//
// Status counts: 4 buckets (pending / processing / completed / failed).
// `processing` is included even though the worker usually completes
// quickly because operators want to see when the queue appears stuck
// (a job hung in `processing` for >5 minutes is worth alerting on).
//
// FirstPublishAt / LastPublishAt: MIN/MAX(scheduled_at) across every
// row scoped to (folder_id, user_id). Nil when the match set is empty
// OR every row has scheduled_at IS NULL (single-file legacy imports).
//
// Note is always set when the aggregation found zero rows so the SPA
// distinguishes an empty/cancelled batch from a fresh non-existent
// folder id.
type DriveBatchStatusResponse struct {
	FolderID        string     `json:"folder_id"`
	UserID          int64      `json:"user_id"`
	PendingCount    int        `json:"pending_count"`
	ProcessingCount int        `json:"processing_count"`
	CompletedCount  int        `json:"completed_count"`
	FailedCount     int        `json:"failed_count"`
	TotalCount      int        `json:"total_count"`
	FirstPublishAt  *time.Time `json:"first_publish_at,omitempty"`
	LastPublishAt   *time.Time `json:"last_publish_at,omitempty"`
	Note            string     `json:"note,omitempty"`
}

// driveFolderIDPatternRegex mirrors the service-level regex that
// Google Drive v3 uses for folder ids (URL-safe base64ish, ~33 chars).
// Duplicating it here means a malformed id is rejected with a clean
// 400 from the API layer BEFORE hitting Postgres — saves a trip and
// also closes the q-parameter-style injection vector in case future
// code ever interpolates folder_id into a raw query.
var driveFolderIDPatternRegex = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,100}$`)

// handleDriveBatchStatus implements
// GET /api/v1/media/import/drive/batch/status?folder_id=<id>.
//
// Authz: user_id comes from the JWT identity (requireUserID); the
// aggregation query further restricts by user_id so a stolen folder
// id from another tenant cannot be probed to enumerate the queue
// state. Workspace_id is intentionally NOT scope-restricted here —
// the same folder may legitimately exist under multiple workspaces
// (multi-tenant cron operator importing into several client
// workspaces); we aggregate every match belonging to the caller.
//
// Response is ALWAYS 200 OK for valid auth + valid folder_id shape —
// even when zero rows match. The dashboard polls aggressively and a
// 404 would surface as a red error banner between import calls;
// 200 + zero counts + a hint note is the better UX.
func (r *Router) handleDriveBatchStatus(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	folderID := strings.TrimSpace(req.URL.Query().Get("folder_id"))
	if folderID == "" {
		writeError(w, http.StatusUnprocessableEntity, "folder_id query parameter is required")
		return
	}
	if !driveFolderIDPatternRegex.MatchString(folderID) {
		writeError(w, http.StatusUnprocessableEntity, "folder_id must be 1–100 letters, digits, hyphens, or underscores")
		return
	}

	summary, err := r.uploadJobStore.AggregateByFolder(folderID, userID)
	if err != nil {
		slog.Warn("drive batch status: aggregation failed",
			"user_id", userID,
			"folder_id", folderID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not read folder status")
		return
	}

	resp := DriveBatchStatusResponse{
		FolderID:        folderID,
		UserID:          userID,
		PendingCount:    summary.PendingCount,
		ProcessingCount: summary.ProcessingCount,
		CompletedCount:  summary.CompletedCount,
		FailedCount:     summary.FailedCount,
		TotalCount:      summary.TotalCount,
		FirstPublishAt:  summary.FirstPublishAt,
		LastPublishAt:   summary.LastPublishAt,
	}
	if summary.TotalCount == 0 {
		resp.Note = "no batch with this folder_id for the current user (either the batch was issued under another account or no import has been started for this folder)"
	}

	writeJSON(w, http.StatusOK, resp)
}
