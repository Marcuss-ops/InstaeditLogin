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
	// Language is the OPTIONAL source language of the post content
	// (BCP-47 / ISO 639-1, e.g. "it", "en"). When a target channel
	// declares a DIFFERENT language in its account metadata, the
	// publish worker translates title + caption into the channel's
	// language before publishing (per-channel-language posting).
	// Persisted in post.Metadata under "source_language"; empty means
	// "unknown — the translator infers it from the text".
	Language string `json:"language,omitempty"`
	// TranslationEnabled controls per-channel translation. Nil preserves the
	// legacy default (enabled); false publishes the original text unchanged.
	TranslationEnabled *bool `json:"translation_enabled,omitempty"`
}

type CreatePostTarget struct {
	PlatformAccountID int64 `json:"platform_account_id"`
	// GroupID is an optional logical target. The HTTP layer expands it to
	// the group's current account membership before persisting post_targets.
	// Keeping the expansion server-side prevents callers from posting to a
	// group outside their workspace and keeps the worker contract unchanged.
	GroupID int64 `json:"group_id,omitempty"`
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
