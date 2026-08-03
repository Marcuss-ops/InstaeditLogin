package api

import (
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MediaLibraryItem is one row of GET /api/v1/media — the Media Library
// row the live wizard's step 3 renders. The probe fields
// (duration/resolution/FPS/audio/codecs) stay null until the upload
// worker probes the asset (migration 092); live_compatibility is
// derived server-side so clients never re-implement the profile check.
type MediaLibraryItem struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	// PreviewURL is a short-lived presigned GET URL (15 min) minted
	// per row so the wizard can render <video preload="metadata">.
	PreviewURL string `json:"preview_url,omitempty"`

	// Probe fields — nil until probed.
	DurationSeconds *float64   `json:"duration_seconds,omitempty"`
	Width           *int       `json:"width,omitempty"`
	Height          *int       `json:"height,omitempty"`
	FPS             *float64   `json:"fps,omitempty"`
	HasAudio        *bool      `json:"has_audio,omitempty"`
	VideoCodec      string     `json:"video_codec,omitempty"`
	AudioCodec      string     `json:"audio_codec,omitempty"`
	ProbedAt        *time.Time `json:"probed_at,omitempty"`

	// LiveCompatibility: "ready" | "needs_normalization" | "unknown".
	// "unknown" = never probed (compatibility can't be asserted);
	// "needs_normalization" = probed but off-profile (the file must
	// be normalised before it can feed a live encoder).
	LiveCompatibility string `json:"live_compatibility"`
}

const (
	liveCompatReady              = "ready"
	liveCompatNeedsNormalization = "needs_normalization"
	liveCompatUnknown            = "unknown"
)

// mediaLibraryPreviewTTL is how long each row's presigned preview URL
// stays valid. Browsers fetch metadata lazily, so 15 minutes covers a
// long wizard session without leaking signed URLs forever.
const mediaLibraryPreviewTTL = 15 * time.Minute

// handleListMediaAssets (GET /api/v1/media, protected) returns the
// caller's ready, non-expired media assets newest-first with their
// ffprobe metadata + a server-derived live-compatibility flag. Powers
// the live wizard's step 3 Media Library picker. Optional ?limit=
// (default 100, max 500).
func (r *Router) handleListMediaAssets(w http.ResponseWriter, req *http.Request) {
	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	limit := 100
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}

	assets, err := r.mediaStore.ListReadyByUser(req.Context(), userID, limit)
	if err != nil {
		logAndError(w, req, "failed to list media library", err, "user_id", userID)
		return
	}
	items := make([]MediaLibraryItem, 0, len(assets))
	for i := range assets {
		asset := &assets[i]
		item := MediaLibraryItem{
			ID:                asset.ID,
			Filename:          mediaAssetFilename(asset.UploadKey),
			ContentType:       asset.ContentType,
			SizeBytes:         asset.SizeBytes,
			CreatedAt:         asset.CreatedAt,
			DurationSeconds:   asset.DurationSeconds,
			Width:             asset.Width,
			Height:            asset.Height,
			FPS:               asset.FPS,
			HasAudio:          asset.HasAudio,
			VideoCodec:        asset.VideoCodec,
			AudioCodec:        asset.AudioCodec,
			ProbedAt:          asset.ProbedAt,
			LiveCompatibility: mediaLiveCompatibility(asset),
		}
		if url, urlErr := r.storageProvider.GetObject(req.Context(), asset.UploadKey, mediaLibraryPreviewTTL); urlErr == nil {
			item.PreviewURL = url
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// mediaAssetFilename recovers the display name from the S3 object key
// ("uploads/{userID}/{uuid}_{sanitized}" → "{sanitized}"). The uuid
// token is 36 hex/dash chars and NEVER contains an underscore, so the
// FIRST underscore after path.Base is the separator — a sanitized
// filename may itself contain underscores (e.g. "my_clip.mp4") and
// must survive intact. Keys without the uuid prefix (legacy shapes)
// fall back to the base name.
func mediaAssetFilename(uploadKey string) string {
	if uploadKey == "" {
		return ""
	}
	base := path.Base(uploadKey)
	parts := strings.SplitN(base, "_", 2)
	if len(parts) == 2 && len(parts[0]) >= 32 {
		return parts[1]
	}
	return base
}

// mediaLiveCompatibility derives the wizard badge from the asset's
// probe columns. Unprobed assets are "unknown"; probed assets either
// match one of the canonical live profiles ("ready") or need
// normalization before they can feed a live encoder.
func mediaLiveCompatibility(asset *models.MediaAsset) string {
	if asset == nil || asset.ProbedAt == nil {
		return liveCompatUnknown
	}
	if asset.Probe().LiveCompatible() {
		return liveCompatReady
	}
	return liveCompatNeedsNormalization
}
