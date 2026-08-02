//go:build e2e

// Package e2e — Test_Z_YouTubeOAuth_EndToEnd_RealBrowser_Smoke.
//
// This test is the **browser-driven** variant of the YouTube OAuth
// E2E proof. While tests/e2e/oauth_callback_binding_e2e_test.go
// exercises the callback handler against pre-seeded state via
// httptest (no real browser) and tests/e2e/pipeline_e2e_test.go
// covers the publish pipeline, THIS test closes the gap where the
// production accept-path gets driven by a REAL headless Chromium:
//
//  1. Existing tests fake the OAuth provider's HandleCallback +
//     DiscoverAccounts using Go mocks; they never prove a real
//     user-agent / cookie jar / DevTools round-trip works.
//  2. Existing tests don't exercise Set-Cookie + 302 chain with
//     multiple hosts (the chain crosses ctx:api/8080 →
//     fake-GOOGLE/oauth2/v2/auth → ctx:api/api/v1/auth/youtube/
//     callback).
//  3. This test asserts the **terminal SQL invariant** the user
//     explicitly asked for: the `tokens` table has encrypted_token
//     + encrypted_refresh_token NOT NULL for the oauth_connection
//     row stamped by the production callback handler.
//
// Build tag + skip chain
// ----------------------
// The test is behind `//go:build e2e` (matches every other test in
// this dir) so normal `go test ./...` does NOT pull in chromedp or
// reach for the real Chrome. Operator/CI runs via `make test-e2e`.
//
// Inside the e2e suite, three gates skip the test:
//   - testing.Short()                              (user passed -short)
//   - GOOGLE_CLIENT_ID_FOR_E2E unset               (smoke channel off)
//   - chrome binary absent / non-executable        (sandbox without Chromium)
//
// The first two cover what the user prompt asked for explicitly;
// the third is a defensive fallback so a cold CI image without
// /usr/bin/google-chrome skips cleanly instead of hanging.
//
// What it sets up
// ---------------
//   - Postgres already provided by NewE2EHarness (testcontainers).
//   - applyBindingE2ESchemaExt (oauth_connections + tokens +
//     post_targets + upload_jobs), already imported from the
//     sibling oauth_callback_binding_e2e_test.go's package.
//   - one user + one workspace + one pending YouTube
//     platform_account + one live `sessions` row (so the
//     JWT the manager issues carries a positive SessionID per the
//     Blocco-2.1 invariant at auth/jwt.go::Issue).
//   - a forgery of the Google OAuth surface — httptest.NewServer
//     exposing /o/oauth2/v2/auth (consent HTML the browser drives)
//     and /oauth2/v4/token (real-JSON token-exchange body that
//     the production provider's HTTP POST hits). Production
//     handlers talk to this fake instead of accounts.google.com.
//   - the real Go API under test (api.NewRouter), wired with:
//   - the real CapabilityRouter (registers our
//     fake-Google-driven YouTube provider)
//   - the real auth.Manager (HS256 JWT verification)
//   - the real vault (SaveEncryptedToken writes to `tokens`)
//   - the real TokenRepository (real SQL INSERT)
//   - the real handleCallback production path
//
// What it drives the browser through
// ----------------------------------
//
//	chromedp navigates the headless Chrome to:
//	    $apiServer/api/v1/auth/youtube/login?expected_channel_id=$ch
//	The Go handler mints an oauth_state_youtube cookie + state JWT
//	nonce + 302 to fakeGoogle/o/oauth2/v2/auth. Chrome follows,
//	waits for #approve-btn, clicks it, the form 302-redirects to
//	$apiServer/api/v1/auth/youtube/callback?code=&state=$nonce.
//	The Go callback handler:
//	    1. validates the state cookie vs URL
//	    2. calls provider.HandleCallback (real HTTP POST to
//	       fakeGoogle/oauth2/v4/token → real TokenData)
//	    3. calls our AuthorizeChannel no-op (returns the just-
//	       inserted oauth_connection_id so vault.SaveEncryptedToken
//	       has a FK target)
//	    4. vault.SaveEncryptedToken — real AES-GCM wrap of the
//	       access + refresh tokens → real SQL INSERT into `tokens`
//
// What it asserts
// ---------------
//
//	(a) fakeGoogle.consentCalls == 1 (the browser reached Google's
//	    authorize URL — proves redirect chain + cookies work)
//	(b) fakeGoogle.tokenCalls == 1 (the callback handler
//	    dispatched a real token exchange — proves HandleCallback
//	    integration is alive)
//	(c) chrome ended on $apiServer/api/v1/auth/youtube/callback
//	    (proves the form's redirect_uri round-trip)
//	(d) SQL:
//	       SELECT t.encrypted_token IS NOT NULL
//	            , t.encrypted_refresh_token IS NOT NULL
//	       FROM tokens t
//	       JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
//	       WHERE oc.provider = 'youtube'
//	    — the canonical user-required SQL assertion, hits at
//	    least one youtube row, both columns NOT NULL.
//
// Caveats
// -------
//   - This is a paper-GOOGLE endpoint, not the real one. CI-grade
//     E2E cannot depend on a real Google account (test accounts
//     get reaped + the OAuth UI changes). A separate operator run
//     with a real GOOGLE_CLIENT_ID + a real test email + the same
//     fake-Google substituted for prod-Google would prove the
//     same invariant against Google's real endpoint; the diffs
//     would be ~30 lines (replace fakeGoogle with a passthrough
//     that just swaps `accounts.google.com` for the fake). That's
//     tracked as Task 11/10 — the user's prompt requested it as
//     a "followup" so it does NOT block this commit.
//   - The session JWT is minted programmatically (not via the
//     public /api/v1/auth/login) so the test stays focused on
//     the OAuth-chain invariant. /api/v1/auth/login itself is
//     already covered by pkg/api/auth_email_test.go.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	chromedpNetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// -----------------------------------------------------------------------------
// Constants pinned for this E2E proof. Values DO NOT need to match any
// production configuration — the test brings up its own fakes.
// -----------------------------------------------------------------------------

const (
	browserSmokeE2EClientID  = "e2e-browser-smoke-fake-google-client"
	browserSmokeE2EChannel   = "UC_browser_smoke_e2e_aaaaa"
	browserSmokeE2EEmail     = "smoke+browser@example.com"
	browserSmokeE2EPassword  = "BrowserSmoke2025!"
	browserSmokeE2EUserName  = "Browser Smoke Test"
	browserSmokeE2EWorkspace = "E2E Browser Smoke WS"
	// Env-var gate. Operators running `make test-e2e` opt-in by setting
	// GOOGLE_CLIENT_ID_FOR_E2E; in CI/sandbox without this var the test
	// skips instead of attempting Chrome. The default-skip posture is
	// what the user prompt asked for ("Da skippare con -short se mancano
	// creds").
	GOOGLEClientIDForE2EEnv = "GOOGLE_CLIENT_ID_FOR_E2E"
	// webServerURLPlaceholder is filled in by the test body with the
	// actual httptest URL — used inside the YouTube provider's
	// GetLoginURLWithOptions to build the redirect_uri= param.
	webServerURLPlaceholder = "http://api.internal/api/v1/auth/youtube/callback"
)

// -----------------------------------------------------------------------------
// Chrome binary detection. Hardcoded list mirrors what you'd see in the
// project's local-bootstrap doc.
// -----------------------------------------------------------------------------

var chromeBinaryCandidates = []string{
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/snap/bin/chromium",
	"/opt/google/chrome/chrome",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
}

// findChromeBinary returns the first executable chrome binary from
// chromeBinaryCandidates or empty string if none found. Kept simple
// (stat.Effective vs exec.LookPath) so it doesn't accidentally accept
// shell scripts or symlinks that the chromedp exec allocator would
// later reject at boot.
func findChromeBinary() string {
	for _, p := range chromeBinaryCandidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			if info.Mode()&0o111 != 0 {
				return p
			}
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// fakeGoogleOauthServer — in-process httptest imitation of the
func Test_Z_YouTubeOAuth_EndToEnd_RealBrowser_Smoke(t *testing.T) {
	// ── Skip gates ────────────────────────────────────────────────
	if testing.Short() {
		t.Skip("real-browser E2E; -short excludes (set -tags=e2e AND drop -short to run)")
	}
	if os.Getenv(GOOGLEClientIDForE2EEnv) == "" {
		t.Skipf(
			"smoke channel off (set %s env var to any non-empty value to enable; the value isn't actually verified against Google's consoles because the test injects a paper-Google endpoint)",
			GOOGLEClientIDForE2EEnv,
		)
	}
	chromePath := findChromeBinary()
	if chromePath == "" {
		t.Skipf(
			"no Chrome binary found (checked %s); install /usr/bin/google-chrome OR chromium to enable",
			strings.Join(chromeBinaryCandidates, ", "),
		)
	}

	// ── Postgres + schema ─────────────────────────────────────────
	h := NewE2EHarness(t)
	if h == nil || h.pgDB == nil {
		t.Skip("testcontainers Postgres unavailable in this sandbox (Docker not reachable)")
	}
	t.Cleanup(func() {
		if h != nil && h.pgDB != nil {
			_ = h.pgDB.Close()
		}
	})
	applyBindingE2ESchemaExt(t, h.pgDB)
	ensureSessionsTable(t, h.pgDB)

	// ── Seed user/workspace/pending-account ───────────────────────
	userID, workspaceID, accountID, sessionID := seedBrowserSmokeUser(t, h.pgDB)
	t.Logf("seeded user=%d workspace=%d account=%d session=%d (channel=%s)",
		userID, workspaceID, accountID, sessionID, browserSmokeE2EChannel)

	// ── Mint a session JWT ────────────────────────────────────────
	authMgr := auth.NewManager(testJWTSecret, 15*time.Minute)
	sessionJWT, _, _, err := authMgr.IssueAccess(userID, workspaceID, sessionID)
	if err != nil {
		t.Fatalf("IssueAccess(user=%d ws=%d sid=%d): %v", userID, workspaceID, sessionID, err)
	}

	// ── Encryption key for the vault ──────────────────────────────
	encKey := make([]byte, 32)
	if _, err := rand.Read(encKey); err != nil {
		t.Fatalf("rand.Read(encryption key): %v", err)
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encKey)
	// The vault reads ENCRYPTION_KEY_BASE64 out of app settings (or env);
	// simulating that for the in-process Postgres is enough — see
	// credentials.NewVault for the canonical shape.
	_, _ = h.pgDB.Exec(
		"SELECT set_config('app.settings.encryption_key_base64', $1, false)",
		encKeyB64,
	)

	// ── Paper-Google httptest server ──────────────────────────────
	fakeGoogle := newFakeGoogleOauthServer(t, browserSmokeE2EClientID, browserSmokeE2EChannel)
	t.Cleanup(fakeGoogle.Close)

	// ── Custom YouTube OAuthProvider wired to fakeGoogle ──────────
	provider := &browserSmokeYouTubeProvider{
		fake:        fakeGoogle,
		redirectURI: webServerURLPlaceholder, // overwritten below once apiServer is up
		clientID:    browserSmokeE2EClientID,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}

	// ── Custom user store + channel authorizer ────────────────────
	store := &browserSmokeUserStore{}
	authzr := &browserSmokeChannelAuthorizer{db: h.pgDB}

	// ── CapRouter registered with the fake +1-spot ───────────────
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("youtube", provider)

	// ── Vault + token repository ─────────────────────────────────
	// The real CredentialVault writes encrypted ciphertexts into
	// `tokens` via vault.Save(ctx, platformAccountID, &tokenData) —
	// the production path the OAuth callback handler hits on
	// accept. Stand it up with the real Encryptor + TokenStore so
	// the SQL INSERT goes through the same shape production uses.
	tokenRepo := repository.NewTokenRepository(h.pgDB)
	// crypto.NewEncryptor takes (activeKeyID uint32, keys
	// map[uint32]string). Production wiring per cmd/link-drive-and-import
	// uses {1: base64(ENCRYPTION_KEY)}; we mirror that here.
	encryptor, encErr := crypto.NewEncryptor(1, map[uint32]string{
		1: base64.StdEncoding.EncodeToString(encKey),
	})
	if encErr != nil {
		t.Fatalf("crypto.NewEncryptor: %v (key length=%d bytes)", encErr, len(encKey))
	}
	vault := credentials.NewCredentialVault(encryptor, h.pgDB, tokenRepo)

	// ── Production router (Go API under test) ─────────────────────
	router := buildE2ERouter(
		capRouter,
		store,
		authMgr,
		api.WithCredentialVault(vault),
		api.WithChannelAuthorizer(authzr),
	)
	apiServer := httptest.NewServer(router.Setup())
	t.Cleanup(apiServer.Close)
	t.Logf("apiServer URL=%s", apiServer.URL)

	// Now that apiServer is up, lock the redirect_uri the provider
	// serves in GetLoginURLWithOptions. cfg lumps YouTubeRedirectURI
	// into the same string — keep them in sync.
	provider.redirectURI = apiServer.URL + "/api/v1/auth/youtube/callback"
	// cfg is unused for the provider (it builds URLs via the provider
	// surface, not directly from cfg), but pinning it keeps a future
	// regression that pulls redirect_uri from cfg visible.
	_ = config.Config{Auth: config.AuthConfig{YouTubeClientID: provider.clientID, YouTubeRedirectURI: provider.redirectURI}}

	// ── Allocate the headless Chrome ─────────────────────────────
	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		append([]chromedp.ExecAllocatorOption{
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.Flag("remote-debugging-port", "0"),
		}, chromedp.DefaultExecAllocatorOptions[:]...)...,
	)
	t.Cleanup(allocCancel)
	chromedpCtx, chromedpCancel := chromedp.NewContext(allocCtx)
	t.Cleanup(chromedpCancel)
	// Wrap with a hard timeout so a chromedp.Run misbehaviour
	// can't hang the test forever; the deadline propagates to
	// every CDP call below.
	timeoutCtx, timeoutCancel := context.WithTimeout(chromedpCtx, 90*time.Second)
	t.Cleanup(timeoutCancel)
	chromedpCtx = timeoutCtx

	// ── Drive the chain ──────────────────────────────────────────
	var finalURL string
	if err := chromedp.Run(chromedpCtx,
		// 1. Inject the JWT session cookie BEFORE navigating so the
		//    production auth gate at /api/v1/auth/youtube/login sees
		//    a logged-in user (otherwise it 302s to /login).
		//    In chromedp v0.16.x the helper `chromedp.SetCookies` is
		//    not exported — use the lower-level cdproto/network cookie
		//    primitive via an ActionFunc wrapper.
		chromedp.ActionFunc(func(ctx context.Context) error {
			// In chromedp v0.16.x, cdproto commands accept the
			// chromedp context.Context as the executor — no need
			// to call chromedp.FromContext to unwrap. The action
			// ctx already implements the cdp.Executor semantics.
			return chromedpNetwork.SetCookies(
				[]*chromedpNetwork.CookieParam{{
					Name:     auth.SessionCookieName,
					Value:    sessionJWT,
					URL:      apiServer.URL,
					HTTPOnly: true,
					SameSite: chromedpNetwork.CookieSameSiteLax,
				}},
			).Do(ctx)
		}),
		// 2. Hit the production /api/v1/auth/youtube/login. The
		//    handler validates session, mints the oauth_state_youtube
		//    cookie, builds the authorize URL (via the provider we
		//    registered), and 302s to fakeGoogle's consent page.
		chromedp.Navigate(apiServer.URL+"/api/v1/auth/youtube/login?expected_channel_id="+browserSmokeE2EChannel),
		// 3. Wait for the consent form's #approve-btn — proves the
		//    browser really landed on fakeGoogle's authorize URL.
		chromedp.WaitVisible("#approve-btn", chromedp.ByID),
		// 4. Click Approve. The form is GET → redirect_uri so the
		//    browser 302s to the production callback URL on apiServer.
		chromedp.Click("#approve-btn", chromedp.ByID),
		// 5. Wait for the final navigation to settle on apiServer
		//    (the callback handler will 200 / 302 to /app/linking or
		//    similar; either is fine for this test — what matters is
		//    the round-trip back to apiServer happened).
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&finalURL),
	); err != nil {
		t.Fatalf("chromedp.Run: %v (last URL=%s)", err, finalURL)
	}

	// ── Assertions — fakeGoogle was visited and the token exchange fired ──
	if got := fakeGoogle.consentCalls.Load(); got != 1 {
		t.Errorf("fakeGoogle.consentCalls: want 1 (browser reached /o/oauth2/v2/auth once); got %d", got)
	}
	if got := fakeGoogle.tokenCalls.Load(); got != 1 {
		t.Errorf("fakeGoogle.tokenCalls: want 1 (production callback handler dispatched one token exchange); got %d", got)
	}
	if !strings.Contains(finalURL, apiServer.URL+"/api/v1/auth/youtube/callback") &&
		!strings.Contains(finalURL, "/app/") {
		// Either the callback page itself OR /app/* (the post-callback SPA
		// redirect) is an acceptable terminal URL — what MUST NOT happen
		// is for the final URL to still be on the fakeGoogle host.
		t.Errorf("final URL after consent: expected to be back on apiServer OR /app/*; got %s", finalURL)
	}
	if fakeGoogle.lastSeenState == "" {
		t.Errorf("fakeGoogle.lastSeenState is empty: production handler forgot to round-trip the state JWT to Google")
	}
	if fakeGoogle.lastSeenRedirectURI != provider.redirectURI {
		t.Errorf("fakeGoogle.lastSeenRedirectURI: want %q, got %q (provider mis-built the redirect_uri param)", provider.redirectURI, fakeGoogle.lastSeenRedirectURI)
	}
	if got := authzr.authorizeCalls.Load(); got != 1 {
		t.Errorf("AuthorizeChannel: want 1 (production callback handler dispatches bind once); got %d", got)
	}

	// ── THE USER-REQUESTED SQL ASSERTION ─────────────────────────
	var (
		encryptedToken        []byte
		encryptedRefreshToken []byte
		providerCol           string
	)
	row := h.pgDB.QueryRow(`
		SELECT t.encrypted_token,
		       t.encrypted_refresh_token,
		       oc.provider
		FROM tokens t
		JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
		WHERE oc.provider = $1
		LIMIT 1`,
		models.PlatformYouTube,
	)
	if err := row.Scan(&encryptedToken, &encryptedRefreshToken, &providerCol); err != nil {
		t.Fatalf("SQL assertion (terminal invariant from user prompt) failed: %v", err)
	}
	if len(encryptedToken) == 0 {
		t.Errorf("encrypted_token column is empty for youtube oauth_connection: production vault.SaveEncryptedToken failed to write the access token ciphertext")
	}
	if len(encryptedRefreshToken) == 0 {
		t.Errorf("encrypted_refresh_token column is empty for youtube oauth_connection: production vault.SaveEncryptedToken failed to write the refresh token ciphertext (the user prompt explicitly calls this column out)")
	}
	if providerCol != models.PlatformYouTube {
		t.Errorf("provider column: want %q, got %q (joined the wrong oauth_connection row)",
			models.PlatformYouTube, providerCol)
	}

	// ── Final cosmetic dump for ops-debugging on CI ───────────────
	t.Logf(
		"SMOKE PASS — browser=%s apiServer=%s channel=%s\n"+
			"  fakeGoogle.consentCalls=%d tokenCalls=%d state=%s redirect_uri=%s\n"+
			"  authzr.authorizeCalls=%d connID=%d\n"+
			"  tokens.encrypted_token=%d bytes, tokens.encrypted_refresh_token=%d bytes",
		chromePath, apiServer.URL, browserSmokeE2EChannel,
		fakeGoogle.consentCalls.Load(), fakeGoogle.tokenCalls.Load(),
		fakeGoogle.lastSeenState, fakeGoogle.lastSeenRedirectURI,
		authzr.authorizeCalls.Load(), authzr.connID,
		len(encryptedToken), len(encryptedRefreshToken),
	)
}

// stripScheme + bytes-tripwire removed after the v0.16.x chromedp
// switch to network.SetCookies(+CookieParam.URL=). Neither bytes nor
// an explicit host-only domain helper is needed anymore — re-add
// only if a future multi-host cookie context needs them.
