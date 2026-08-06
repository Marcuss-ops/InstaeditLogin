package api

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/go-chi/chi/v5"
)

// MediaLibraryListItem is the deliberately small row DTO returned by
// GET /api/v1/media. Probe metadata and signed object URLs are loaded on
// demand by GET /api/v1/media/{id}.
type MediaLibraryListItem struct {
	ID                string    `json:"id"`
	Filename          string    `json:"filename"`
	ContentType       string    `json:"content_type"`
	SizeBytes         int64     `json:"size_bytes"`
	CreatedAt         time.Time `json:"created_at"`
	LiveCompatibility string    `json:"live_compatibility"`
}

// MediaLibraryDetail is returned by GET /api/v1/media/{id}. The signed
// preview URL is minted only for this explicitly requested asset.
type MediaLibraryDetail struct {
	MediaLibraryListItem
	PreviewURL string `json:"preview_url,omitempty"`

	DurationSeconds *float64   `json:"duration_seconds,omitempty"`
	Width           *int       `json:"width,omitempty"`
	Height          *int       `json:"height,omitempty"`
	FPS             *float64   `json:"fps,omitempty"`
	HasAudio        *bool      `json:"has_audio,omitempty"`
	VideoCodec      string     `json:"video_codec,omitempty"`
	AudioCodec      string     `json:"audio_codec,omitempty"`
	ProbedAt        *time.Time `json:"probed_at,omitempty"`
}

// MediaLibraryItem remains available for existing API tests and internal
// helpers that model the previous complete row shape.
type MediaLibraryItem struct {
	ID                string     `json:"id"`
	Filename          string     `json:"filename"`
	ContentType       string     `json:"content_type"`
	SizeBytes         int64      `json:"size_bytes"`
	CreatedAt         time.Time  `json:"created_at"`
	PreviewURL        string     `json:"preview_url,omitempty"`
	DurationSeconds   *float64   `json:"duration_seconds,omitempty"`
	Width             *int       `json:"width,omitempty"`
	Height            *int       `json:"height,omitempty"`
	FPS               *float64   `json:"fps,omitempty"`
	HasAudio          *bool      `json:"has_audio,omitempty"`
	VideoCodec        string     `json:"video_codec,omitempty"`
	AudioCodec        string     `json:"audio_codec,omitempty"`
	ProbedAt          *time.Time `json:"probed_at,omitempty"`
	LiveCompatibility string     `json:"live_compatibility"`
}

const (
	liveCompatReady              = "ready"
	liveCompatNeedsNormalization = "needs_normalization"
	liveCompatUnknown            = "unknown"

	// The server cache is shorter than the 15-minute URL validity period,
	// leaving enough time for a browser to finish a metadata request.
	mediaLibraryPreviewCacheTTL = 5 * time.Minute
	mediaLibraryPreviewTTL      = 15 * time.Minute
)

type mediaPreviewCacheEntry struct {
	url       string
	expiresAt time.Time
}

// handleListMediaAssets (GET /api/v1/media, protected) returns only the
// fields needed to render a list. It intentionally performs no storage
// signing and no per-row detail expansion.
func (r *Router) handleListMediaAssets(w http.ResponseWriter, req *http.Request) {
	if r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	limit, rawCursor, err := parseListPageWithBounds(req.URL.Query(), 100, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := ""
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "media", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull {
		writeError(w, http.StatusBadRequest, "invalid list cursor: media cursor timestamp is required")
		return
	}

	var assets []models.MediaAsset
	hasMore := false
	if paged, ok := r.mediaStore.(interface {
		ListReadyByUserPage(context.Context, int64, *time.Time, string, int) ([]models.MediaAsset, bool, error)
	}); ok {
		var afterTime *time.Time
		if rawCursor != "" {
			afterTime = &cursorTime
		}
		assets, hasMore, err = paged.ListReadyByUserPage(req.Context(), userID, afterTime, cursorID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this media store")
			return
		}
		assets, err = r.mediaStore.ListReadyByUser(req.Context(), userID, limit)
		if len(assets) > limit {
			hasMore = true
			assets = assets[:limit]
		}
	}
	if err != nil {
		logAndError(w, req, "failed to list media library", err, "user_id", userID)
		return
	}

	items := make([]MediaLibraryListItem, 0, len(assets))
	for i := range assets {
		asset := &assets[i]
		items = append(items, MediaLibraryListItem{
			ID:                asset.ID,
			Filename:          mediaAssetFilename(asset.UploadKey),
			ContentType:       asset.ContentType,
			SizeBytes:         asset.SizeBytes,
			CreatedAt:         asset.CreatedAt,
			LiveCompatibility: mediaLiveCompatibility(asset),
		})
	}
	response := map[string]any{"items": items, "has_more": hasMore}
	if hasMore && len(assets) > 0 {
		last := assets[len(assets)-1]
		response["next_cursor"] = encodeListCursorForContext("media", cursorContext, last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

// handleGetMediaAsset (GET /api/v1/media/{id}, protected) returns the
// detail DTO and mints a signed URL only for this requested asset.
func (r *Router) handleGetMediaAsset(w http.ResponseWriter, req *http.Request) {
	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(chi.URLParam(req, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "media id is required")
		return
	}
	asset, err := r.mediaStore.FindByID(id)
	if err != nil {
		logAndError(w, req, "failed to find media asset", err, "asset_id", id)
		return
	}
	if asset == nil || asset.UserID != userID || asset.Status != models.MediaAssetStatusReady || time.Now().After(asset.ExpiresAt) {
		writeError(w, http.StatusNotFound, "media asset not found")
		return
	}

	item := MediaLibraryDetail{
		MediaLibraryListItem: MediaLibraryListItem{
			ID:                asset.ID,
			Filename:          mediaAssetFilename(asset.UploadKey),
			ContentType:       asset.ContentType,
			SizeBytes:         asset.SizeBytes,
			CreatedAt:         asset.CreatedAt,
			LiveCompatibility: mediaLiveCompatibility(asset),
		},
		DurationSeconds: asset.DurationSeconds,
		Width:           asset.Width,
		Height:          asset.Height,
		FPS:             asset.FPS,
		HasAudio:        asset.HasAudio,
		VideoCodec:      asset.VideoCodec,
		AudioCodec:      asset.AudioCodec,
		ProbedAt:        asset.ProbedAt,
	}

	if url, cached := r.mediaPreviewURL(id); cached {
		item.PreviewURL = url
	} else if url, urlErr := r.storageProvider.GetObject(req.Context(), asset.UploadKey, mediaLibraryPreviewTTL); urlErr == nil {
		item.PreviewURL = url
		r.storeMediaPreviewURL(id, url)
	} else {
		logAndError(w, req, "failed to sign media preview", urlErr, "asset_id", id)
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) mediaPreviewURL(id string) (string, bool) {
	r.mediaPreviewCacheMu.Lock()
	defer r.mediaPreviewCacheMu.Unlock()
	entry, ok := r.mediaPreviewCache[id]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.url, true
}

func (r *Router) storeMediaPreviewURL(id, url string) {
	r.mediaPreviewCacheMu.Lock()
	defer r.mediaPreviewCacheMu.Unlock()
	if r.mediaPreviewCache == nil {
		r.mediaPreviewCache = make(map[string]mediaPreviewCacheEntry)
	}
	r.mediaPreviewCache[id] = mediaPreviewCacheEntry{url: url, expiresAt: time.Now().Add(mediaLibraryPreviewCacheTTL)}
}

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

func mediaLiveCompatibility(asset *models.MediaAsset) string {
	if asset == nil || asset.ProbedAt == nil {
		return liveCompatUnknown
	}
	if asset.Probe().LiveCompatible() {
		return liveCompatReady
	}
	return liveCompatNeedsNormalization
}
