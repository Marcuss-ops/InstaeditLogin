package api

import (
	"encoding/json"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// uploadJobCalendarDefaultLimit caps the per-account "calendar" list at
// 200 rows. Each upload_job is one row, so this is 200 distinct videos
// for one account. The frontend paginates beyond by passing to/from
// cursor bounds; the handler itself doesn't yet honour pagination
// cursors because the GIN index makes the per-account range cheap
// enough that the entire batch fits in one round-trip.
const uploadJobCalendarDefaultLimit = 200

// uploadJobMaxScheduleHorizonDays (Blocco #2 P0) is RETIRED — moved to
// r.scheduleLimits.PublishHorizonDays (env PUBLISH_HORIZON_DAYS,
// default 30). The hardcoded 60-day cap was removed because:
//   1. the batch V2 contract's 90-day cap (drive_batch_v2_handlers.go)
//      and the per-account reschedule cap diverged;
//   2. operators wanting a longer horizon had to rebuild + redeploy;
//   3. the value 60 = drive_batch_jitter_max(7) + safety was a stale
//      sentence-type comment, not a real semantic invariant.
// See WithScheduleLimits + (*Router).publishHorizonDays for the
// new accessor.
// uploadJobMaxScheduleHorizonDays = 60 — REMOVED (use r.publishHorizonDays()).

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
	ID          int64  `json:"id"`
	WorkspaceID int64  `json:"workspace_id"`
	Title       string `json:"title"`
	Caption     string `json:"caption,omitempty"`
	Status      string `json:"status"`
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
		"id":            a.ID,
		"workspace_id":  a.WorkspaceID,
		"title":         a.Title,
		"caption":       a.Caption,
		"status":        a.Status,
		"created_at":    a.CreatedAt,
		"targets":       a.Targets,
		"source_type":   a.SourceType,
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
