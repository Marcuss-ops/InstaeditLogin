package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

const (
	testPoolClientAID     = "client-a-id.apps.googleusercontent.com"
	testPoolClientBID     = "client-b-id.apps.googleusercontent.com"
	testPoolSecret        = "super-secret-pool-client-0123456789abcdef" // 40 chars, never in logs/errors
	testPoolRedirectA     = "https://instaedit.example.com/oauth/youtube/callback"
	testPoolRedirectB     = "https://instaedit.example.com/oauth/youtube/callback"
	testPoolGoogleSubject = "google-subject-1234567890"
)

// testPoolClients returns the two-client pool fixture used by most
// tests below.
func testPoolClients() []YouTubeOAuthClientConfig {
	return []YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: testPoolClientAID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA},
		{Key: "youtube_pool_b", ClientID: testPoolClientBID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectB},
	}
}

// fakeOAuthClientUsageCounter is a deterministic test double for
// OAuthClientUsageCounter.
type fakeOAuthClientUsageCounter struct {
	usage map[string]int64 // oauthClientKey -> active refresh grants
	err   error
	calls []string
}

func (f *fakeOAuthClientUsageCounter) CountActiveRefreshTokens(_ context.Context, _ string, oauthClientKey string) (int64, error) {
	f.calls = append(f.calls, oauthClientKey)
	if f.err != nil {
		return 0, f.err
	}
	return f.usage[oauthClientKey], nil
}

func TestYouTubeOAuthClientRegistry_New_EmptyClients(t *testing.T) {
	_, err := NewYouTubeOAuthClientRegistry(nil)
	if !errors.Is(err, ErrYouTubeOAuthClientPoolEmpty) {
		t.Fatalf("NewYouTubeOAuthClientRegistry(nil): want ErrYouTubeOAuthClientPoolEmpty, got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_New_SkipsFullyEmptyEntryAndDefaultsCapacity(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{}, // fully empty: skipped as caller convenience
		{Key: "youtube_pool_b", ClientID: testPoolClientBID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectB},
	})
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len: want 1 (fully-empty entry skipped), got %d", r.Len())
	}
	client, err := r.Resolve("youtube_pool_b")
	if err != nil {
		t.Fatalf("Resolve(youtube_pool_b): %v", err)
	}
	if client.RecommendedCapacity != defaultYouTubePoolCapacity {
		t.Errorf("RecommendedCapacity default: want %d, got %d", defaultYouTubePoolCapacity, client.RecommendedCapacity)
	}
}

func TestYouTubeOAuthClientRegistry_New_HalfConfiguredRejected(t *testing.T) {
	// ClientID set but secret and redirect missing: must fail at
	// construction, not at first refresh with invalid_client. The
	// error names the client key and presence booleans, never the
	// credential values.
	_, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: testPoolClientAID},
	})
	if err == nil || !strings.Contains(err.Error(), "half-configured") {
		t.Fatalf("want half-configured error, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), testPoolSecret) {
		t.Fatalf("error must never contain the client secret: %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_New_DuplicateKeyRejected(t *testing.T) {
	_, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: testPoolClientAID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA},
		{Key: "youtube_pool_a", ClientID: testPoolClientBID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate client key") {
		t.Fatalf("want duplicate-key error, got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_New_EmptyKeyRejected(t *testing.T) {
	_, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{ClientID: testPoolClientAID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA},
	})
	if err == nil || !strings.Contains(err.Error(), "empty key") {
		t.Fatalf("want empty-key error, got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_Resolve_ReturnsConfiguredClient(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	client, err := r.Resolve("youtube_pool_a")
	if err != nil {
		t.Fatalf("Resolve(youtube_pool_a): %v", err)
	}
	if client.Key != "youtube_pool_a" || client.ClientID != testPoolClientAID || client.RedirectURI != testPoolRedirectA {
		t.Errorf("Resolve returned wrong config: %+v", client)
	}
	if client.ClientSecret != testPoolSecret {
		t.Error("Resolve must return the full config including the secret (callers need it for token exchange)")
	}
	if got := r.Keys(); len(got) != 2 || got[0] != "youtube_pool_a" || got[1] != "youtube_pool_b" {
		t.Errorf("Keys: want [youtube_pool_a youtube_pool_b], got %v", got)
	}
}

func TestYouTubeOAuthClientRegistry_Resolve_UnknownKey_ErrorDoesNotLeakSecret(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	_, err = r.Resolve("youtube_pool_z")
	if !errors.Is(err, ErrYouTubeOAuthClientUnknown) {
		t.Fatalf("Resolve(unknown): want ErrYouTubeOAuthClientUnknown, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), testPoolSecret) {
		t.Fatalf("Resolve error must never contain the client secret: %v", err)
	}
}

func TestYouTubeOAuthClientConfig_Redacted_NeverContainsSecret(t *testing.T) {
	client := YouTubeOAuthClientConfig{
		Key: "youtube_pool_a", ClientID: testPoolClientAID,
		ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA,
	}
	redacted := client.Redacted()
	if strings.Contains(redacted, testPoolSecret) {
		t.Fatalf("Redacted() leaked the client secret: %s", redacted)
	}
	if !strings.Contains(redacted, testPoolClientAID) {
		t.Errorf("Redacted() should expose the client id for operator triage: %s", redacted)
	}
}

func TestYouTubeOAuthClientRegistry_Select_NoCounter_DeterministicFirst(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	first, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectForNewConnection: %v", err)
	}
	if first.Key != "youtube_pool_a" {
		t.Fatalf("no-counter fallback: want youtube_pool_a (first registered), got %q", first.Key)
	}
	// Deterministic across calls (never random).
	for i := 0; i < 5; i++ {
		again, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
		if err != nil {
			t.Fatalf("SelectForNewConnection: %v", err)
		}
		if again.Key != first.Key {
			t.Fatalf("selection not deterministic: got %q then %q", first.Key, again.Key)
		}
	}
}

func TestYouTubeOAuthClientRegistry_Select_LeastLoaded(t *testing.T) {
	counter := &fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 48,
		"youtube_pool_b": 43,
	}}
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(counter))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}

	selected, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectForNewConnection: %v", err)
	}
	if selected.Key != "youtube_pool_b" {
		t.Fatalf("least-loaded selection: pool B (43) has more headroom than A (48), want youtube_pool_b, got %q", selected.Key)
	}
	if len(counter.calls) != 2 {
		t.Errorf("usage counter must be queried for every pool client; calls=%v", counter.calls)
	}
}

func TestYouTubeOAuthClientRegistry_Select_OverCapacityPicksOther(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: testPoolClientAID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA, RecommendedCapacity: 50},
		{Key: "youtube_pool_b", ClientID: testPoolClientBID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectB, RecommendedCapacity: 50},
	}, WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 61, // over the recommended soft ceiling
		"youtube_pool_b": 20,
	}}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	selected, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectForNewConnection: %v", err)
	}
	if selected.Key != "youtube_pool_b" {
		t.Fatalf("over-capacity A must never be selected while B has headroom; got %q", selected.Key)
	}
}

func TestYouTubeOAuthClientRegistry_Select_Tie_Deterministic(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 40,
		"youtube_pool_b": 40,
	}}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	for i := 0; i < 5; i++ {
		selected, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
		if err != nil {
			t.Fatalf("SelectForNewConnection: %v", err)
		}
		if selected.Key != "youtube_pool_a" {
			t.Fatalf("tie must break to the first registered client (deterministic), got %q", selected.Key)
		}
	}
}

func TestYouTubeOAuthClientRegistry_Select_CounterError_FailsClosed(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{
		usage: map[string]int64{"youtube_pool_a": 10},
		err:   errors.New("db unreachable"),
	}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	_, err = r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err == nil {
		t.Fatal("SelectForNewConnection: want fail-closed error on counter failure, got nil")
	}
	if strings.Contains(err.Error(), testPoolSecret) {
		t.Fatalf("error must never contain the client secret: %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_Select_EmptySubjectRejectedWithCounter(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	_, err = r.SelectForNewConnection(context.Background(), "")
	if err == nil {
		t.Fatal("SelectForNewConnection with empty subject + counter: want error, got nil")
	}
	if !strings.Contains(err.Error(), "googleSubjectID is required") {
		t.Errorf("error must explain the empty subject; got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_Select_CancelledContext(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.SelectForNewConnection(ctx, testPoolGoogleSubject); err == nil {
		t.Fatal("SelectForNewConnection with cancelled ctx: want error, got nil")
	}
}

func TestYouTubeOAuthPoolHealthFor_Bands(t *testing.T) {
	cases := []struct {
		active int64
		want   YouTubeOAuthPoolHealth
	}{
		{0, YouTubeOAuthPoolHealthHealthy},
		{60, YouTubeOAuthPoolHealthHealthy},
		{61, YouTubeOAuthPoolHealthWarning},
		{75, YouTubeOAuthPoolHealthWarning},
		{76, YouTubeOAuthPoolHealthHigh},
		{85, YouTubeOAuthPoolHealthHigh},
		{86, YouTubeOAuthPoolHealthCritical},
		{90, YouTubeOAuthPoolHealthCritical},
		{91, YouTubeOAuthPoolHealthBlocked},
		{100, YouTubeOAuthPoolHealthBlocked},
	}
	for _, tc := range cases {
		if got := YouTubeOAuthPoolHealthFor(tc.active); got != tc.want {
			t.Errorf("YouTubeOAuthPoolHealthFor(%d): want %s, got %s", tc.active, tc.want, got)
		}
	}
}

func TestYouTubeOAuthClientRegistry_Select_AllClientsBlocked_ReturnsExhausted(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 95,
		"youtube_pool_b": 97,
	}}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	_, err = r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if !errors.Is(err, ErrYouTubeOAuthClientPoolExhausted) {
		t.Fatalf("both clients over critical: want ErrYouTubeOAuthClientPoolExhausted, got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_Select_SkipsBlockedClient(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 95, // blocked (>90)
		"youtube_pool_b": 20,
	}}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	selected, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectForNewConnection: %v", err)
	}
	if selected.Key != "youtube_pool_b" {
		t.Fatalf("blocked client A (95) must be skipped; want youtube_pool_b, got %q", selected.Key)
	}
}

func TestYouTubeOAuthClientRegistry_Select_CriticalButNotBlocked_StillSelectable(t *testing.T) {
	// 86–90 is critical but NOT blocked: selection must still be able
	// to land on it when it is the only available client (the operator
	// band says critical, the hard block starts above 90).
	r, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: testPoolClientAID, ClientSecret: testPoolSecret, RedirectURI: testPoolRedirectA, RecommendedCapacity: 50},
	}, WithYouTubeOAuthClientUsageCounter(&fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 90,
	}}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	if _, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject); err != nil {
		t.Fatalf("critical-but-not-blocked client (90) must remain selectable: %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_Select_EmptyRegistry(t *testing.T) {
	var r *YouTubeOAuthClientRegistry
	_, err := r.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if !errors.Is(err, ErrYouTubeOAuthClientPoolEmpty) {
		t.Fatalf("nil registry: want ErrYouTubeOAuthClientPoolEmpty, got %v", err)
	}
}

func TestYouTubeOAuthClientRegistry_FromConfig_NoPoolReturnsNil(t *testing.T) {
	r, err := NewYouTubeOAuthClientRegistryFromConfig(&config.Config{})
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistryFromConfig(empty): %v", err)
	}
	if r != nil {
		t.Fatalf("no pool configured: want (nil, nil), got registry %v", r.Keys())
	}
}

func TestYouTubeOAuthClientRegistry_FromConfig_BuildsBothClients(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			YouTubeOAuthClientPool: config.YouTubeOAuthClientPoolConfig{
				ClientA: config.YouTubeOAuthPoolClient{
					ClientID:     testPoolClientAID,
					ClientSecret: testPoolSecret,
					RedirectURI:  testPoolRedirectA,
				},
				ClientB: config.YouTubeOAuthPoolClient{
					ClientID:     testPoolClientBID,
					ClientSecret: testPoolSecret,
					RedirectURI:  testPoolRedirectB,
				},
			},
		},
	}
	r, err := NewYouTubeOAuthClientRegistryFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistryFromConfig: %v", err)
	}
	if r == nil {
		t.Fatal("registry must not be nil when both pool clients are configured")
	}
	if r.Len() != 2 {
		t.Fatalf("Len: want 2, got %d", r.Len())
	}
	client, err := r.Resolve("youtube_pool_b")
	if err != nil {
		t.Fatalf("Resolve(youtube_pool_b): %v", err)
	}
	if client.ClientID != testPoolClientBID || client.RedirectURI != testPoolRedirectB {
		t.Errorf("Resolve(youtube_pool_b) returned wrong config: %+v", client)
	}
	if client.RecommendedCapacity != defaultYouTubePoolCapacity {
		t.Errorf("RecommendedCapacity default: want %d, got %d", defaultYouTubePoolCapacity, client.RecommendedCapacity)
	}
}
