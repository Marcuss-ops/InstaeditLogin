package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// uploadJobCalendarDefaultLimit caps the per-account "calendar" list at
// 200 rows. Each upload_job is one row, so this is 200 distinct videos
// for one account. The frontend paginates beyond by passing to/from
// cursor bounds; the handler itself doesn't yet honour pagination
// cursors because the GIN index makes the per-account range cheap
// enough that the entire batch fits in one round-trip.
const uploadJobCalendarDefaultLimit = 200

// uploadJobMaxScheduleHorizonDays caps how far in the future a user
// can move a scheduled video via drag-drop. 60 days matches the
// drive_batch_jitter_max_seconds (7 days) plus a generous safety
// margin so the dashboard never lets a user accidentally park a post
// 6 months out (which would silently reduce the "the worker will
// publish this" inspection frequency).
const uploadJobMaxScheduleHorizonDays = 60

// UploadJobDTO is the wire shape returned to the SPA. We deliberately
// do NOT return the full models.UploadJob struct (it leaks user_id,
// drive_account_id, error_message, and the targets raw int64 list
// only meaningful as a join key). The 9 fields below are what the
// dashboard "Programmati" view + per-account calendar need.
//
// targets is kept (the SPA uses it to determine which platforms an
// upload covers — useful for the multi-account "this video publishes
// to FB + YT simultaneously" hint).
//
// P1#4 — ScheduledAt is replaced by PublishAt (canonical) AND we
// surface both keys on the wire. The MarshalJSON below emits
// publish_at (canonical) AND scheduled_at (legacy alias mirrored
// from publish_at) so legacy SPA clients reading scheduled_at
// continue to work until they migrate.
type UploadJobDTO struct {
	ID          int64      `json:"id"`
	WorkspaceID int64      `json:"workspace_id"`
	Title       string     `json:"title"`
	Caption     string     `json:"caption,omitempty"`
	Status      string     `json:"status"`
	// P1#4 — canonical user-facing publish time. NULL for
	// never-scheduled single-file flows (publish immediately).
	PublishAt  *time.Time `json:"publish_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Targets    []int64    `json:"targets"`
	SourceType string     `json:"source_type"`
	Error      string     `json:"error_message,omitempty"`
}

// MarshalJSON emits both publish_at (canonical) and scheduled_at
// (legacy alias mirrored from publish_at) so legacy SPA clients
// continue to render the calendar until they migrate. Pointer
// created_at avoids the JSON null-vs-absent distinction that some
// older flex parsers mishandle.
func (d UploadJobDTO) MarshalJSON() ([]byte, error) {
	type alias UploadJobDTO // avoid recursive MarshalJSON call
	a := alias(d)
	base := map[string]interface{}{
		"id":           a.ID,
		"workspace_id": a.WorkspaceID,
		"title":        a.Title,
		"caption":      a.Caption,
		"status":       a.Status,
		"created_at":   a.CreatedAt,
		"targets":      a.Targets,
		"source_type":  a.SourceType,
		"error_message": a.Error,
		"publish_at":    a.PublishAt,
	}
	if a.PublishAt != nil {
		t := *a.PublishAt
		base["scheduled_at"] = &t
	} else {
		base["scheduled_at"] = nil
	}
	return json.Marshal(base)
}

func toUploadJobDTO(j *models.UploadJob) UploadJobDTO {
	targets := j.Targets
	if targets == nil {
		targets = []int64{}
	}
	return UploadJobDTO{
		ID:          j.ID,
		WorkspaceID: j.WorkspaceID,
		Title:       j.Title,
		Caption:     j.Caption,
		Status:      string(j.Status),
		PublishAt:   j.PublishAt,
		CreatedAt:   j.CreatedAt,
		Targets:     targets,
		SourceType:  string(j.SourceType),
		Error:       j.ErrorMessage,
	}
}

// --- Handlers ---

// handleListUploads is GET /api/v1/uploads (the cross-account list
// endpoint used by the dashboard widget when it doesn't know which
// account to drill into yet). Returns the same DTO shape as the
// per-account endpoint.
//
// Query params (all optional):
//   account_id (positive int) — restrict to matching targets
//   status     (upload_job_status enum value) — restrict to status
//   from, to   (RFC3339) — scheduled_at range filter
//   limit      (positive int) — default 200
func (r *Router) handleListUploads(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	filter, err := parseUploadJobFilter(req.URL.Query(), true /* allowEmpty */)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobs, listErr := r.uploadJobStore.ListByUser(identity.UserID(), filter)
	if listErr != nil {
		slog.Warn("uploads list failed", "user_id", identity.UserID(), "error", listErr)
		writeError(w, http.StatusInternalServerError, "could not list uploads")
		return
	}
	items := make([]UploadJobDTO, 0, len(jobs))
	for i := range jobs {
		items = append(items, toUploadJobDTO(&jobs[i]))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"uploads": items,
		"count":   len(items),
	})
}

// handleListUploadsByAccount is GET /api/v1/uploads/by-account backing
// the per-account calendar view in the SPA. The handler buckets rows
// by UTC date so the calendar component renders directly without a
// second client-side groupBy pass.
//
// Returns 404 when the account id doesn't belong to the caller (vs.
// 200-with-empty-list for "account exists but has no scheduled
// uploads"). The 404 short-circuits clear "stale link" cases (user
// clicks a bookmarked calendar URL after disconnecting the account);
// the SPA uses 200-empty as the deliberate "calendar is empty"
// happy-path signal.
func (r *Router) handleListUploadsByAccount(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	q := req.URL.Query()
	accountID, err := parseInt64Query(q, "account_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "account_id query parameter is required and must be a positive integer")
		return
	}

	account, err := r.userRepo.FindPlatformAccountByID(accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load account")
		return
	}
	if account == nil || account.UserID != identity.UserID() {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	filter, err := parseUploadJobFilter(q, false /* allowEmpty */)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.AccountID = &accountID
	if filter.Limit == 0 {
		filter.Limit = uploadJobCalendarDefaultLimit
	}

	jobs, listErr := r.uploadJobStore.ListByUser(identity.UserID(), filter)
	if listErr != nil {
		slog.Warn("uploads by-account failed",
			"user_id", identity.UserID(),
			"account_id", accountID,
			"error", listErr,
		)
		writeError(w, http.StatusInternalServerError, "could not list uploads")
		return
	}

	type UploadJobBucket struct {
		Date  string         `json:"date"`  // YYYY-MM-DD UTC
		Jobs  []UploadJobDTO `json:"jobs"`
		Count int            `json:"count"`
	}

	bucketByDate := map[string]*UploadJobBucket{}
	var pending, processing, completed, failed int
	var firstScheduled, lastScheduled *time.Time
	for i := range jobs {
		dto := toUploadJobDTO(&jobs[i])
		// P1#4 — collapse both old `processing` (legacy state) + new
		// `ingest_completed` (Drive → S3 done, awaiting publish_at)
		// into the existing processing_count bucket. Dashboard
		// badges had a single "in-flight" badge before; the rename
		// preserves the same semantic so user-facing render is
		// unchanged. ingest_completed rows with a future publish_at
		// CANNOT contribute to processing_count because the publish
		// pool hasn't claimed them — those rows surface in
		// ready_to_publish_count (the new badge).
		switch models.UploadJobStatus(dto.Status) {
		case models.UploadJobStatusPending:
			pending++
		case models.UploadJobStatusProcessing,
			models.UploadJobStatusIngestCompleted:
			processing++
		case models.UploadJobStatusPublishCompleted,
			models.UploadJobStatusCompleted:
			completed++
		case models.UploadJobStatusFailed:
			failed++
		}
		if dto.PublishAt != nil {
			if firstScheduled == nil || dto.PublishAt.Before(*firstScheduled) {
				t := *dto.PublishAt
				firstScheduled = &t
			}
			if lastScheduled == nil || dto.PublishAt.After(*lastScheduled) {
				t := *dto.PublishAt
				lastScheduled = &t
			}
			key := dto.PublishAt.UTC().Format("2006-01-02")
			b, ok := bucketByDate[key]
			if !ok {
				b = &UploadJobBucket{Date: key, Jobs: []UploadJobDTO{}}
				bucketByDate[key] = b
			}
			b.Jobs = append(b.Jobs, dto)
			b.Count = len(b.Jobs)
		}
	}

	buckets := make([]UploadJobBucket, 0, len(bucketByDate))
	for _, b := range bucketByDate {
		buckets = append(buckets, *b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Date < buckets[j].Date })

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"account_id":       accountID,
		"platform":         account.Platform,
		"username":         account.Username,
		"count":            len(jobs),
		"pending_count":    pending,
		"processing_count": processing,
		"completed_count":  completed,
		"failed_count":     failed,
		"first_publish_at": firstScheduled,
		"last_publish_at":  lastScheduled,
		"by_day":           buckets,
	})
}

// UploadJobCountDTO is the wire shape for one entry in the dashboard
// "Programmati" widget's per-account aggregate. The dashboard hits
// this instead of fetching 200+ rows and bucketing them in JS.
type UploadJobCountDTO struct {
	AccountID     int64      `json:"account_id"`
	Count         int        `json:"count"`
	NextPublishAt *time.Time `json:"next_publish_at,omitempty"`
}

// handleUploadCounts backs GET /api/v1/uploads/counts. Returns the
// per-target rollup (count + earliest scheduled_at) driven by
// PendingCountsByAccount's single GROUP BY query. The dashboard
// widget renders THIS payload — no client-side N^2 bucketing, no
// 200-row cap hiding rows past the calendar view's limit.
//
// Authn is the JWT (no workspace scope; the WHERE is by user_id).
// When the user has no pending uploads at all, the handler returns
// an empty array so the SPA's iteration is unconditional.
func (r *Router) handleUploadCounts(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	counts, err := r.uploadJobStore.PendingCountsByAccount(identity.UserID())
	if err != nil {
		slog.Warn("uploads counts failed", "user_id", identity.UserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "could not aggregate uploads")
		return
	}
	items := make([]UploadJobCountDTO, 0, len(counts))
	for _, c := range counts {
		items = append(items, UploadJobCountDTO{
			AccountID:     c.AccountID,
			Count:         c.Count,
			NextPublishAt: c.NextPublishAt,
		})
	}
	// total_uploads is the DISTINCT row count so multi-target uploads
	// (e.g. one drive_batch row targeting FB+IG) count once on the
	// dashboard's "Pending uploads" stat instead of twice. SUM over
	// per-target counts would over-count by a factor of len(targets).
	totalUploads, err := r.uploadJobStore.PendingDistinctCount(identity.UserID())
	if err != nil {
		slog.Warn("uploads distinct count failed", "user_id", identity.UserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "could not aggregate uploads")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"counts":        items,
		"total_uploads": totalUploads,
		"total_targets": len(items),
	})
}

// rescheduleUploadRequest is the body for PATCH /api/v1/uploads/{id}/reschedule.
// We accept only the new publish_at — title, caption, and targets
// remain unchanged (a future "edit" endpoint can fan those out).
//
// P1#4 — publish_at is canonical; scheduled_at is the legacy alias
// kept for one-minor-version back-compat. If both keys are present
// and parseable, publish_at wins (consistent with
// CreatePostRequest.ResolvePublishAt).
type rescheduleUploadRequest struct {
	// publish_at is canonical.
	PublishAt *time.Time `json:"publish_at,omitempty"`
	// scheduled_at is the legacy alias.
	ScheduledAt time.Time `json:"scheduled_at"`
}

// resolvePublishAt returns the canonical cursor with publish_at
// precedence.
func (r rescheduleUploadRequest) resolvePublishAt() time.Time {
	if r.PublishAt != nil && !r.PublishAt.IsZero() {
		return *r.PublishAt
	}
	return r.ScheduledAt
}

// handleRescheduleUpload moves a pending upload_job to a new
// publish_at. The dashboard calendar uses this on drag-drop. Returns
// 200 with the updated row.
func (r *Router) handleRescheduleUpload(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	jobID, err := parseInt64PathParam(req, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var body rescheduleUploadRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// P1#4 — canonical publish_at wins; scheduled_at falls back.
	newPublishAt := body.resolvePublishAt()
	if newPublishAt.IsZero() {
		writeError(w, http.StatusBadRequest, "publish_at (or scheduled_at alias) is required (RFC3339)")
		return
	}
	if newPublishAt.Before(time.Now().Add(-1 * time.Minute)) {
		// Past dates collapse the anti-pattern-detection jitter: a
		// video "scheduled for yesterday" publishes immediately on
		// the next worker tick. The publish-flow ALREADY supports
		// that (manual "Publish now" path), so dashboard reschedule
		// intentionally rejects past dates and forces the user to
		// use Publish-now if that's what they want.
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("publish_at must be in the future (use /api/v1/posts/{id}/publish to publish immediately instead) [got %s]", newPublishAt.Format(time.RFC3339)))
		return
	}
	// 5-second floor: a drag-drop that resolves to "literally now"
	// would race the publish_worker's next tick and surface as a
	// "completed before the SPA optimistic update" race. Require a
	// few seconds of headroom so the optimistic UI sees the chip in
	// its new bucket before the worker picks it up.
	if newPublishAt.Before(time.Now().Add(5 * time.Second)) {
		writeError(w, http.StatusBadRequest, "publish_at must be at least 5 seconds in the future")
		return
	}
	maxHorizon := time.Now().Add(time.Duration(uploadJobMaxScheduleHorizonDays) * 24 * time.Hour)
	if newPublishAt.After(maxHorizon) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("publish_at must be within %d days from now", uploadJobMaxScheduleHorizonDays),
		)
		return
	}

	job, err := r.uploadJobStore.Reschedule(jobID, identity.UserID(), newPublishAt)
	if err != nil {
		if errors.Is(err, repository.ErrUploadJobNotFound) {
			writeError(w, http.StatusNotFound,
				"upload job not found or no longer pending (the worker may have already started publishing — refresh and try again)",
			)
			return
		}
		slog.Warn("uploads reschedule failed",
			"user_id", identity.UserID(),
			"job_id", jobID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not reschedule upload")
		return
	}
	writeJSON(w, http.StatusOK, toUploadJobDTO(&job))
}

// handleCancelUpload deletes a pending upload_job. The dashboard
// calendar uses this on the "trash" button. Returns 204.
//
// State-machine contract mirrors Reschedule: only pending rows can be
// cancelled. Once the worker has claimed the row (processing) or it's
// terminal (completed/failed), the DELETE matches zero rows and the
// handler returns 404 — same UX surface as Reschedule (no info leak).
func (r *Router) handleCancelUpload(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	jobID, err := parseInt64PathParam(req, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = r.uploadJobStore.Cancel(jobID, identity.UserID())
	if err != nil {
		if errors.Is(err, repository.ErrUploadJobNotFound) {
			writeError(w, http.StatusNotFound, "upload job not found or no longer pending")
			return
		}
		slog.Warn("uploads cancel failed",
			"user_id", identity.UserID(),
			"job_id", jobID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not cancel upload")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

// parseUploadJobFilter validates the optional query params shared
// between /uploads and /uploads/by-account. allowEmpty toggles
// whether `account_id` is required (by-account → required; list → optional).
func parseUploadJobFilter(q map[string][]string, allowEmpty bool) (repository.UploadJobListFilter, error) {
	var filter repository.UploadJobListFilter

	if !allowEmpty {
		// by-account endpoint makes account_id mandatory.
		if v, ok := q["account_id"]; ok && len(v) > 0 && v[0] != "" {
			id, err := strconv.ParseInt(strings.TrimSpace(v[0]), 10, 64)
			if err != nil || id <= 0 {
				return filter, errors.New("account_id must be a positive integer")
			}
			filter.AccountID = &id
		} else {
			return filter, errors.New("account_id query parameter is required")
		}
	} else {
		if v, ok := q["account_id"]; ok && len(v) > 0 && v[0] != "" {
			id, err := strconv.ParseInt(strings.TrimSpace(v[0]), 10, 64)
			if err != nil || id <= 0 {
				return filter, errors.New("account_id must be a positive integer")
			}
			filter.AccountID = &id
		}
	}

	if v, ok := q["status"]; ok && len(v) > 0 && v[0] != "" {
		s := models.UploadJobStatus(v[0])
		// P1#4 — accept the new ingest_completed + publish_completed
		// names (canonical post-rename) AND the legacy aliases
		// (ready_to_publish, completed) so a SPA mid-migration
		// doesn't 400-filter. The repository's enum stores the
		// canonical case-insensitive string; the rewrite SQL in 049c
		// UPDATE'd any pre-existing rows out of the legacy values.
		switch s {
		case models.UploadJobStatusPending,
			models.UploadJobStatusProcessing,
			models.UploadJobStatusCompleted,
			models.UploadJobStatusFailed,
			models.UploadJobStatusLeased,
			models.UploadJobStatusRetryWait,
			models.UploadJobStatusDeadLetter,
			models.UploadJobStatusCancelled,
			models.UploadJobStatusIngestCompleted,
			models.UploadJobStatusPublishCompleted,
			models.UploadJobStatusReadyToPublish:
			filter.Status = &s
		default:
			return filter, errors.New("status must be one of: pending, processing, completed, failed, leased, retry_wait, dead_letter, cancelled, ingest_completed, publish_completed")
		}
	}

	if v, ok := q["from"]; ok && len(v) > 0 && v[0] != "" {
		t, err := time.Parse(time.RFC3339, v[0])
		if err != nil {
			return filter, errors.New("from must be RFC3339 (e.g. 2026-07-17T00:00:00Z)")
		}
		filter.From = &t
	}
	if v, ok := q["to"]; ok && len(v) > 0 && v[0] != "" {
		t, err := time.Parse(time.RFC3339, v[0])
		if err != nil {
			return filter, errors.New("to must be RFC3339 (e.g. 2026-07-24T00:00:00Z)")
		}
		filter.To = &t
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return filter, errors.New("to must be >= from")
	}

	if v, ok := q["limit"]; ok && len(v) > 0 && v[0] != "" {
		lim, err := strconv.Atoi(v[0])
		if err != nil || lim <= 0 {
			return filter, errors.New("limit must be a positive integer")
		}
		filter.Limit = lim
	}
	return filter, nil
}

func parseInt64Query(q map[string][]string, key string) (int64, error) {
	v, ok := q[key]
	if !ok || len(v) == 0 || strings.TrimSpace(v[0]) == "" {
		return 0, fmt.Errorf("%s query parameter is required", key)
	}
	return strconv.ParseInt(strings.TrimSpace(v[0]), 10, 64)
}

func parseInt64PathParam(req *http.Request, key string) (int64, error) {
	raw := chi.URLParam(req, key)
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s path parameter must be a positive integer", key)
	}
	return id, nil
}
