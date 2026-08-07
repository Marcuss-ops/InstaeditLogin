package services

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// fakeOAuthTokenCapacityCounter is the test double for the storage
// counter backing the capacity manager: per-key counts for
// CountActiveRefreshTokens and grouped rows for ListPoolUsage.
type fakeOAuthTokenCapacityCounter struct {
	usage map[string]int64 // oauthClientKey -> active grants
	rows  []repository.OAuthPoolUsageRow
	err   error
}

func (f *fakeOAuthTokenCapacityCounter) CountActiveRefreshTokens(_ context.Context, _, oauthClientKey string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.usage[oauthClientKey], nil
}

func (f *fakeOAuthTokenCapacityCounter) ListPoolUsage(_ context.Context, _ string) ([]repository.OAuthPoolUsageRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// newTestCapacityManager returns the CONCRETE manager so tests can also
// exercise the OAuthClientUsageCounter delegation (the interface does
// not expose CountActiveRefreshTokens).
func newTestCapacityManager(counter OAuthTokenCapacityCounter) *oauthTokenCapacityManager {
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		panic(err)
	}
	m, err := NewOAuthTokenCapacityManager(counter, reg)
	if err != nil {
		panic(err)
	}
	return m.(*oauthTokenCapacityManager)
}

func TestNewOAuthTokenCapacityManager_NilCounter(t *testing.T) {
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	if _, err := NewOAuthTokenCapacityManager(nil, reg); err == nil {
		t.Fatal("nil counter: want error, got nil")
	}
}

func TestNewOAuthTokenCapacityManager_NilRegistry(t *testing.T) {
	if _, err := NewOAuthTokenCapacityManager(&fakeOAuthTokenCapacityCounter{}, nil); !errors.Is(err, ErrYouTubeOAuthClientPoolEmpty) {
		t.Fatalf("nil registry: want ErrYouTubeOAuthClientPoolEmpty, got %v", err)
	}
}

func TestOAuthTokenCapacityManager_SelectPool_LeastLoaded(t *testing.T) {
	// Pool A has 48/50 used, pool B 43/50 → B has the most headroom.
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{
		"youtube_pool_a": 48,
		"youtube_pool_b": 43,
	}})

	sel, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectPool: %v", err)
	}
	if sel.Key != "youtube_pool_b" {
		t.Errorf("least-loaded selection: want youtube_pool_b (43 used vs 48), got %q", sel.Key)
	}
	if sel.ClientID != testPoolClientBID {
		t.Errorf("ClientID: want %q, got %q", testPoolClientBID, sel.ClientID)
	}
	if sel.ActiveRefreshTokens != 43 {
		t.Errorf("ActiveRefreshTokens: want 43, got %d", sel.ActiveRefreshTokens)
	}
	if sel.RecommendedCapacity != defaultYouTubePoolCapacity {
		t.Errorf("RecommendedCapacity: want %d, got %d", defaultYouTubePoolCapacity, sel.RecommendedCapacity)
	}
	if sel.RemainingCapacity != defaultYouTubePoolCapacity-43 {
		t.Errorf("RemainingCapacity: want %d, got %d", defaultYouTubePoolCapacity-43, sel.RemainingCapacity)
	}
}

func TestOAuthTokenCapacityManager_SelectPool_Tie_Deterministic(t *testing.T) {
	// Equal usage → first registered client wins (deterministic, never
	// random alternation).
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{
		"youtube_pool_a": 40,
		"youtube_pool_b": 40,
	}})
	for i := 0; i < 5; i++ {
		sel, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject)
		if err != nil {
			t.Fatalf("SelectPool: %v", err)
		}
		if sel.Key != "youtube_pool_a" {
			t.Fatalf("tie must break to the first registered client, got %q", sel.Key)
		}
	}
}

// TestOAuthTokenCapacityManager_SelectPool_AllOverSoftCapacity pins the
// edge case where EVERY client is over the recommended soft capacity
// (51–90 active grants) but under the hard block threshold: the
// selection must still pick the client with the most headroom AND
// report its real usage (previously the baseline initialization made
// the first client win with ActiveRefreshTokens=0).
func TestOAuthTokenCapacityManager_SelectPool_AllOverSoftCapacity(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{
		"youtube_pool_a": 80,
		"youtube_pool_b": 75,
	}})
	sel, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectPool: %v", err)
	}
	if sel.Key != "youtube_pool_b" {
		t.Errorf("all clients over soft capacity: B (75) has more headroom than A (80); want youtube_pool_b, got %q", sel.Key)
	}
	if sel.ActiveRefreshTokens != 75 {
		t.Errorf("ActiveRefreshTokens: want 75 (the selected client's real usage), got %d", sel.ActiveRefreshTokens)
	}
	if sel.RemainingCapacity != defaultYouTubePoolCapacity-75 {
		t.Errorf("RemainingCapacity: want %d, got %d", defaultYouTubePoolCapacity-75, sel.RemainingCapacity)
	}
}

func TestOAuthTokenCapacityManager_SelectPool_SkipsBlockedClient(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{
		"youtube_pool_a": 95, // > 90 → blocked, must be skipped
		"youtube_pool_b": 20,
	}})
	sel, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectPool: %v", err)
	}
	if sel.Key != "youtube_pool_b" {
		t.Errorf("blocked client A must be skipped; want youtube_pool_b, got %q", sel.Key)
	}
}

func TestOAuthTokenCapacityManager_SelectPool_AllBlocked_Exhausted(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{
		"youtube_pool_a": 95,
		"youtube_pool_b": 97,
	}})
	_, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject)
	if !errors.Is(err, ErrYouTubeOAuthClientPoolExhausted) {
		t.Fatalf("all clients blocked: want ErrYouTubeOAuthClientPoolExhausted, got %v", err)
	}
}

func TestOAuthTokenCapacityManager_SelectPool_CounterError_FailsClosed(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{
		usage: map[string]int64{"youtube_pool_a": 10},
		err:   errors.New("db unreachable"),
	})
	if _, err := m.SelectPool(context.Background(), models.PlatformYouTube, testPoolGoogleSubject); err == nil {
		t.Fatal("counter failure: want fail-closed error, got nil")
	}
}

func TestOAuthTokenCapacityManager_SelectPool_UnsupportedProvider(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{}})
	if _, err := m.SelectPool(context.Background(), "tiktok", testPoolGoogleSubject); err == nil {
		t.Fatal("non-YouTube provider: want error, got nil")
	}
}

func TestOAuthTokenCapacityManager_SelectPool_EmptySubjectRejected(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{}})
	if _, err := m.SelectPool(context.Background(), models.PlatformYouTube, ""); err == nil {
		t.Fatal("empty subject: want error, got nil")
	}
}

func TestOAuthTokenCapacityManager_GetUsage_ZeroFillsConfiguredClients(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{rows: []repository.OAuthPoolUsageRow{
		{ProviderSubjectID: testPoolGoogleSubject, OAuthClientKey: "youtube_pool_a", ActiveRefreshTokens: 48},
		// youtube_pool_b has no rows → must be zero-filled.
	}})

	usage, err := m.GetUsage(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("GetUsage must return every configured client, got %d: %+v", len(usage), usage)
	}
	a, b := usage[0], usage[1]
	if a.OAuthClientKey != "youtube_pool_a" || a.ActiveRefreshTokens != 48 {
		t.Errorf("usage[0]: want (youtube_pool_a, 48), got %+v", a)
	}
	if b.OAuthClientKey != "youtube_pool_b" || b.ActiveRefreshTokens != 0 {
		t.Errorf("usage[1]: want (youtube_pool_b, 0 zero-filled), got %+v", b)
	}
	for _, u := range usage {
		if u.RecommendedCapacity != defaultYouTubePoolCapacity {
			t.Errorf("%s RecommendedCapacity: want %d, got %d", u.OAuthClientKey, defaultYouTubePoolCapacity, u.RecommendedCapacity)
		}
		if u.ProviderLimit != GoogleOAuthClientRefreshTokenLimit {
			t.Errorf("%s ProviderLimit: want %d, got %d", u.OAuthClientKey, GoogleOAuthClientRefreshTokenLimit, u.ProviderLimit)
		}
	}
}

func TestOAuthTokenCapacityManager_GetUsage_CounterError(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{err: errors.New("db unreachable")})
	if _, err := m.GetUsage(context.Background(), testPoolGoogleSubject); err == nil {
		t.Fatal("counter failure: want error, got nil")
	}
}

func TestOAuthTokenCapacityManager_GetUsage_EmptySubjectRejected(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{})
	if _, err := m.GetUsage(context.Background(), ""); err == nil {
		t.Fatal("empty subject: want error, got nil")
	}
}

// TestOAuthTokenCapacityManager_ImplementsUsageCounter pins that the
// manager can be wired directly as the registry's usage counter
// (WithYouTubeOAuthClientUsageCounter) once the login flow resolves the
// Google subject — the per-(subject, client) count delegates to the
// storage counter.
func TestOAuthTokenCapacityManager_ImplementsUsageCounter(t *testing.T) {
	m := newTestCapacityManager(&fakeOAuthTokenCapacityCounter{usage: map[string]int64{"youtube_pool_a": 61}})
	count, err := m.CountActiveRefreshTokens(context.Background(), testPoolGoogleSubject, "youtube_pool_a")
	if err != nil {
		t.Fatalf("CountActiveRefreshTokens: %v", err)
	}
	if count != 61 {
		t.Errorf("count: want 61, got %d", count)
	}
}
