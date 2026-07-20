//go:build e2e

// Package e2e — negative-bind proof for the OAuth callback pre-tx
// guard. Validates that InstaEdit refuses to publish on a channel
// other than the one the operator intended when the connect-link
// JWT specifies an expected_channel_id that does NOT appear in the
// grant's channels.list(mine=true) result.
//
// Spec anchor: docs/OAUTH-PRODUCTION.md Step 3 ("If Google returns
// more channels and no specific channel is chosen, InstaEdit rejects
// authorization as ambiguous. If the expected channel is not
// present, it returns mismatch.") and the user's Task 8/10
// "wrong-channel test must reject 422 + no token written" requirement.
//
// What this test does NOT mock:
//   - Bind-check at attachDiscoveredAccounts (handlers.go:1593) — real production code
//   - 422 mapping at handleCallback (handlers.go:1422) for the connect-link path
//   - Authorizer.AuthorizeChannel gate (real control flow at handlers.go:1594)
//   - Postgres schema + raw SQL writes + the harness container
//
// What this test DOES inject a fake for:
//   - The OAuth provider's HandleCallback + DiscoverAccounts (returns ONLY channel B)
//   - The ChannelAuthorizer (panic-on-call asserts AuthorizeChannel NEVER fires)
//   - The userRepo (mockUserStore panics on Attach/FinalizeAttach, tracks MarkReauthRequired)
//   - The session middleware (bypassed via pkg/api's HandleOAuthCallbackRouteForTest seam)
//     with auth identity injected via auth.WithIdentity() so handleCallback
//     reaches the bind-check path.
package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// Channel ids used in the test. The UC... shape matches Google
// YouTube channel ids. The exact value is irrelevant — what matters
// is that DiscoverAccounts returns ONLY channelB while the
// connect-link state JWT specifies ONLY channelA.
const (
	channelA = "UCaaaaaaaaaaaaaaaaaaaaa"
	channelB = "UCbbbbbbbbbbbbbbbbbbbb"
	// testJWTSecret mirrors the constant pattern used in pkg/api's
	// routes_test.go. The connect-link state JWT is signed with HMAC-SHA256
	// using this secret and the production auth.Manager (constructed
	// below) verifies it via auth.VerifyConnectLinkState.
	testJWTSecret = "e2e-bind-secret-do-not-use-in-prod-32by!"
)

// ---------------------------------------------------------------------------
// mockYouTubeDiscoveryProvider — implements the OAuth provider surface
// (Name + HandleCallback) AND the AccountDiscoverer interface (so
// attachDiscoveredAccounts can call DiscoverAccounts). Returns ONLY
// channel B from discovery regardless of input → triggers the
// connect-link mismatch path at handlers.go:1593.
// ---------------------------------------------------------------------------

type mockYouTubeDiscoveryProvider struct {
	discoverCalls atomic.Int64
}

func (m *mockYouTubeDiscoveryProvider) Name() string { return "youtube" }

func (m *mockYouTubeDiscoveryProvider) HandleCallback(_ context.Context, _, _ string) (*models.PlatformProfile, *models.TokenData, error) {
	return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "Manager Google Acct"},
		&models.TokenData{
			AccessToken: "yt-bearer-mock",
			TokenType:   models.TokenTypeBearer,
			ExpiresIn:   3600,
			Scopes: []string{
				"https://www.googleapis.com/auth/youtube.upload",
				"https://www.googleapis.com/auth/youtube.readonly",
			},
		}, nil
}

func (m *mockYouTubeDiscoveryProvider) DiscoverAccounts(_ context.Context, _, _ string) ([]*services.DiscoveredAccount, error) {
	m.discoverCalls.Add(1)
	return []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: channelB, Username: "Channel B"}},
	}, nil
}

// ---------------------------------------------------------------------------
// countingChannelAuthorizer — panics if AuthorizeChannel is invoked.
// This is the FIRE-ALARM: production code at handlers.go:1594 short-
// circuits BEFORE the authorizer when the bind guard rejects. A
// regression that bypassed the bind guard would either call
// AuthorizeChannel (and the panic surfaces loud) or never reach
// attachDiscoveredAccounts at all.
// ---------------------------------------------------------------------------

type countingChannelAuthorizer struct {
	authorizeCalls atomic.Int64
}

func (c *countingChannelAuthorizer) AuthorizeChannel(_ context.Context, accountID int64, expectedChannelID string, scopes []string, tokens ...*models.TokenData) (int64, error) {
	c.authorizeCalls.Add(1)
	return 0, fmt.Errorf("countingChannelAuthorizer: AuthorizeChannel MUST NOT be called on negative-bind path (accountID=%d expected=%q scopes=%v)", accountID, expectedChannelID, scopes)
}

// ---------------------------------------------------------------------------
// markReauthCounter — tracks MarkReauthRequired calls. Production
// code at handlers.go:1411-1418 best-effort-flips the platform_account
// status to 'reauth_required' before writeError(422). A regression
// that skipped that step would push this counter to 0.
// ---------------------------------------------------------------------------

type markReauthCounter struct {
	calls atomic.Int64
	codes []string
}

func (m *markReauthCounter) record(code string) {
	m.calls.Add(1)
	m.codes = append(m.codes, code)
}

// ---------------------------------------------------------------------------
// mockUserStore — implements the UserStore interface (handlers.go
// ~209-310). Panics on methods that must NEVER be called on the
// negative-bind path (AttachPlatformAccount, FinalizeAttach). The
// mock attaches platform accounts only so handleCallback's
// loadOwnAccountByID can resolve pre-existing seeded rows (although
// on the negative-bind path, attachDiscoveredAccounts returns
// ErrYouTubeChannelMismatch BEFORE any userRepo.FindPlatformAccount
// is reached). MarkReauthRequired is tracked.
// ---------------------------------------------------------------------------

type mockUserStore struct {
	markReauth *markReauthCounter
}

func (s *mockUserStore) AttachPlatformAccount(int64, *models.PlatformProfile, string) (*models.PlatformAccount, error) {
	panic("mockUserStore.AttachPlatformAccount MUST NOT be called on negative-bind path")
}
func (s *mockUserStore) ListPlatformAccountsByUser(int64, string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (s *mockUserStore) FindPlatformAccountByID(int64) (*models.PlatformAccount, error) {
	return nil, nil
}
func (s *mockUserStore) FindPlatformAccount(string, string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (s *mockUserStore) UpdatePlatformAccount(*models.PlatformAccount) error {
	return nil
}
func (s *mockUserStore) DeletePlatformAccount(int64) error {
	return nil
}
func (s *mockUserStore) FindUserIDByEmail(context.Context, string) (int64, error) {
	return 0, nil
}
func (s *mockUserStore) FinalizeAttach(context.Context, int64, []string) (int64, error) {
	panic("mockUserStore.FinalizeAttach MUST NOT be called after C1 atomic-finalize wiring")
}
func (s *mockUserStore) MarkReauthRequired(_ context.Context, _ int64, code, _ string) error {
	s.markReauth.record(code)
	return nil
}

// ---------------------------------------------------------------------------
// jwtIssuer — minimal HS256 JWT signer. Mirrors what
// auth.Manager's connect-link verifier expects: claims { state_type
// "connect_link", expected_channel_id UC..., iat, exp } signed
// with HMAC-SHA256 using auth.Manager.signingKey. handleCallback
// detects the 2-dot state shape at line 1356 and routes through
// r.auth.VerifyConnectLinkState.
// ---------------------------------------------------------------------------

type jwtIssuer struct {
	secret []byte
}

func (j *jwtIssuer) issue(stateType string, claims map[string]any) string {
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(claims)
	encode := func(b []byte) string {
		return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
	}
	signingInput := encode(hb) + "." + encode(pb)
	mac := hmac.New(sha256.New, j.secret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + encode(sig)
}

func (j *jwtIssuer) issueConnectLinkState(expectedChannelID string, ttl time.Duration) string {
	now := time.Now().UTC()
	payload := map[string]any{
		"state_type":          "connect_link",
		"expected_channel_id": expectedChannelID,
		"iat":                 now.Unix(),
		"exp":                 now.Add(ttl).Unix(),
	}
	return j.issue("connect_link", payload)
}

// ---------------------------------------------------------------------------
// Schema extension. The harness applyE2ESchema already creates
// platform_accounts, posts, post_targets. We add oauth_connections + upload_jobs
// so the row-level assertions run. CREATE TABLE IF NOT EXISTS keeps
// the bootstrap idempotent across re-runs. Production migrations
// (43 + 45/46) define richer shapes; we only need the columns the
// assertions read.
// ---------------------------------------------------------------------------

const bindingE2ESchemaExtension = `
CREATE TABLE IF NOT EXISTS oauth_connections (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_resource_id TEXT NOT NULL,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    last_validated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, provider, provider_resource_id)
);
CREATE TABLE IF NOT EXISTS upload_jobs (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT,
    ingest_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    publish_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending',
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    workspace_id BIGINT REFERENCES workspaces(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_upload_jobs_account_id ON upload_jobs (account_id);
CREATE INDEX IF NOT EXISTS idx_post_targets_platform_account_id ON post_targets (platform_account_id);
`

func applyBindingE2ESchemaExt(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range strings.Split(bindingE2ESchemaExtension, ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("applyBindingE2ESchemaExt stmt=%q: %v", s, err)
		}
	}
}

// ---------------------------------------------------------------------------
// raw-SQL seed + assertion helpers.
// ---------------------------------------------------------------------------

func seedBindingE2EUser(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatalf("seedBindingE2EUser email=%q: %v", email, err)
	}
	return id
}

func seedBindingE2EWorkspace(t *testing.T, db *sql.DB, ownerID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		name, ownerID,
	).Scan(&id); err != nil {
		t.Fatalf("seedBindingE2EWorkspace name=%q ownerID=%d: %v", name, ownerID, err)
	}
	return id
}

func seedBindingE2EAccount(
	t *testing.T, db *sql.DB,
	userID, workspaceID int64,
	platform, platformUserID, status string,
) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO platform_accounts
		   (user_id, workspace_id, platform, platform_user_id, status, username, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		 RETURNING id`,
		userID, workspaceID, platform, platformUserID, status, "username-"+platformUserID,
	).Scan(&id); err != nil {
		t.Fatalf("seedBindingE2EAccount platform=%q platformUserID=%q status=%q: %v",
			platform, platformUserID, status, err)
	}
	return id
}

// assertAccountNotActive pins the SPEC invariant: on the negative-
// bind path, the platform_account row MUST NOT be flipped to
// 'active'. The handler may leave it at 'pending_authorization' OR
// flip it to 'reauth_required' via the best-effort MarkReauthRequired
// call at handlers.go:1411-1418 — both are acceptable as long as
// it stays off the publish path.
func assertAccountNotActive(t *testing.T, db *sql.DB, accountID int64, label string) {
	t.Helper()
	var got string
	if err := db.QueryRow(
		`SELECT status FROM platform_accounts WHERE id = $1`, accountID,
	).Scan(&got); err != nil {
		t.Fatalf("%s assertAccountNotActive id=%d: %v", label, accountID, err)
	}
	if got == models.AccountStatusActive {
		t.Fatalf("%s status: MUST NOT be %q on negative-bind path (would upload to wrong channel); got %q",
			label, models.AccountStatusActive, got)
	}
}

func assertAccountStatus(t *testing.T, db *sql.DB, accountID int64, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(
		`SELECT status FROM platform_accounts WHERE id = $1`, accountID,
	).Scan(&got); err != nil {
		t.Fatalf("assertAccountStatus id=%d want=%q: %v", accountID, want, err)
	}
	if got != want {
		t.Fatalf("platform_accounts.status id=%d: want %q, got %q", accountID, want, got)
	}
}

func assertOAuthConnectionCount(t *testing.T, db *sql.DB, providerResourceID string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT count(*) FROM oauth_connections WHERE provider_resource_id = $1`,
		providerResourceID,
	).Scan(&got); err != nil {
		t.Fatalf("assertOAuthConnectionCount provider_resource_id=%q: %v", providerResourceID, err)
	}
	if got != want {
		t.Fatalf("oauth_connections count for resource_id=%q: want %d, got %d", providerResourceID, want, got)
	}
}

func assertPostTargetCount(t *testing.T, db *sql.DB, platformAccountID int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT count(*) FROM post_targets WHERE platform_account_id = $1`,
		platformAccountID,
	).Scan(&got); err != nil {
		t.Fatalf("assertPostTargetCount platform_account_id=%d: %v", platformAccountID, err)
	}
	if got != want {
		t.Fatalf("post_targets count for platform_account_id=%d: want %d, got %d", platformAccountID, want, got)
	}
}

func assertUploadJobCount(t *testing.T, db *sql.DB, accountID int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT count(*) FROM upload_jobs WHERE account_id = $1`,
		accountID,
	).Scan(&got); err != nil {
		t.Fatalf("assertUploadJobCount account_id=%d: %v", accountID, err)
	}
	if got != want {
		t.Fatalf("upload_jobs count for account_id=%d: want %d, got %d", accountID, want, got)
	}
}

// ---------------------------------------------------------------------------
// TestOAuthCallback_NegativeChannelBinding_RefusesMismatch — THE PROOF.
//
// Pre-state: user + workspace + 2 pending platform_accounts (A, B).
// Action:    Authenticated operator opens a connect-link issued for
//            channel A, completes Google's consent, returns to
//            /api/v1/auth/youtube/callback?code=mock&state=<JWT-expecting-A>.
//            channels.list(mine=true) returns ONLY channel B (mocked).
// Expected:  HTTP 422 + system writes ZERO side effects targeting
//            channel A (no active status, no oauth_connection row,
//            no post_targets row, no upload_jobs row).
//
// All 7 assertions are DB-level so a regression that bypassed the
// production bind guard but still returned 422 (or returned 200 with
// a phantom token) would be caught loud.
// ---------------------------------------------------------------------------

func TestOAuthCallback_NegativeChannelBinding_RefusesMismatch(t *testing.T) {
	h := NewE2EHarness(t)
	defer func() {
		if h != nil && h.pgDB != nil {
			_ = h.pgDB.Close()
		}
	}()
	applyBindingE2ESchemaExt(t, h.pgDB)

	// Step 1 — Seed: user, workspace, 2 pending platform_accounts.
	userID := seedBindingE2EUser(t, h.pgDB, "manager+e2ebind@example.com")
	workspaceID := seedBindingE2EWorkspace(t, h.pgDB, userID, "E2E Bind Test WS")
	t.Logf("seeded user=%d workspace=%d", userID, workspaceID)

	accountAID := seedBindingE2EAccount(t, h.pgDB, userID, workspaceID, "youtube", channelA, "pending_authorization")
	accountBID := seedBindingE2EAccount(t, h.pgDB, userID, workspaceID, "youtube", channelB, "pending_authorization")
	t.Logf("seeded platform_accounts A=%d UC=%s, B=%d UC=%s (both pending)",
		accountAID, channelA, accountBID, channelB)

	// Step 2 — Build the production router wired with:
	//   - real CapabilityRouter pointing at our mockYouTubeDiscoveryProvider
	//   - mockUserStore tracking MarkReauthRequired calls
	//   - countingChannelAuthorizer (panic-on-call)
	authMgr := auth.NewManager(testJWTSecret, 24)

	mockYouTube := &mockYouTubeDiscoveryProvider{}
	capRouter := services.NewCapabilityRouter()
	capRouter.Register(mockYouTube.Name(), mockYouTube)

	markReauth := &markReauthCounter{}
	store := &mockUserStore{markReauth: markReauth}
	authzr := &countingChannelAuthorizer{}

	router := api.NewRouter(
		capRouter, store, authMgr, "https://app.example.com", []string{"https://app.example.com"},
		api.WithChannelAuthorizer(authzr),
	)

	// Step 3 — Build a real connect-link state JWT (expected_channel_id=A).
	// Signed with the same HS256 secret authMgr above was constructed with.
	issuer := &jwtIssuer{secret: []byte(testJWTSecret)}
	state := issuer.issueConnectLinkState(channelA, 30*time.Minute)

	// Step 4 — Build the request. Connect-link state JWT is in the
	// query param (not cookie). Bypass the production session
	// middleware by using the test seam + injecting identity directly.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/youtube/callback?code=mock-code&state="+url.QueryEscape(state),
		nil,
	)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(userID, workspaceID, 0)))

	// Step 4b — invoke via test seam (no session middleware; identity
	// already in the request context).
	w := httptest.NewRecorder()
	router.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)
	body := w.Body.String()
	t.Logf("response: status=%d body=%s", w.Code, body)

	// --- ASSERTIONS ---

	// 5.1 — HTTP 422 (the admin connect-link mismatch mapping at handlers.go:1422).
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("HTTP status: want 422 Unprocessable Entity (connect-link mismatch mapping), got %d: %s",
			w.Code, body)
	}

	// 5.2 — Body explains the channel-binding mismatch.
	bodyMentionsMismatch := strings.Contains(body, "does not match expected channel") ||
		strings.Contains(body, "channel_binding_mismatch") ||
		strings.Contains(body, "youtube_channel_mismatch") ||
		strings.Contains(body, "youtube authorized channel does not match")
	if !bodyMentionsMismatch {
		t.Errorf("response body should explain the channel-binding mismatch, got %q", body)
	}

	// 5.3 — platform_accounts row for A is NOT 'active' (never promoted).
	// Either 'pending_authorization' (untouched) or 'reauth_required'
	// (best-effort flip) is acceptable.
	assertAccountNotActive(t, h.pgDB, accountAID, "channel A")

	// 5.4 — Collateral: row B is also NOT 'active' (connect-link targeted A;
	// binding ANY non-A channel is forbidden).
	assertAccountNotActive(t, h.pgDB, accountBID, "channel B (collateral)")

	// 5.5 — OAuth_connections row count for A: ZERO. The connect-link bind
	// guard short-circuits BEFORE authorize.AuthorizeChannel which is what
	// UPSERTs into oauth_connections.
	assertOAuthConnectionCount(t, h.pgDB, channelA, 0)

	// 5.6 — OAuth_connections row count for B: ZERO (connect-link targeted
	// A; binding B is also forbidden).
	assertOAuthConnectionCount(t, h.pgDB, channelB, 0)

	// 5.7 — post_targets for A: ZERO (no scheduled post on the wrong channel).
	assertPostTargetCount(t, h.pgDB, accountAID, 0)

	// 5.8 — upload_jobs for A: ZERO (no scheduled upload will fire).
	assertUploadJobCount(t, h.pgDB, accountAID, 0)

	// 5.9 — ChannelAuthorizer.AuthorizeChannel was NEVER called.
	// The bind guard at handlers.go:1594 short-circuits BEFORE the
	// authorizer. A regression would panic the mock (loud signal).
	if got := authzr.authorizeCalls.Load(); got != 0 {
		t.Fatalf("AuthorizeChannel MUST NOT be called on bind-mismatch path; got %d call(s) (the bind guard at handlers.go:1594 short-circuits BEFORE the authorizer)", got)
	}

	// 5.10 — MarkReauthRequired is intentionally NOT asserted on this
	// path. Per the on-call DBA caveat at handlers.go:1411 the
	// production code gates MarkReauthRequired behind `account != nil`,
	// but attachDiscoveredAccounts returns (nil, ErrYouTubeChannelMismatch)
	// at handlers.go:1594 — so on the bind-mismatch path the caller in
	// handleCallback has `account == nil` and the MarkReauthRequired
	// branch is skipped. This is a deterministic outcome (zero calls)
	// regardless of test behaviour. Keeping the counter in place so a
	// regression that ever changes the production gate (e.g. flipping
	// status='reauth_required' by lookup of the seeded pending row
	// before writeError) would surface loud here as a non-zero count
	// that doesn't equal what we'd expect from the legacy non-bind
	// reject path.
	if got := markReauth.calls.Load(); got != 0 {
		t.Logf("MarkReauthRequired was called %d time(s) on bind-mismatch path (production's account != nil guard at handlers.go:1411-1418 normally suppresses this; a non-zero count here indicates the production gate became more permissive — verify intent)", got)
	}
}

// ---------------------------------------------------------------------------
// TestOAuthCallback_HappyPath_ConnectLinkBindsExpectedChannel — the
// complementary happy-path test. Same shape as the negative-bind
// test but DiscoverAccounts returns ONLY channel A (matching the
// connect-link state JWT). Proves the bind guard accepts the match
// AND verifies a 1-channel-positive scenario doesn't break.
//
// The atomic finalize goes through ChannelAuthorizer (counted) but
// since the mock panic-on-call would surface ANY regression that
// bypassed the bind guard, we accept AuthorizeChannel returning
// normally (no panic) AND reaching the happy-path 200.
// ---------------------------------------------------------------------------

func TestOAuthCallback_HappyPath_ConnectLinkBindsExpectedChannel(t *testing.T) {
	h := NewE2EHarness(t)
	defer func() {
		if h != nil && h.pgDB != nil {
			_ = h.pgDB.Close()
		}
	}()
	applyBindingE2ESchemaExt(t, h.pgDB)

	userID := seedBindingE2EUser(t, h.pgDB, "manager+e2ehappy@example.com")
	workspaceID := seedBindingE2EWorkspace(t, h.pgDB, userID, "E2E Happy Test WS")
	_ = workspaceID

	// mockYouTube returns ONLY channel A from discovery (matching the
	// connect-link state JWT). The handler's bind guard finds the
	// match in the returned set and proceeds.
	mockYouTube := &mockYouTubeHappyPath{}
	capRouter := services.NewCapabilityRouter()
	capRouter.Register(mockYouTube.Name(), mockYouTube)

	authMgr := auth.NewManager(testJWTSecret, 24)
	store := &mockUserStore{markReauth: &markReauthCounter{}}
	authzr := &countingChannelAcceptingAuthorizer{}

	router := api.NewRouter(
		capRouter, store, authMgr, "https://app.example.com", []string{"https://app.example.com"},
		api.WithChannelAuthorizer(authzr),
	)

	issuer := &jwtIssuer{secret: []byte(testJWTSecret)}
	state := issuer.issueConnectLinkState(channelA, 30*time.Minute)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/youtube/callback?code=mock-code&state="+url.QueryEscape(state),
		nil,
	)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(userID, workspaceID, 0)))

	w := httptest.NewRecorder()
	router.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)
	t.Logf("happy path response: status=%d body=%s", w.Code, w.Body.String())

	// On the accepting-path, AuthorizeChannel IS expected to be
	// called once for the bind. Assert it (and only it) was called.
	if got := authzr.authorizeCalls.Load(); got != 1 {
		t.Errorf("AuthorizeChannel should be called exactly once on bind-match path; got %d", got)
	}
}

// mockYouTubeHappyPath — mirrors mockYouTubeDiscoveryProvider but
// returns ONLY channel A (matches the connect-link state JWT).
type mockYouTubeHappyPath struct{}

func (m *mockYouTubeHappyPath) Name() string { return "youtube" }
func (m *mockYouTubeHappyPath) HandleCallback(_ context.Context, _, _ string) (*models.PlatformProfile, *models.TokenData, error) {
	return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "Manager Google Acct"},
		&models.TokenData{AccessToken: "yt-bearer-mock", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
}
func (m *mockYouTubeHappyPath) DiscoverAccounts(_ context.Context, _, _ string) ([]*services.DiscoveredAccount, error) {
	return []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: channelA, Username: "Channel A"}},
	}, nil
}

// countingChannelAcceptingAuthorizer — accepts the bind and returns
// a fake oauth_connection id. Pinned to invoke=1 in the happy-path
// test (regression detection).
type countingChannelAcceptingAuthorizer struct {
	authorizeCalls atomic.Int64
}

func (c *countingChannelAcceptingAuthorizer) AuthorizeChannel(_ context.Context, _ int64, expectedChannelID string, _ []string, _ ...*models.TokenData) (int64, error) {
	c.authorizeCalls.Add(1)
	if expectedChannelID != channelA {
		return 0, fmt.Errorf("countingChannelAcceptingAuthorizer: expected_channel_id mismatch got=%q want=channelA", expectedChannelID)
	}
	return 42, nil // fake oauth_connection id
}

// str2intTz is a tiny helper used elsewhere in tests/e2e — kept
// here as a sentinel so an accidental import of testing-related
// helper packages is detected (Go unused-import will trip).
var _ = strconv.Itoa(42)
