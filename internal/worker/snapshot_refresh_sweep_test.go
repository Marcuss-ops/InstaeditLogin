package worker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// refreshSweepStoreStub is a SnapshotRefreshStore with a fixed pending
// list (or error), plus recorders for the upserts it received.
type refreshSweepStoreStub struct {
	pending   []repository.PendingSnapshotRefresh
	listErr   error
	upsertErr error
	claimErr  error

	mu              sync.Mutex
	upserted        []*repository.AccountResourceSnapshot
	upsertCalls     int
	claimCalls      int
	rescheduleCalls int
	lastBatchSize   int
}

func (s *refreshSweepStoreStub) ClaimPendingSnapshotRefreshes(_ context.Context, limit int, _ time.Duration) ([]repository.PendingSnapshotRefresh, error) {
	s.mu.Lock()
	s.lastBatchSize = limit
	s.claimCalls++
	s.mu.Unlock()
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.pending, nil
}

func (s *refreshSweepStoreStub) RescheduleSnapshotRefresh(_ context.Context, _ int64, _ time.Time, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rescheduleCalls++
	return nil
}

func (s *refreshSweepStoreStub) UpsertSnapshot(snap *repository.AccountResourceSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertCalls++
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = append(s.upserted, snap)
	return nil
}

func (s *refreshSweepStoreStub) upsertCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upsertCalls
}

// sweepFetcher is an AccountDetailsFetcher with a call counter + optional
// error injection. fetchCalls is mu-protected because the worker refreshes
// accounts concurrently.
type sweepFetcher struct {
	mu         sync.Mutex
	fetchCalls int
	details    *models.AccountDetails
	err        error
}

func (f *sweepFetcher) GetAccountDetails(_ context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.details, nil
}

func (f *sweepFetcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetchCalls
}

// newRefreshSweepHarness wires a SnapshotRefreshSweepWorker with the
// given pending list and a configurable token outcome. refreshers +
// fetchers cover youtube by default; override via the returned harness.
func newRefreshSweepHarness(t *testing.T, pending []repository.PendingSnapshotRefresh, renewOutcome func(accountID int64) error) (*refreshSweepStoreStub, *sweepFetcher, *SnapshotRefreshSweepWorker) {
	t.Helper()
	store := &refreshSweepStoreStub{pending: pending}
	fetcher := &sweepFetcher{details: &models.AccountDetails{
		ResourceType: "channel",
		DisplayName:  "Refreshed Channel",
		ExternalID:   "UC-refreshed",
		FetchedAt:    time.Now(),
	}}
	vault := &mockCredentialVault{
		renewFn: func(_ context.Context, accountID int64, tokenType string, refresher credentials.TokenRefresher) (*models.OAuthToken, error) {
			if renewOutcome != nil {
				if err := renewOutcome(accountID); err != nil {
					return nil, err
				}
			}
			return &models.OAuthToken{AccessToken: "fresh-token", TokenType: tokenType}, nil
		},
	}
	refreshers := map[string]credentials.TokenRefresher{
		models.PlatformYouTube: func(_ context.Context, refreshToken string) (*models.TokenData, error) {
			return &models.TokenData{AccessToken: "fresh", RefreshToken: refreshToken}, nil
		},
	}
	fetchers := map[string]AccountDetailsFetcher{
		models.PlatformYouTube: fetcher,
	}
	worker := NewSnapshotRefreshSweepWorker(store, vault, refreshers, fetchers, 0, newSweepTestLogger())
	return store, fetcher, worker
}

// TestSnapshotRefreshSweep_Tick_RefreshesPendingAccounts proves the core
// flow: pending accounts are fetched from the provider and upserted as
// fresh snapshots (which clears refresh_pending_at), with the batch
// limit forwarded to the store.
func TestSnapshotRefreshSweep_Tick_RefreshesPendingAccounts(t *testing.T) {
	pending := []repository.PendingSnapshotRefresh{
		{PlatformAccountID: 21, Platform: "youtube", PlatformUserID: "UC-one", Username: "one"},
		{PlatformAccountID: 22, Platform: "youtube", PlatformUserID: "UC-two", Username: "two"},
	}
	store, fetcher, w := newRefreshSweepHarness(t, pending, nil)

	w.tick(context.Background())

	if fetcher.calls() != 2 {
		t.Errorf("provider GetAccountDetails calls: want 2, got %d", fetcher.calls())
	}
	if store.upsertCount() != 2 {
		t.Fatalf("UpsertSnapshot calls: want 2, got %d", store.upsertCount())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upserted) != 2 {
		t.Fatalf("upserted snapshots: want 2, got %d", len(store.upserted))
	}
	// The sweep refreshes accounts concurrently, so the upsert ORDER is
	// not deterministic — assert the SET of account ids instead.
	gotIDs := map[int64]bool{}
	for _, snap := range store.upserted {
		gotIDs[snap.PlatformAccountID] = true
		if snap.Profile["display_name"] != "Refreshed Channel" {
			t.Errorf("upserted snapshot %d: profile display_name = %v", snap.PlatformAccountID, snap.Profile["display_name"])
		}
	}
	if !gotIDs[21] || !gotIDs[22] {
		t.Errorf("upserted account ids: want {21,22}, got %v", gotIDs)
	}
	if store.lastBatchSize != repository.SnapshotRefreshBatchLimit {
		t.Errorf("batch limit forwarded to store: want %d, got %d", repository.SnapshotRefreshBatchLimit, store.lastBatchSize)
	}
}

// TestSnapshotRefreshSweep_Tick_PlatformWithoutFetcher_Skipped proves a
// pending account whose platform has no AccountDetailsFetcher wired is
// skipped WITHOUT a provider call or upsert (and without aborting the
// pass for the accounts that DO have a fetcher).
func TestSnapshotRefreshSweep_Tick_PlatformWithoutFetcher_Skipped(t *testing.T) {
	pending := []repository.PendingSnapshotRefresh{
		{PlatformAccountID: 21, Platform: "youtube", PlatformUserID: "UC-one", Username: "one"},
		{PlatformAccountID: 30, Platform: "tiktok", PlatformUserID: "tt-three", Username: "three"}, // no fetcher wired
	}
	store, fetcher, w := newRefreshSweepHarness(t, pending, nil)

	w.tick(context.Background())

	if fetcher.calls() != 1 {
		t.Errorf("provider GetAccountDetails calls: want 1 (tiktok skipped), got %d", fetcher.calls())
	}
	if store.upsertCount() != 1 {
		t.Errorf("UpsertSnapshot calls: want 1, got %d", store.upsertCount())
	}
}

// TestSnapshotRefreshSweep_Tick_TokenFailureIsolated pins the failure
// isolation contract: an account whose token cannot be resolved is
// logged and skipped, and the sweep continues with the other accounts.
// No panic, no abort.
func TestSnapshotRefreshSweep_Tick_TokenFailureIsolated(t *testing.T) {
	pending := []repository.PendingSnapshotRefresh{
		{PlatformAccountID: 21, Platform: "youtube", PlatformUserID: "UC-one", Username: "one"},
		{PlatformAccountID: 22, Platform: "youtube", PlatformUserID: "UC-two", Username: "two"},
	}
	store, fetcher, w := newRefreshSweepHarness(t, pending, func(accountID int64) error {
		if accountID == 21 {
			return errors.New("token refresh failed (status 400)")
		}
		return nil
	})

	w.tick(context.Background()) // must not panic

	if fetcher.calls() != 1 {
		t.Errorf("provider GetAccountDetails calls: want 1 (account 21 token-failed, skipped), got %d", fetcher.calls())
	}
	if store.upsertCount() != 1 {
		t.Errorf("UpsertSnapshot calls: want 1 (account 22 refreshed despite 21 failing), got %d", store.upsertCount())
	}
}

// TestSnapshotRefreshSweep_Tick_StoreError_NoFetch pins the selection
// failure path: a store error aborts the pass (logged at WARN by the
// caller) without touching the provider or the vault.
func TestSnapshotRefreshSweep_Tick_StoreError_NoFetch(t *testing.T) {
	store := &refreshSweepStoreStub{claimErr: errors.New("db unreachable")}
	fetcher := &sweepFetcher{}
	w := NewSnapshotRefreshSweepWorker(store, &mockCredentialVault{}, nil, map[string]AccountDetailsFetcher{models.PlatformYouTube: fetcher}, 0, newSweepTestLogger())

	w.tick(context.Background())

	if fetcher.calls() != 0 {
		t.Errorf("provider calls: want 0 on store error, got %d", fetcher.calls())
	}
	if store.upsertCount() != 0 {
		t.Errorf("upsert calls: want 0 on store error, got %d", store.upsertCount())
	}
}

// TestSnapshotRefreshSweep_Tick_NoPending_NoFetch pins the empty-cohort
// fast path: no selection → no provider traffic, no log noise.
func TestSnapshotRefreshSweep_Tick_NoPending_NoFetch(t *testing.T) {
	store, fetcher, w := newRefreshSweepHarness(t, nil, nil)

	w.tick(context.Background())

	if fetcher.calls() != 0 {
		t.Errorf("provider calls: want 0 for empty cohort, got %d", fetcher.calls())
	}
	if store.upsertCount() != 0 {
		t.Errorf("upsert calls: want 0 for empty cohort, got %d", store.upsertCount())
	}
}

// TestSnapshotRefreshSweep_Run_CtxCancel pins the shutdown contract:
// Run blocks until ctx is cancelled and returns ctx.Err() — the same
// shape the WorkerRegistry drain expects.
func TestSnapshotRefreshSweep_Run_CtxCancel(t *testing.T) {
	_, _, w := newRefreshSweepHarness(t, nil, nil)
	w.interval = time.Hour // block on the ticker, not a second tick

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run: want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on ctx cancel within 5s")
	}
}

// TestSnapshotRefreshSweep_LogsNeverContainTokenMaterial pins the
// redaction contract at the sweep surface: the worker logs only ids +
// classified errors, never raw token text or provider bodies.
func TestSnapshotRefreshSweep_LogsNeverContainTokenMaterial(t *testing.T) {
	pending := []repository.PendingSnapshotRefresh{
		{PlatformAccountID: 21, Platform: "youtube", PlatformUserID: "UC-one", Username: "one"},
	}
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	store, _, w := newRefreshSweepHarness(t, pending, func(accountID int64) error {
		return errors.New("token refresh failed (status 400): supersecret-google-body")
	})
	w.logger = logger

	w.tick(context.Background())

	out := buf.String()
	for _, forbidden := range []string{"supersecret", "ya29.tokenvalue", "refreshToken"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("sweep log leaked raw error material %q: %s", forbidden, out)
		}
	}
	if store.upsertCount() != 0 {
		t.Errorf("upsert calls: want 0 (token failed), got %d", store.upsertCount())
	}
}

// TestSnapshotRefreshSweep_ConcurrencyCapped proves the DoD concurrency
// cap: with more pending accounts than the semaphore, at most
// accountSnapshotRefreshConcurrency provider fetches run in flight at
// once. The probe counts the peak concurrent GetAccountDetails calls
// (the semaphore bounds the actual concurrent provider traffic).
func TestSnapshotRefreshSweep_ConcurrencyCapped(t *testing.T) {
	pending := make([]repository.PendingSnapshotRefresh, 0, 10)
	for i := int64(1); i <= 10; i++ {
		pending = append(pending, repository.PendingSnapshotRefresh{
			PlatformAccountID: i, Platform: "youtube", PlatformUserID: "UC-x", Username: "x",
		})
	}
	store := &refreshSweepStoreStub{pending: pending}

	base := &sweepFetcher{
		details: &models.AccountDetails{ResourceType: "channel", FetchedAt: time.Now()},
	}
	countingFetcher := &peakCountingFetcher{base: base}
	vault := &mockCredentialVault{
		renewFn: func(_ context.Context, accountID int64, tokenType string, refresher credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t", TokenType: tokenType}, nil
		},
	}
	w := NewSnapshotRefreshSweepWorker(
		store,
		vault,
		map[string]credentials.TokenRefresher{models.PlatformYouTube: func(_ context.Context, rt string) (*models.TokenData, error) {
			return &models.TokenData{AccessToken: "t", RefreshToken: rt}, nil
		}},
		map[string]AccountDetailsFetcher{models.PlatformYouTube: countingFetcher},
		0,
		newSweepTestLogger(),
	)

	w.tick(context.Background())

	if peak := countingFetcher.peak(); peak > accountSnapshotRefreshConcurrency {
		t.Errorf("peak concurrent provider fetches: want <= %d, got %d", accountSnapshotRefreshConcurrency, peak)
	}
	if countingFetcher.totalCalls() != 10 {
		t.Errorf("provider fetches: want 10 (all accounts refreshed), got %d", countingFetcher.totalCalls())
	}
}

// peakCountingFetcher wraps an AccountDetailsFetcher and records the
// peak number of concurrent GetAccountDetails calls (the semaphore cap).
type peakCountingFetcher struct {
	base    *sweepFetcher
	mu      sync.Mutex
	in      int
	peakVal int
	callsT  int
}

func (p *peakCountingFetcher) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	p.mu.Lock()
	p.in++
	if p.in > p.peakVal {
		p.peakVal = p.in
	}
	p.callsT++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.in--
		p.mu.Unlock()
	}()
	// Small sleep so concurrent goroutines overlap enough to be observed.
	time.Sleep(5 * time.Millisecond)
	return p.base.GetAccountDetails(ctx, accessToken, platformUserID)
}

func (p *peakCountingFetcher) peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peakVal
}

func (p *peakCountingFetcher) totalCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callsT
}
