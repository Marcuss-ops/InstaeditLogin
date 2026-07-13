package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// -----------------------------------------------------------------------
// PlatformThrottle tests — FASE 1.3
// -----------------------------------------------------------------------

func TestThrottle_Wait_AcquiresTokenImmediately(t *testing.T) {
	pt := NewPlatformThrottle()

	// First call should return immediately (burst=1, bucket full).
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := pt.Wait(ctx, "instagram"); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait took %v, want immediate (bucket full)", elapsed)
	}
}

func TestThrottle_Wait_BlocksWhenEmpty(t *testing.T) {
	pt := NewPlatformThrottle()

	// Consume the burst token.
	ctx := context.Background()
	if err := pt.Wait(ctx, "tiktok"); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	// Second call should block (TikTok rate: 1/2s, burst=1).
	// Use a generous timeout so slow CI runners do not flake.
	ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := pt.Wait(ctx2, "tiktok"); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
	elapsed := time.Since(start)
	// TikTok rate is 0.5 req/s = 2s per token. Elapsed should be
	// at least ~2s, but allow generous slack for scheduler variance.
	if elapsed < 1300*time.Millisecond {
		t.Errorf("Wait took %v, want >=1300ms (tiktok rate: 0.5/s)", elapsed)
	}
}

func TestThrottle_DifferentPlatforms_IndependentBuckets(t *testing.T) {
	pt := NewPlatformThrottle()

	ctx := context.Background()

	// Consume burst tokens for both platforms.
	if err := pt.Wait(ctx, "instagram"); err != nil {
		t.Fatalf("instagram first Wait: %v", err)
	}
	if err := pt.Wait(ctx, "tiktok"); err != nil {
		t.Fatalf("tiktok first Wait: %v", err)
	}

	// Instagram has rate=2/s (500ms per token). TikTok has 0.5/s
	// (2s per token). Calling Instagram again should return much
	// faster than TikTok. Use generous timeouts to avoid flakes on
	// slow or contended CI runners.
	start := time.Now()
	if err := pt.Wait(ctx, "instagram"); err != nil {
		t.Fatalf("instagram second Wait: %v", err)
	}
	igElapsed := time.Since(start)
	if igElapsed > 900*time.Millisecond {
		t.Errorf("instagram Wait took %v, want ~500ms (rate=2/s)", igElapsed)
	}

	start = time.Now()
	if err := pt.Wait(ctx, "tiktok"); err != nil {
		t.Fatalf("tiktok second Wait: %v", err)
	}
	ttElapsed := time.Since(start)
	if ttElapsed < 1300*time.Millisecond {
		t.Errorf("tiktok Wait took %v, want >=1300ms (rate=0.5/s)", ttElapsed)
	}
}

func TestThrottle_ContextCancel_ReturnsError(t *testing.T) {
	pt := NewPlatformThrottle()

	// Consume burst.
	if err := pt.Wait(context.Background(), "tiktok"); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	// Second call with a context that cancels immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := pt.Wait(ctx, "tiktok")
	if err == nil {
		t.Fatal("expected context.Canceled, got nil")
	}
	if err != context.Canceled {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestThrottle_UnknownPlatform_DefaultsToOnePerSecond(t *testing.T) {
	pt := NewPlatformThrottle()

	// Unknown platform should get 1 req/s (1s per token after burst).
	ctx := context.Background()
	if err := pt.Wait(ctx, "unknown-platform"); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx2, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	if err := pt.Wait(ctx2, "unknown-platform"); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 700*time.Millisecond {
		t.Errorf("unknown platform Wait took %v, want >=700ms (rate=1/s)", elapsed)
	}
}

func TestThrottle_ConcurrentAccess_NoRace(t *testing.T) {
	pt := NewPlatformThrottle()

	var wg sync.WaitGroup
	const goroutines = 10

	// Each goroutine calls Wait once. Since burst=1, only the first
	// returns immediately; the rest wait for tokens to refill. With
	// 10 goroutines and instagram rate=2/s, total time should be
	// about (10-1)/2 = 4.5s. Use a generous timeout so slow CI
	// runners do not flake.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pt.Wait(ctx, "instagram")
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("Wait error: %v", err)
		}
	}
}

// TestThrottle_LimiterRates verifies the configured rate limits
// without relying on wall-clock timing. This is the authoritative
// check that each platform gets the expected rate; the timing-based
// tests above are smoke tests that may be skipped or relaxed further
// if they remain flaky on slow CI runners.
func TestThrottle_LimiterRates(t *testing.T) {
	pt := NewPlatformThrottle()

	cases := []struct {
		platform string
		want     float64
	}{
		{"instagram", 2},
		{"facebook", 2},
		{"threads", 2},
		{"tiktok", 0.5},
		{"youtube", 0.33},
		{"twitter", 1},
		{"linkedin", 0.5},
		{"unknown-platform", 1},
	}

	for _, tc := range cases {
		lim := pt.LimiterFor(tc.platform)
		if lim == nil {
			t.Fatalf("LimiterFor(%q) returned nil", tc.platform)
		}
		got := lim.Limit()
		if got != rate.Limit(tc.want) {
			t.Errorf("LimiterFor(%q).Limit() = %v, want %v", tc.platform, got, tc.want)
		}
	}
}

func TestThrottle_AllKnownPlatforms_Defined(t *testing.T) {
	pt := NewPlatformThrottle()

	platforms := []string{
		"instagram", "facebook", "threads", "tiktok",
		"youtube", "twitter", "linkedin",
	}

	for _, p := range platforms {
		ctx := context.Background()
		if err := pt.Wait(ctx, p); err != nil {
			t.Errorf("Wait(%q): %v", p, err)
		}
	}

	// Verify each platform has a different limiter.
	pt.mu.Lock()
	count := len(pt.entries)
	pt.mu.Unlock()
	if count != len(platforms) {
		t.Errorf("entries: want %d, got %d", len(platforms), count)
	}
}
