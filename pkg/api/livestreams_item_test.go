package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestGetLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/"+ls.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp livestreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != ls.ID {
		t.Errorf("id: got %q", resp.ID)
	}
}

func TestGetLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/ls_missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func TestPatchLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"title":          "WWE News 24/7 — Nuovo",
		"privacy_status": "private",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil || updated.Title != "WWE News 24/7 — Nuovo" || updated.PrivacyStatus != "private" {
		t.Fatalf("updated row wrong: %+v", updated)
	}
	if updated.PlaybackMode != models.LivestreamPlaybackLoopContinuous {
		t.Errorf("untouched field should survive: %+v", updated)
	}
}

func TestPatchLivestream_MetadataFields(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"category":           "20",
		"made_for_kids":      true,
		"language":           "en",
		"dvr_enabled":        false,
		"auto_start":         true,
		"auto_stop":          false,
		"latency_preference": "ultraLow",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil {
		t.Fatal("updated row is nil")
	}
	if updated.Category != "20" || updated.Language != "en" || updated.LatencyPreference != "ultraLow" {
		t.Errorf("patched metadata wrong: %+v", updated)
	}
	if !updated.MadeForKids || !updated.AutoStart || updated.AutoStop || updated.DVREnabled {
		t.Errorf("patched booleans wrong: %+v", updated)
	}
	// Untouched thumbnail survives.
	if updated.ThumbnailMediaID == nil || *updated.ThumbnailMediaID != "thumb-123" {
		t.Errorf("untouched thumbnail should survive: %+v", updated)
	}
}

func TestPatchLivestream_ClearsThumbnail(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	// Empty string clears the cover (same semantics as scheduled_start_at).
	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"thumbnail_media_id": "",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated.ThumbnailMediaID != nil {
		t.Errorf("thumbnail_media_id should be cleared, got %v", updated.ThumbnailMediaID)
	}
}

func TestPatchLivestream_RejectsWorkerOwnedState(t *testing.T) {
	ls := livestreamFixtureResponse()
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"desired_state": "live",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/ls_missing", map[string]any{
		"title": "x",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func TestDeleteLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	deleted := false
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
		deleteFn: func(ctx context.Context, id string) error {
			deleted = true
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodDelete, "/api/v1/livestreams/"+ls.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if !deleted {
		t.Error("delete was not called")
	}
}

func TestDeleteLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodDelete, "/api/v1/livestreams/ls_missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
