package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// mediaResolveTTL is how long each presigned GET URL minted by the media
// resolver stays valid. Long enough for the editor to paint the canvas,
// short enough that leaked URLs don't outlive the session.
const mediaResolveTTL = 15 * time.Minute

// maxResolveMediaIDs caps the number of media references one snapshot
// can resolve in a single request. Canvas snapshots rarely exceed a
// handful of images; the cap prevents a pathological snapshot from
// forcing a huge IN query.
const maxResolveMediaIDs = 100

// thumbnailMediaResolveRequest is the body of
// POST /api/v1/thumbnail-projects/{id}/media/resolve.
type thumbnailMediaResolveRequest struct {
	MediaIDs []string `json:"media_ids"`
}

// thumbnailMediaResolveItem is one resolved media reference: a
// short-lived presigned GET URL plus the metadata the editor needs to
// size/display the object. Cross-workspace, not-ready, expired, or
// missing assets are never returned.
type thumbnailMediaResolveItem struct {
	MediaID     string    `json:"media_id"`
	URL         string    `json:"url"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type thumbnailMediaResolveResponse struct {
	Items []thumbnailMediaResolveItem `json:"items"`
}

// handleResolveThumbnailProjectMedia resolves the media references of a
// project snapshot from the server (never local blobs): it looks up the
// requested media_ids inside the caller's workspace, checks ready +
// non-expired, and mints a presigned GET URL per visible asset.
//
// No YouTube/OAuth dependency exists on this path. Cross-workspace
// assets are blocked twice: the workspace ownership gate at the top and
// the repository's workspace-membership filter, so a foreign asset is
// indistinguishable from a missing one (no existence leak).
func (r *Router) handleResolveThumbnailProjectMedia(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil || r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail media resolution not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleViewer); !ok {
		return
	}
	if _, ok := parseThumbnailProjectID(w, req); !ok {
		return
	}
	var body thumbnailMediaResolveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail media resolve body")
		return
	}
	if len(body.MediaIDs) == 0 {
		writeError(w, http.StatusBadRequest, "media_ids must contain at least one id")
		return
	}
	if len(body.MediaIDs) > maxResolveMediaIDs {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("media_ids must not exceed %d ids", maxResolveMediaIDs))
		return
	}
	// Deduplicate preserving order; reject non-UUID references (the
	// repository casts to uuid[], so a bad id would otherwise surface
	// as an opaque SQL error).
	seen := make(map[string]bool, len(body.MediaIDs))
	ids := make([]string, 0, len(body.MediaIDs))
	for _, raw := range body.MediaIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			writeError(w, http.StatusUnprocessableEntity, "media_ids entries must not be empty")
			return
		}
		if _, err := uuid.Parse(id); err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("media_ids entry %q is not a UUID", id))
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	assets, err := r.mediaStore.ListVisibleInWorkspace(req.Context(), workspaceID, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve thumbnail media: "+err.Error())
		return
	}
	items := make([]thumbnailMediaResolveItem, 0, len(assets))
	for i := range assets {
		asset := &assets[i]
		cacheKey := asset.ID + "\x00" + asset.UploadKey
		url, cached := r.mediaResolveURL(cacheKey)
		if !cached {
			url, err = r.storageProvider.GetObject(req.Context(), asset.UploadKey, mediaResolveTTL)
			if err != nil {
				// A signing failure for one asset must not fail the whole
				// resolve; the editor treats the object as unresolved and
				// the user can retry. Mirrors handleListMediaAssets.
				continue
			}
			r.storeMediaResolveURL(cacheKey, url)
		}
		items = append(items, thumbnailMediaResolveItem{
			MediaID:     asset.ID,
			URL:         url,
			ContentType: asset.ContentType,
			SizeBytes:   asset.SizeBytes,
			CreatedAt:   asset.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, thumbnailMediaResolveResponse{Items: items})
}

// mediaResolveURL and storeMediaResolveURL keep a short-lived, bounded
// cache for temporary GET URLs. The URL is never persisted and expires
// before the provider's 15-minute signature, so a cache miss naturally
// refreshes it without affecting correctness.
func (r *Router) mediaResolveURL(id string) (string, bool) {
	r.mediaResolveCacheMu.Lock()
	defer r.mediaResolveCacheMu.Unlock()
	entry, ok := r.mediaResolveCache[id]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(r.mediaResolveCache, id)
		return "", false
	}
	return entry.url, true
}

func (r *Router) storeMediaResolveURL(id, url string) {
	r.mediaResolveCacheMu.Lock()
	defer r.mediaResolveCacheMu.Unlock()
	if r.mediaResolveCache == nil {
		r.mediaResolveCache = make(map[string]mediaPreviewCacheEntry)
	}
	now := time.Now()
	for key, entry := range r.mediaResolveCache {
		if now.After(entry.expiresAt) {
			delete(r.mediaResolveCache, key)
		}
	}
	if len(r.mediaResolveCache) >= mediaLibraryPreviewCacheMax {
		for key := range r.mediaResolveCache {
			delete(r.mediaResolveCache, key)
			break
		}
	}
	r.mediaResolveCache[id] = mediaPreviewCacheEntry{url: url, expiresAt: now.Add(mediaTemporaryURLCacheTTL)}
}
