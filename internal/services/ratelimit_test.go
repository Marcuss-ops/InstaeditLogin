// Tests for the SPRINT 2.2 RateLimitService. The service is
// tested with a fake RateLimitRepo (counter map, no DB) + a real
// MemoryLimiter (in-process, no Docker). The Postgres-backed
// Increment contract is exercised through the fake; the
// in-memory tier is exercised through the real MemoryLimiter
// (whose own tests cover the per-scope token-bucket semantics).
package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRateLimitRepo is a deterministic in-memory implementation of
// the RateLimitRepo interface. It returns the post-increment count
// in a fixed window (time.Minute) and supports a forced error mode
// for fail-open coverage.
type fakeRateLimitRepo struct {
	mu       sync.Mutex
	counters map[string]int // scope -> count in current window
	winStart time.Time      // start of the current 1-minute window
	forceErr error          // when set, Increment returns this error
	calls    int64          // atomic counter for call assertions
}

func newFakeRateLimitRepo() *fakeRateLimitRepo {
	return &fakeRateLimitRepo{
		counters: make(map[string]int),
		winStart: time.Now(),
	}
}

func (f *fakeRateLimitRepo) Increment(_ context.Context, scope string, window time.Duration) (int, time.Time, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.forceErr != nil {
		return 0, time.Time{}, f.forceErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Roll the window if the supplied window puts us past winStart + window.
	if time.Since(f.winStart) >= window {
		f.counters = make(map[string]int)
		f.winStart = time.Now()
	}
	f.counters[scope]++
	return f.counters[scope], f.winStart.Add(window), nil
}

func (f *fakeRateLimitRepo) setCount(scope string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[scope] = n
}

// -----------------------------------------------------------------------
// Tier scope builders
// -----------------------------------------------------------------------

func TestWorkspacePostLimit_ScopeAndLimit(t *testing.T) {
	tier := WorkspacePostLimit(42)
	if tier.Scope != "ws_post:42" {
		t.Errorf("scope: want ws_post:42, got %q", tier.Scope)
	}
	if tier.Storage != StoragePostgres {
		t.Errorf("storage: want StoragePostgres, got %v", tier.Storage)
	}
	if tier.Limit != 60 {
		t.Errorf("limit: want 60, got %d", tier.Limit)
	}
}

func TestAPIKeyReadLimit_ScopeAndLimit(t *testing.T) {
	tier := APIKeyReadLimit(7)
	if tier.Scope != "apikey_read:7" {
		t.Errorf("scope: want apikey_read:7, got %q", tier.Scope)
	}
	if tier.Storage != StoragePostgres {
		t.Errorf("storage: want StoragePostgres, got %v", tier.Storage)
	}
	if tier.Limit != 600 {
		t.Errorf("limit: want 600, got %d", tier.Limit)
	}
}

func TestMediaPresignLimit_ScopeAndLimit(t *testing.T) {
	tier := MediaPresignLimit()
	if tier.Scope != "endpoint:media_presign" {
		t.Errorf("scope: want endpoint:media_presign, got %q", tier.Scope)
	}
	if tier.Storage != StorageMemory {
		t.Errorf("storage: want StorageMemory, got %v", tier.Storage)
	}
	if tier.Limit != 30 {
		t.Errorf("limit: want 30, got %d", tier.Limit)
	}
}

func TestOAuthStartLimit_ScopeAndLimit(t *testing.T) {
	tier := OAuthStartLimit("203.0.113.42")
	if tier.Scope != "oauth_ip:203.0.113.42" {
		t.Errorf("scope: want oauth_ip:203.0.113.42, got %q", tier.Scope)
	}
	if tier.Storage != StorageMemory {
		t.Errorf("storage: want StorageMemory, got %v", tier.Storage)
	}
	if tier.Limit != 20 {
		t.Errorf("limit: want 20, got %d", tier.Limit)
	}
}

// -----------------------------------------------------------------------
// Postgres tier (per-workspace, per-key)
// -----------------------------------------------------------------------

func TestRateLimit_Check_Postgres_AllowsFirstN(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := NewRateLimitService(repo)
	ctx := context.Background()
	tier := WorkspacePostLimit(1)
	for i := 0; i < tier.Limit; i++ {
		ok, remaining, _, err := svc.Check(ctx, tier, tier.Limit)
		if err != nil {
			t.Fatalf("request %d: unexpected error %v", i, err)
		}
		if !ok {
			t.Fatalf("request %d: denied, want allowed", i)
		}
		// remaining should be limit - (i+1) (post-increment count)
		wantRem := tier.Limit - (i + 1)
		if remaining != wantRem {
			t.Errorf("request %d: remaining want %d, got %d", i, wantRem, remaining)
		}
	}
	if got := atomic.LoadInt64(&repo.calls); got != int64(tier.Limit) {
		t.Errorf("repo.Increment calls: want %d, got %d", tier.Limit, got)
	}
}

func TestRateLimit_Check_Postgres_DeniesNPlusOne(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := NewRateLimitService(repo)
	ctx := context.Background()
	tier := WorkspacePostLimit(2)
	// Exhaust the budget.
	for i := 0; i < tier.Limit; i++ {
		ok, _, _, _ := svc.Check(ctx, tier, tier.Limit)
		if !ok {
			t.Fatalf("warmup request %d denied", i)
		}
	}
	// N+1 must be denied.
	ok, remaining, _, err := svc.Check(ctx, tier, tier.Limit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("N+1: want denied, got allowed")
	}
	if remaining != 0 {
		t.Errorf("N+1 remaining: want 0, got %d", remaining)
	}
}

func TestRateLimit_Check_Postgres_IndependentScopes(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := NewRateLimitService(repo)
	ctx := context.Background()
	tierA := WorkspacePostLimit(100)
	tierB := WorkspacePostLimit(200)
	// Exhaust tier A.
	for i := 0; i < tierA.Limit; i++ {
		svc.Check(ctx, tierA, tierA.Limit)
	}
	okA, _, _, _ := svc.Check(ctx, tierA, tierA.Limit)
	if okA {
		t.Errorf("tier A should be exhausted")
	}
	// Tier B is independent — first call must succeed.
	okB, _, _, _ := svc.Check(ctx, tierB, tierB.Limit)
	if !okB {
		t.Errorf("tier B should be fresh: got denied")
	}
}

// -----------------------------------------------------------------------
// Fail-open posture
// -----------------------------------------------------------------------

func TestRateLimit_Check_Postgres_FailOpenOnError(t *testing.T) {
	repo := newFakeRateLimitRepo()
	repo.forceErr = errors.New("simulated db blip")
	svc := NewRateLimitService(repo)
	ctx := context.Background()
	tier := WorkspacePostLimit(3)
	ok, remaining, resetAt, err := svc.Check(ctx, tier, tier.Limit)
	if err == nil {
		t.Fatal("expected error to be surfaced")
	}
	if !ok {
		t.Errorf("fail-open: want allowed=true, got %v", ok)
	}
	if remaining != tier.Limit {
		t.Errorf("fail-open: remaining want %d, got %d", tier.Limit, remaining)
	}
	if resetAt.IsZero() {
		t.Errorf("fail-open: resetAt should be set (now+window)")
	}
}

// -----------------------------------------------------------------------
// Memory tier
// -----------------------------------------------------------------------

func TestRateLimit_Check_Memory_AllowsAndDenies(t *testing.T) {
	svc := NewRateLimitService(newFakeRateLimitRepo())
	defer svc.mem.Shutdown()
	ctx := context.Background()
	tier := MediaPresignLimit()
	// First N calls allowed.
	for i := 0; i < tier.Limit; i++ {
		ok, _, _, err := svc.Check(ctx, tier, tier.Limit)
		if err != nil {
			t.Fatalf("warmup %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("warmup %d: want allowed", i)
		}
	}
	// N+1 must be denied. (Token bucket: burst=Limit, then immediate refill
	// at rpm/60 per second, so within the same millisecond N+1 is denied.)
	ok, remaining, _, _ := svc.Check(ctx, tier, tier.Limit)
	if ok {
		t.Errorf("N+1: want denied")
	}
	if remaining != 0 {
		t.Errorf("N+1: remaining want 0, got %d", remaining)
	}
}

func TestRateLimit_Check_UnknownStorage_ReturnsTrueAndError(t *testing.T) {
	svc := NewRateLimitService(newFakeRateLimitRepo())
	defer svc.mem.Shutdown()
	ctx := context.Background()
	tier := Tier{Storage: Storage(99), Scope: "weird", Limit: 1}
	ok, _, _, err := svc.Check(ctx, tier, tier.Limit)
	if err == nil {
		t.Fatal("want error for unknown storage")
	}
	if !ok {
		t.Errorf("unknown storage: want fail-open allowed=true, got %v", ok)
	}
}

func TestRateLimit_Check_ZeroLimit_AlwaysAllows(t *testing.T) {
	svc := NewRateLimitService(newFakeRateLimitRepo())
	defer svc.mem.Shutdown()
	ctx := context.Background()
	tier := WorkspacePostLimit(1)
	for i := 0; i < 5; i++ {
		ok, _, _, err := svc.Check(ctx, tier, 0)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !ok {
			t.Errorf("call %d with limit=0: want allowed", i)
		}
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------
