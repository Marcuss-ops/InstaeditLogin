package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

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
