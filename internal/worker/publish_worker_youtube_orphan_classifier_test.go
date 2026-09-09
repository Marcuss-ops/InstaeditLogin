package worker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestIsOrphanedYouTubeVideo covers the publish-worker's Phase-1 orphan-
// video classifier (Blocco #1 followup — Finding #4). The classifier
// decides whether UpdateVideoPrivacy returned a 404 referencing OUR
// yt_pub row's youtube_video_id, signalling that the Phase-1 orphan
// was deleted out from under us (user manual delete via YouTube
// Studio, moderator takedown, etc.) and that the worker should
// synchronously fall through to publisher.Publish after clearing
// the stale yt_pub row.
//
// Primary signal: typed sentinel errors.Is on
// services.ErrYouTubeVideoNotFound. Defense-in-depth substring fallback:
// when the err message contains BOTH the offending videoID AND a
// "not found" marker, fire the recovery branch anyway. Covers any
// future code path not yet re-wired to wrap with the typed sentinel.
//
// Scenarios verified:
//   - nil error                       → false (defensive nil-check)
//   - typed sentinel wrapped error     → true (canonical orphan path)
//   - non-sentinel error, empty videoID → false (substring fallback disabled when no videoID)
//   - non-sentinel error, missing videoID in msg → false (no false-positive orphan classification)
//   - non-sentinel error, msg contains both videoID + "not found" → true (substring fallback fires)
func TestIsOrphanedYouTubeVideo(t *testing.T) {
	const orphanVideoID = "dQw4w9WgXcQ"

	tests := []struct {
		name    string
		err     error
		videoID string
		want    bool
	}{
		{
			name:    "nil error returns false (defensive nil-check)",
			err:     nil,
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "nil error with empty videoID returns false",
			err:     nil,
			videoID: "",
			want:    false,
		},
		{
			name:    "typed sentinel-wrapped error returns true (canonical orphan path)",
			err:     fmt.Errorf("youtube update video: video not found (status 404): video_id=%s: %w", orphanVideoID, services.ErrYouTubeVideoNotFound),
			videoID: orphanVideoID,
			want:    true,
		},
		{
			name:    "typed sentinel-wrapped error returns true even with empty videoID (sentinel is authoritative)",
			err:     fmt.Errorf("orphan: %w", services.ErrYouTubeVideoNotFound),
			videoID: "",
			want:    true,
		},
		{
			name:    "non-sentinel error with empty videoID returns false (substring fallback disabled)",
			err:     errors.New("youtube update video: video not found (status 404)"),
			videoID: "",
			want:    false,
		},
		{
			name:    "non-sentinel error with videoID but no 'not found' marker returns false",
			err:     errors.New("youtube update video: internal server error (status 500) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "non-sentinel error with 'not found' but no matching videoID returns false",
			err:     errors.New("youtube update video: video not found (status 404) for video_id=other123abc"),
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "non-sentinel error matching both videoID and 'not found' returns true (substring fallback)",
			err:     errors.New("youtube update video: video not found (status 404) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    true,
		},
		{
			name:    "non-sentinel error with case-insensitive 'NOT FOUND' marker still matches (defense-in-depth)",
			err:     errors.New("youtube update video: video NOT FOUND (status 404) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOrphanedYouTubeVideo(tt.err, tt.videoID)
			if got != tt.want {
				t.Errorf("isOrphanedYouTubeVideo(%q, %q) = %v, want %v", errString(tt.err), tt.videoID, got, tt.want)
			}
		})
	}
}

// errString is a tiny helper for test output readability — returns
// "<nil>" when err is nil so the message renders cleanly. Avoids
// pulling in a third-party dep just for nil-error formatting.
func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	// Truncate very long error messages so test names stay
	// readable; the substring-fallback test cases use msg strings
	// ~70 chars long, so a 60-char cap keeps output balanced.
	msg := err.Error()
	if len(msg) > 60 {
		msg = strings.TrimRight(msg[:57], " ") + "..."
	}
	return msg
}
