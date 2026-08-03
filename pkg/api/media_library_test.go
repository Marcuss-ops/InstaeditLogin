package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func readyAsset(id string, userID int64, key string, probed bool) *models.MediaAsset {
	a := &models.MediaAsset{
		ID:          id,
		UserID:      userID,
		UploadKey:   key,
		ContentType: "video/mp4",
		SizeBytes:   1024 * 1024,
		Status:      models.MediaAssetStatusReady,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	if probed {
		now := time.Now()
		duration, width, height, fps, hasAudio := 840.0, 1920, 1080, 30.0, true
		a.DurationSeconds = &duration
		a.Width = &width
		a.Height = &height
		a.FPS = &fps
		a.HasAudio = &hasAudio
		a.VideoCodec = "h264"
		a.AudioCodec = "aac"
		a.ProbedAt = &now
	}
	return a
}

func TestMediaLibrary_ListsReadyAssetsWithCompatibility(t *testing.T) {
	store := newMockMediaStore()
	store.assets["a1"] = readyAsset("a1", 42, "uploads/42/"+testUUID+"_video-01.mp4", true)  // ready (1080p30 h264/aac)
	store.assets["a2"] = readyAsset("a2", 42, "uploads/42/"+testUUID+"_video-02.mp4", false) // never probed → unknown
	vfr := readyAsset("a4", 42, "uploads/42/"+testUUID+"_vfr.mp4", true)                     // probed off-profile → needs_normalization
	off := 23.976
	vfr.FPS = &off
	store.assets["a4"] = vfr
	// A pending + a foreign asset must be excluded.
	store.assets["p1"] = readyAsset("p1", 42, "uploads/42/"+testUUID+"_pending.mp4", false)
	store.assets["p1"].Status = models.MediaAssetStatusPending
	store.assets["f1"] = readyAsset("f1", 7, "uploads/7/"+testUUID+"_other.mp4", false)

	r := newMediaTestRouter(store, newMockStorageProvider())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media", nil)
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []MediaLibraryItem `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only the caller's ready assets are returned.
	if len(resp.Items) != 3 {
		t.Fatalf("items: got %d, want 3 (a1/a2/a4; pending + foreign excluded)", len(resp.Items))
	}

	byID := map[string]MediaLibraryItem{}
	for _, it := range resp.Items {
		byID[it.ID] = it
	}

	a1 := byID["a1"]
	if a1.Filename != "video-01.mp4" {
		t.Errorf("a1 filename: got %q, want video-01.mp4", a1.Filename)
	}
	if a1.LiveCompatibility != liveCompatReady {
		t.Errorf("a1 compatibility: got %q, want %q", a1.LiveCompatibility, liveCompatReady)
	}
	if a1.Width == nil || *a1.Width != 1920 || a1.Height == nil || *a1.Height != 1080 {
		t.Errorf("a1 resolution: got %vx%v, want 1920x1080", a1.Width, a1.Height)
	}
	if a1.FPS == nil || *a1.FPS != 30 {
		t.Errorf("a1 fps: got %v, want 30", a1.FPS)
	}
	if a1.HasAudio == nil || !*a1.HasAudio {
		t.Error("a1 has_audio: want true")
	}
	if a1.VideoCodec != "h264" || a1.AudioCodec != "aac" {
		t.Errorf("a1 codecs: got %s/%s, want h264/aac", a1.VideoCodec, a1.AudioCodec)
	}
	if a1.ProbedAt == nil {
		t.Error("a1 probed_at: want set")
	}
	if a1.PreviewURL == "" {
		t.Error("a1 preview_url: want minted presigned URL")
	}

	a2 := byID["a2"]
	if a2.LiveCompatibility != liveCompatUnknown {
		t.Errorf("a2 compatibility: got %q, want %q (never probed)", a2.LiveCompatibility, liveCompatUnknown)
	}
	if a2.DurationSeconds != nil || a2.ProbedAt != nil {
		t.Error("a2 probe fields: want nil for unprobed asset")
	}

	a4 := byID["a4"]
	if a4.LiveCompatibility != liveCompatNeedsNormalization {
		t.Errorf("a4 compatibility: got %q, want %q (VFR)", a4.LiveCompatibility, liveCompatNeedsNormalization)
	}
}

func TestMediaLibrary_EmptyList(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media", nil)
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Items []MediaLibraryItem `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items == nil || len(resp.Items) != 0 {
		t.Fatalf("items: want empty non-nil slice, got %#v", resp.Items)
	}
}

func TestMediaLibrary_StoreErrorIs500(t *testing.T) {
	store := newMockMediaStore()
	store.errOnList = errors.New("boom")
	r := newMediaTestRouter(store, newMockStorageProvider())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media", nil)
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

func TestMediaLibrary_UnconfiguredIs501(t *testing.T) {
	r := newMediaTestRouter(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media", nil)
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

// testUUID is a realistic 36-char UUID token (hex + dashes, no
// underscores) matching what newUUID4() produces in object keys.
const testUUID = "0f5a1b2c-3d4e-5f60-8a9b-0c1d2e3f4a5b"

func TestMediaAssetFilename_ExtractsDisplayName(t *testing.T) {
	cases := map[string]string{
		"uploads/42/" + testUUID + "_video-01.mp4": "video-01.mp4",
		"uploads/7/" + testUUID + "_my_clip.mp4":   "my_clip.mp4",
		"legacy-key-without-uuid.mp4":              "legacy-key-without-uuid.mp4",
		"uploads/9/short-token_no-separator.mp4":   "short-token_no-separator.mp4",
		"":                                         "",
	}
	for key, want := range cases {
		if got := mediaAssetFilename(key); got != want {
			t.Errorf("mediaAssetFilename(%q) = %q, want %q", key, got, want)
		}
	}
}
