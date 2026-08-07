package api

// Post request types and repository error mapping.

import (
	"database/sql"

	"errors"

	"net/http"

	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type CreatePostContent struct {
	Title   string     `json:"title,omitempty"`
	Caption string     `json:"caption,omitempty"`
	Media   []MediaRef `json:"media,omitempty"`
}

type CreatePostTarget struct {
	PlatformAccountID int64 `json:"platform_account_id"`
}

type CreatePostRequest struct {
	WorkspaceID int64             `json:"workspace_id"`
	Content     CreatePostContent `json:"content"`
	// scheduled_at is the legacy alias. New callers should send
	// publish_at; both keys are accepted, publish_at wins if both
	// are set. The struct pair is preserved for one minor version;
	// P1#5 removes scheduled_at from the wire.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	// publish_at is the canonical user-facing cursor.
	PublishAt *time.Time         `json:"publish_at,omitempty"`
	Status    models.PostStatus  `json:"status,omitempty"`
	Targets   []CreatePostTarget `json:"targets"`
}

func (r CreatePostRequest) ResolvePublishAt() *time.Time {
	if r.PublishAt != nil {
		return r.PublishAt
	}
	return r.ScheduledAt
}

func publishAtJSON(publishAt *time.Time) map[string]interface{} {
	out := map[string]interface{}{
		"publish_at": publishAt,
	}
	if publishAt != nil {
		// Mirror as scheduled_at for back-compat.
		t := *publishAt
		out["scheduled_at"] = &t
	} else {
		out["scheduled_at"] = nil
	}
	return out
}

func mapRepoError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, repository.ErrPostUnauthorized):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, repository.ErrPostNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, repository.ErrPostTargetNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, repository.ErrIdempotencyConflict):
		return http.StatusConflict, err.Error()
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "post not found"
	default:
		return http.StatusInternalServerError, err.Error()
	}
}
