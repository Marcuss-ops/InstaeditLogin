// Command oauth-scope-canary is the InstaEdit OAuth scope-drift detector.
//
// Single responsibility (post-cutover): probe Google OAuth's v3 tokeninfo
// endpoint and diff the granted scopes against this binary's canonical
// manifest. Drift in EITHER direction (missing canonical / extra granted /
// forbidden scope present) trips a non-zero exit code.
//
// The previous second responsibility (a hosted-platform secrets-coherence
// leg) has been removed from this binary; the underlying two secrets-
// fixture files were dropped in earlier cutover commits and the matching
// env-var overrides no longer exist. Any future cohesion validation
// lives entirely in scripts/_parse_envfile.py + scripts/test_parse_envfile.py
// (a standalone Python CI job), as documented in
// .github/workflows/integration.yml.
//
// Trigger flow:
//   - weekly scheduled run + on-demand workflow_dispatch (see
//     .github/workflows/oauth-canary.yml). Never runs on PR lane.
//   - DRIVE_OAUTH_CANARY_TOKEN must be a git repo secret; if absent the
//     binary logs SKIPPED and exits 0 (missing token is not a failure).
//
// The hardcoded scope list below is the SINGLE SOURCE OF TRUTH. docs/
// OAUTH-PRODUCTION.md mirrors it; TestOAuthScopes_DocsMatchCanonical lints
// the markdown against this list at unit-test time so a docs-edit that drops
// one of these scopes fails PR.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// canonicalScopes is the InstaEdit OAuth manifest — every scope the
// production OAuth client + consent screen MUST request together.
// Order is stable for the unit test diff output.
//
// Source of truth = this constant. docs/OAUTH-PRODUCTION.md Step 3
// table mirrors it; TestOAuthScopes_DocsMatchCanonical lints the
// markdown against this list at test-time so a docs-edit that drops
// one of these scopes fails PR.
var canonicalScopes = []string{
	"https://www.googleapis.com/auth/youtube.upload",
	"https://www.googleapis.com/auth/youtube.readonly",
	"https://www.googleapis.com/auth/youtube.force-ssl",
	"https://www.googleapis.com/auth/drive.readonly",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"openid",
}

// forbiddenScopes is the InstaEdit OAuth DENY list. ANY tokeninfo
// response that includes one of these scopes is treated as drift;
// the publish pipeline must NEVER have them granted (e.g. the
// full-scope `drive` write scope, reserved for the never-shipped
// exporter; yt-analytics.readonly which historically confused
// downstream YouTube dashboards).
var forbiddenScopes = []string{
	"https://www.googleapis.com/auth/drive",
	"https://www.googleapis.com/auth/drive.file",
	"https://www.googleapis.com/auth/youtube",
	"https://www.googleapis.com/auth/yt-analytics.readonly",
}

// tokenInfoEndpoint is the canonical Google tokeninfo endpoint.
// Google's v1 alias (oauth2.googleapis.com/v1/tokeninfo) is deprecated;
// v3 is the production-supported introspection endpoint as of 2026.
const tokenInfoEndpoint = "https://oauth2.googleapis.com/v3/tokeninfo"

// tokenInfoResponse mirrors the subset of Google's tokeninfo JSON
// shape this canary needs. Other fields (email, expires_in, ...) are
// ignored; the canary scope is *scope drift* only.
type tokenInfoResponse struct {
	Aud   string `json:"aud"`
	Azp   string `json:"azp"`
	Scope string `json:"scope"`
}

// runResult codes the binary prints + exits with. Post-cutover the
// only meaningful codes are resultOK (drift free) and resultScopeDrift
// (live tokeninfo disagreed with the canonical set).
type runResult int

const (
	resultOK runResult = iota
	resultScopeDrift
)

// envTokeninfoURL overrides Google's oauth2.googleapis.com/v3/tokeninfo
// URL; tests point it at an httptest.Server. Production callers
// leave it unset; the binary falls back to tokenInfoEndpoint.
const envTokeninfoURL = "OAUTH_TOKENINFO_URL"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		if errors.Is(err, errScopeDrift) {
			os.Exit(int(resultScopeDrift))
		}
		fmt.Fprintln(os.Stderr, "oauth-scope-canary: unexpected:", err)
		os.Exit(1)
	}
}

var errScopeDrift = errors.New("oauth scope drift detected")

// run is the orchestration entry point; returns nil on success or
// errScopeDrift (or a wrapped instance) on drift.
//
// The previous second responsibility (hosted-platform secrets-coherence)
// has been removed; run() now ONLY probes the live tokeninfo endpoint
// when DRIVE_OAUTH_CANARY_TOKEN is set. Missing token is a logged
// SKIP, not a failure.
func run(logger *slog.Logger) error {
	driveToken := os.Getenv("DRIVE_OAUTH_CANARY_TOKEN")
	if driveToken == "" {
		logger.Info("live tokeninfo check: SKIPPED (DRIVE_OAUTH_CANARY_TOKEN env var not set)")
		return nil
	}

	endpoint := os.Getenv(envTokeninfoURL)
	if endpoint == "" {
		endpoint = tokenInfoEndpoint
	}
	return checkLiveScopeDrift(context.Background(), logger, driveToken, endpoint)
}

// checkLiveScopeDrift hits tokeninfo, parses scope, diffs against
// canonicalScopes + forbiddenScopes. Returns nil on match, errScopeDrift
// (wrapped with details) on drift. The http client has a 10s ceiling
// so a flaky Google doesn't hang the scheduled run.
func checkLiveScopeDrift(ctx context.Context, logger *slog.Logger, token, endpoint string) error {
	if token == "" {
		// Caller-guarded but defence-in-depth so a slipped empty token
		// doesn't surface as a confusing 401.
		return fmt.Errorf("%w: empty DRIVE_OAUTH_CANARY_TOKEN", errScopeDrift)
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, endpoint+"?access_token="+token, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", errScopeDrift, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: tokeninfo request: %v", errScopeDrift, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: tokeninfo status %d: %s",
			errScopeDrift, resp.StatusCode, string(body))
	}
	var parsed tokenInfoResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return fmt.Errorf("%w: parse tokeninfo response: %v", errScopeDrift, jsonErr)
	}
	granted := strings.Fields(parsed.Scope)
	logger.Info("live tokeninfo received",
		"aud", parsed.Aud,
		"azp", parsed.Azp,
		"granted_count", len(granted))

	return diffScopes(granted, canonicalScopes, forbiddenScopes, logger)
}

// diffScopes is the testable core: pure-function scope drift detector.
// Returns nil if granted == canonicalScopes element-set AND no
// forbidden scope is present. Returns errScopeDrift (wrapped with a
// detailed report) otherwise. Logs each missing / extra / forbidden
// scope so the operator sees the exact drift without re-reading the
// canary source.
//
// Set semantics: missing scope ∈ canonicalScopes but ∉ granted;
// extra scope ∈ granted but ∉ canonicalScopes; forbidden scope ∈
// forbiddenScopes ∩ granted. The intersection check on forbidden is
// strict-equality (no scope-allow listing) — adding a single forbidden
// literal MUST surface immediately.
func diffScopes(granted, canonical, forbidden []string, logger *slog.Logger) error {
	grantedSet := make(map[string]bool, len(granted))
	for _, s := range granted {
		grantedSet[s] = true
	}
	canonicalSet := make(map[string]bool, len(canonical))
	for _, s := range canonical {
		canonicalSet[s] = true
	}
	forbiddenSet := make(map[string]bool, len(forbidden))
	for _, s := range forbidden {
		forbiddenSet[s] = true
	}

	var missing, extra, forb []string
	for _, s := range canonical {
		if !grantedSet[s] {
			missing = append(missing, s)
		}
	}
	for _, s := range granted {
		if !canonicalSet[s] {
			extra = append(extra, s)
		}
		if forbiddenSet[s] {
			forb = append(forb, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(forb)

	if len(missing) == 0 && len(extra) == 0 && len(forb) == 0 {
		logger.Info("scope drift: NONE", "granted_count", len(granted))
		return nil
	}
	for _, s := range missing {
		logger.Warn("scope drift: MISSING", "scope", s)
	}
	for _, s := range extra {
		logger.Warn("scope drift: EXTRA", "scope", s)
	}
	for _, s := range forb {
		logger.Warn("scope drift: FORBIDDEN", "scope", s)
	}
	return fmt.Errorf("%w: missing=%d extra=%d forbidden=%d",
		errScopeDrift, len(missing), len(extra), len(forb))
}
