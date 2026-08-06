package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// handleListUploads is GET /api/v1/uploads (the cross-account list
// endpoint used by the dashboard widget when it doesn't know which
// account to drill into yet). Returns the same DTO shape as the
// per-account endpoint.
//
// Query params (all optional):
//
//	account_id (positive int) — restrict to matching targets
//	status     (upload_job_status enum value) — restrict to status
//	from, to   (RFC3339) — scheduled_at range filter
//	limit      (positive int) — default 200
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

	q := req.URL.Query()
	filter, err := parseUploadJobFilter(q, true /* allowEmpty */)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, rawCursor, pageErr := parseListPageWithBounds(q, 200, 500)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	cursorContext := listCursorFilterContext(q, "account_id", "status", "from", "to")
	cursorTime, cursorID, cursorNull, pageErr := decodeListCursorDetails(rawCursor, "uploads", cursorContext)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	if rawCursor != "" {
		filter.AfterID = parseCursorID(cursorID)
		if filter.AfterID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid list cursor")
			return
		}
		if !cursorNull {
			filter.AfterPublishAt = &cursorTime
		}
		filter.AfterPublishNull = cursorNull
	}
	filter.Limit = limit + 1

	jobs, listErr := r.uploadJobStore.ListByUser(identity.UserID(), filter)
	if listErr != nil {
		slog.Warn("uploads list failed", "user_id", identity.UserID(), "error", listErr)
		writeError(w, http.StatusInternalServerError, "could not list uploads")
		return
	}
	hasMore := len(jobs) > limit
	if hasMore {
		jobs = jobs[:limit]
	}
	items := make([]UploadJobDTO, 0, len(jobs))
	for i := range jobs {
		items = append(items, toUploadJobDTO(&jobs[i]))
	}
	response := map[string]interface{}{
		"uploads":  items,
		"count":    len(items),
		"has_more": hasMore,
	}
	if hasMore && len(jobs) > 0 {
		last := jobs[len(jobs)-1]
		var when time.Time
		if last.PublishAt != nil {
			when = *last.PublishAt
		}
		response["next_cursor"] = encodeListCursorForContext("uploads", cursorContext, when, fmt.Sprint(last.ID))
	}
	writeJSON(w, http.StatusOK, response)
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
	limit, rawCursor, pageErr := parseListPageWithBounds(q, uploadJobCalendarDefaultLimit, 500)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	cursorContext := listCursorFilterContext(q, "account_id", "status", "from", "to")
	cursorTime, cursorID, cursorNull, pageErr := decodeListCursorDetails(rawCursor, "uploads-account", cursorContext)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	if rawCursor != "" {
		filter.AfterID = parseCursorID(cursorID)
		if filter.AfterID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid list cursor")
			return
		}
		if !cursorNull {
			filter.AfterPublishAt = &cursorTime
		}
		filter.AfterPublishNull = cursorNull
	}
	filter.Limit = limit + 1

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

	hasMore := len(jobs) > limit
	if hasMore {
		jobs = jobs[:limit]
	}

	type UploadJobBucket struct {
		Date  string         `json:"date"` // YYYY-MM-DD UTC
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

	response := map[string]interface{}{
		"account_id":       accountID,
		"platform":         account.Platform,
		"has_more":         hasMore,
		"username":         account.Username,
		"count":            len(jobs),
		"pending_count":    pending,
		"processing_count": processing,
		"completed_count":  completed,
		"failed_count":     failed,
		"first_publish_at": firstScheduled,
		"last_publish_at":  lastScheduled,
		"by_day":           buckets,
	}
	if hasMore && len(jobs) > 0 {
		last := jobs[len(jobs)-1]
		var when time.Time
		if last.PublishAt != nil {
			when = *last.PublishAt
		}
		response["next_cursor"] = encodeListCursorForContext("uploads-account", cursorContext, when, fmt.Sprint(last.ID))
	}
	writeJSON(w, http.StatusOK, response)
}
