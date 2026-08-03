package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// sweepStoreStub is a TokenRefreshSweepStore with a fixed grant list
// (or error) and a recorder for the horizon the worker passed.
type sweepStoreStub struct {
	grants     []models.DormantRefreshGrant
	err        error
	gotHorizon int
	calls      int
}

func (s *sweepStoreStub) ListDormantRefreshGrants(_ context.Context, horizonDays int) ([]models.DormantRefreshGrant, error) {
	s.calls++
	s.gotHorizon = horizonDays
	if s.err != nil {
		return nil, s.err
	}
	return s.grants, nil
}

// newSweepTestLogger discards log output so the test surface stays
// clean (assertions are on behaviour, not log text).
func newSweepTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// testSweepHarness bundles the worker + fakes used across tests.
type testSweepHarness struct {
	store  *sweepStoreStub
	vault  *mockCredentialVault
	worker *TokenRefreshSweepWorker
	// renewCalls records every Renew invocation: accountID, tokenType,
	// and the refresher closure (so the test can invoke it to prove the
	// RIGHT provider adapter was wired).
	mu         sync.Mutex
	renewCalls []renewCall
}

type renewCall struct {
	accountID int64
	tokenType string
	refresher credentials.TokenRefresher
	// probeResult is the AccessToken the captured refresher returned
	// when invoked with a probe string during capture. Used to assert
	// the RIGHT provider adapter was selected (funcs can't be
	// compared in Go, so identity is proven by behaviour).
	probeResult string
}

// newSweepHarness wires a worker with the given grants and a
// configurable renew outcome. refreshers covers youtube + google-drive
// by default; overrides via the returned harness.
func newSweepHarness(t *testing.T, grants []models.DormantRefreshGrant, renewOutcome func(accountID int64) error) *testSweepHarness {
	t.Helper()
	// Declare h BEFORE the initializer: the renewFn closure references
	// it, and a variable's scope only begins after its own := statement.
	var h *testSweepHarness
	h = &testSweepHarness{
		store: &sweepStoreStub{grants: grants},
		vault: &mockCredentialVault{
			renewFn: func(_ context.Context, accountID int64, tokenType string, refresher credentials.TokenRefresher) (*models.OAuthToken, error) {
				call := renewCall{accountID: accountID, tokenType: tokenType, refresher: refresher}
				// Probe the captured refresher so the test can assert the
				// RIGHT provider adapter was selected by behaviour.
				if refresher != nil {
					if td, probeErr := refresher(context.Background(), "probe"); probeErr == nil && td != nil {
						call.probeResult = td.AccessToken
					}
				}
				h.mu.Lock()
				h.renewCalls = append(h.renewCalls, call)
				h.mu.Unlock()
				if renewOutcome != nil {
					if err := renewOutcome(accountID); err != nil {
						return nil, err
					}
				}
				return &models.OAuthToken{AccessToken: "fresh", TokenType: tokenType}, nil
			},
		},
	}
	// Two DISTINCT closures, distinguishable by the AccessToken they
	// return, so the test can prove each account got its provider's
	// adapter (Go funcs can't be compared directly).
	refreshers := map[string]credentials.TokenRefresher{
		models.PlatformYouTube: func(_ context.Context, refreshToken string) (*models.TokenData, error) {
			return &models.TokenData{AccessToken: "youtube-fresh", RefreshToken: refreshToken}, nil
		},
		models.PlatformGoogleDrive: func(_ context.Context, refreshToken string) (*models.TokenData, error) {
			return &models.TokenData{AccessToken: "drive-fresh", RefreshToken: refreshToken}, nil
		},
	}
	h.worker = NewTokenRefreshSweepWorker(h.store, h.vault, refreshers, 0, 0, newSweepTestLogger())
	return h
}

func TestTokenRefreshSweep_Tick_RenewsWiredProvidersAndSkipsOthers(t *testing.T) {
	grants := []models.DormantRefreshGrant{
		{OAuthConnectionID: 1, PlatformAccountID: 10, Provider: models.PlatformYouTube},
		{OAuthConnectionID: 2, PlatformAccountID: 20, Provider: models.PlatformGoogleDrive},
		{OAuthConnectionID: 3, PlatformAccountID: 30, Provider: "tiktok"}, // no refresher wired → skipped
	}
	h := newSweepHarness(t, grants, nil)

	h.worker.tick(context.Background())

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.renewCalls) != 2 {
		t.Fatalf("Renew calls: want 2 (youtube + google-drive), got %d: %+v", len(h.renewCalls), h.renewCalls)
	}
	// YouTube grant renewed via the canonical bearer type (RenewYouTubeToken).
	if h.renewCalls[0].accountID != 10 || h.renewCalls[0].tokenType != models.TokenTypeBearer {
		t.Errorf("youtube renew: want (account=10, type=bearer), got (%d, %q)", h.renewCalls[0].accountID, h.renewCalls[0].tokenType)
	}
	// Drive grant renewed via bearer.
	if h.renewCalls[1].accountID != 20 || h.renewCalls[1].tokenType != models.TokenTypeBearer {
		t.Errorf("drive renew: want (account=20, type=bearer), got (%d, %q)", h.renewCalls[1].accountID, h.renewCalls[1].tokenType)
	}
	// Provider adapter selection proven by behaviour: the youtube grant
	// was passed the YOUTUBE refresher (probe → "youtube-fresh") and
	// the drive grant the DRIVE refresher (probe → "drive-fresh").
	if h.renewCalls[0].probeResult != "youtube-fresh" {
		t.Errorf("youtube renew: refresher probe want youtube-fresh, got %q", h.renewCalls[0].probeResult)
	}
	if h.renewCalls[1].probeResult != "drive-fresh" {
		t.Errorf("drive renew: refresher probe want drive-fresh, got %q", h.renewCalls[1].probeResult)
	}
	// Defaults applied: 24h interval, 120d horizon passed to the store.
	if h.store.gotHorizon != DefaultTokenRefreshSweepHorizonDays {
		t.Errorf("store horizon: want %d, got %d", DefaultTokenRefreshSweepHorizonDays, h.store.gotHorizon)
	}
	if h.worker.interval != DefaultTokenRefreshSweepInterval {
		t.Errorf("worker interval: want %v, got %v", DefaultTokenRefreshSweepInterval, h.worker.interval)
	}
}

// TestTokenRefreshSweep_Tick_InvalidGrantFailureIsolated pins the
// dead-grant path: a youtube grant whose renewal surfaces
// credentials.ErrInvalidGrant (the exact production shape from
// RefreshOAuthToken) fails through RenewYouTubeToken → logged, and the
// sweep continues with the drive grant. No panic, no abort.
func TestTokenRefreshSweep_Tick_InvalidGrantFailureIsolated(t *testing.T) {
	grants := []models.DormantRefreshGrant{
		{OAuthConnectionID: 1, PlatformAccountID: 10, Provider: models.PlatformYouTube},
		{OAuthConnectionID: 2, PlatformAccountID: 20, Provider: models.PlatformGoogleDrive},
	}
	h := newSweepHarness(t, grants, func(accountID int64) error {
		if accountID == 10 {
			return fmt.Errorf("youtube refresh failed (status 400): %w", credentials.ErrInvalidGrant)
		}
		return nil
	})

	h.worker.tick(context.Background()) // must not panic

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.renewCalls) != 2 {
		t.Fatalf("Renew calls: want 2 (both attempted despite one failure), got %d", len(h.renewCalls))
	}
	// RenewYouTubeToken classifies the sentinel: the youtube grant's
	// bearer renewal was attempted exactly ONCE (no legacy fallback for
	// invalid_grant), proving the typed sentinel path is exercised.
	if h.renewCalls[0].accountID != 10 || h.renewCalls[0].tokenType != models.TokenTypeBearer {
		t.Errorf("youtube renew: want (account=10, type=bearer), got (%d, %q)", h.renewCalls[0].accountID, h.renewCalls[0].tokenType)
	}
	// The drive grant still renewed after the youtube failure.
	if h.renewCalls[1].accountID != 20 {
		t.Errorf("drive renew: want account 20 to run after youtube failure, got %d", h.renewCalls[1].accountID)
	}
}

// TestTokenRefreshSweep_Tick_StoreError_NoRenew pins the selection
// failure path: a store error aborts the pass (logged at WARN by the
// caller) without touching the vault.
func TestTokenRefreshSweep_Tick_StoreError_NoRenew(t *testing.T) {
	h := newSweepHarness(t, nil, nil)
	h.store.err = errors.New("db unreachable")

	h.worker.tick(context.Background())

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.renewCalls) != 0 {
		t.Errorf("Renew calls: want 0 on store error, got %d", len(h.renewCalls))
	}
}

// TestTokenRefreshSweep_Tick_NoGrants_NoRenew pins the empty-cohort
// fast path: no selection → no vault traffic, no log noise.
func TestTokenRefreshSweep_Tick_NoGrants_NoRenew(t *testing.T) {
	h := newSweepHarness(t, nil, nil)

	h.worker.tick(context.Background())

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.renewCalls) != 0 {
		t.Errorf("Renew calls: want 0 for empty cohort, got %d", len(h.renewCalls))
	}
	if h.store.gotHorizon != DefaultTokenRefreshSweepHorizonDays {
		t.Errorf("store horizon: want default %d, got %d", DefaultTokenRefreshSweepHorizonDays, h.store.gotHorizon)
	}
}

// TestTokenRefreshSweep_Run_CtxCancel pins the shutdown contract:
// Run blocks until ctx is cancelled and returns ctx.Err() — the same
// shape the WorkerRegistry drain expects.
func TestTokenRefreshSweep_Run_CtxCancel(t *testing.T) {
	h := newSweepHarness(t, nil, nil)
	// Huge interval so the loop blocks on the ticker, not a second tick.
	h.worker.interval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.worker.Run(ctx) }()
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

// TestTokenRefreshSweep_LogsNeverContainTokenMaterial pins the
// redaction contract at the sweep surface: the worker logs only ids +
// classified errors — a renew error carrying raw provider text must
// be replaced by the credentials package's typed classification
// (which contains no token bytes).
func TestTokenRefreshSweep_LogsNeverContainTokenMaterial(t *testing.T) {
	grants := []models.DormantRefreshGrant{
		{OAuthConnectionID: 1, PlatformAccountID: 10, Provider: models.PlatformYouTube},
	}
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := newSweepHarness(t, grants, func(accountID int64) error {
		return fmt.Errorf("youtube refresh failed (status 400): %w", credentials.ErrInvalidGrant)
	})
	h.worker.logger = logger

	h.worker.tick(context.Background())

	out := buf.String()
	for _, forbidden := range []string{"invalid_grant", "supersecrettoken"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("sweep log leaked raw error material %q: %s", forbidden, out)
		}
	}
	if !strings.Contains(out, "renew failed") {
		t.Errorf("expected a renew-failed WARN line, got: %s", out)
	}
}
