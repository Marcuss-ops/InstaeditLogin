// Tests for the canonical quota-day function YouTubeQuotaDay. Google
// resets every YouTube Data API v3 daily quota bucket at midnight
// Pacific Time (America/Los_Angeles), so the day key and the
// retry-after window must derive from midnight PT — never UTC midnight.
// These tests pin both the fixed UTC equivalents (07:00 UTC during PDT,
// 08:00 UTC during PST) and the DST-day lengths the retry-after math
// depends on.
package repository

import (
	"testing"
	"time"
)

// locLA loads America/Los_Angeles once per test; the embedded tzdata
// guarantees resolution without host timezone data.
func locLA(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load America/Los_Angeles: %v", err)
	}
	return loc
}

// TestYouTubeQuotaDay_PDTBoundary pins the reset boundary during
// Pacific Daylight Time (March–November): midnight PT == 07:00 UTC.
func TestYouTubeQuotaDay_PDTBoundary(t *testing.T) {
	// 06:59:59 UTC on July 20 is still July 19 in LA.
	day := YouTubeQuotaDay(time.Date(2026, 7, 20, 6, 59, 59, 0, time.UTC))
	if got := day.Format("2006-01-02"); got != "2026-07-19" {
		t.Errorf("06:59:59 UTC → day %s, want 2026-07-19", got)
	}
	// 07:00:00 UTC on July 20 is midnight July 20 in LA.
	day = YouTubeQuotaDay(time.Date(2026, 7, 20, 7, 0, 0, 0, time.UTC))
	if got := day.Format("2006-01-02"); got != "2026-07-20" {
		t.Errorf("07:00:00 UTC → day %s, want 2026-07-20", got)
	}
}

// TestYouTubeQuotaDay_PSTBoundary pins the reset boundary during
// Pacific Standard Time (November–March): midnight PT == 08:00 UTC.
func TestYouTubeQuotaDay_PSTBoundary(t *testing.T) {
	day := YouTubeQuotaDay(time.Date(2026, 1, 15, 7, 59, 59, 0, time.UTC))
	if got := day.Format("2006-01-02"); got != "2026-01-14" {
		t.Errorf("07:59:59 UTC → day %s, want 2026-01-14", got)
	}
	day = YouTubeQuotaDay(time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC))
	if got := day.Format("2006-01-02"); got != "2026-01-15" {
		t.Errorf("08:00:00 UTC → day %s, want 2026-01-15", got)
	}
}

// TestYouTubeQuotaDay_ReturnsPacificMidnight asserts the returned value
// is exactly 00:00 in America/Los_Angeles carrying the LA location, and
// that the date key is the Pacific calendar date (timezone-independent).
func TestYouTubeQuotaDay_ReturnsPacificMidnight(t *testing.T) {
	day := YouTubeQuotaDay(time.Date(2026, 7, 20, 12, 34, 56, 0, time.UTC))
	if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 || day.Nanosecond() != 0 {
		t.Errorf("quota day must be midnight: got %v", day)
	}
	if got := day.Location().String(); got != "America/Los_Angeles" {
		t.Errorf("quota day location: want America/Los_Angeles, got %q", got)
	}
	zone, offset := day.Zone()
	if zone != "PDT" || offset != -7*60*60 {
		t.Errorf("July quota day zone: want PDT (UTC-7), got %s (%d)", zone, offset)
	}
	// The Pacific calendar date must be stable regardless of the
	// timezone the input was expressed in.
	if got := day.Format("2006-01-02"); got != "2026-07-20" {
		t.Errorf("Pacific date key: got %s, want 2026-07-20", got)
	}
}

// TestYouTubeQuotaDay_NextMidnightAcrossDST pins the retry-after
// boundary: the next quota day (YouTubeQuotaDay(now).AddDate(0,0,1))
// must be the true next midnight PT — 23h after a spring-forward day,
// 25h after a fall-back day, 24h on a regular day. Adding 24 absolute
// hours instead would land on the wrong side of the fall-back boundary.
func TestYouTubeQuotaDay_NextMidnightAcrossDST(t *testing.T) {
	loc := locLA(t)

	// 2026-03-08: DST starts at 02:00 PST → the day has 23 hours.
	springStart := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	if gap := YouTubeQuotaDay(springStart).AddDate(0, 0, 1).Sub(springStart); gap != 23*time.Hour {
		t.Errorf("spring-forward day length: want 23h, got %v", gap)
	}

	// 2026-11-01: DST ends at 02:00 PDT → the day has 25 hours.
	fallStart := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	if gap := YouTubeQuotaDay(fallStart).AddDate(0, 0, 1).Sub(fallStart); gap != 25*time.Hour {
		t.Errorf("fall-back day length: want 25h, got %v", gap)
	}

	// Regular PDT day: exactly 24h.
	regular := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)
	if gap := YouTubeQuotaDay(regular).AddDate(0, 0, 1).Sub(regular); gap != 24*time.Hour {
		t.Errorf("regular day length: want 24h, got %v", gap)
	}
}
