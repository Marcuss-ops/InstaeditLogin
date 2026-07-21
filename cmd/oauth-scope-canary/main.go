// Command oauth-scope-canary is the InstaEdit OAuth drift canary.
//
// Two responsibilities:
//
//  1. Live tokeninfo check (when DRIVE_OAUTH_CANARY_TOKEN env var is
//     present). Hits Google's https://oauth2.googleapis.com/v3/tokeninfo
//     with a golden access token and diffs the GRANTED scopes against
//     the canonical list below. Drift in EITHER direction triggers
//     exit code 1. Designed to run weekly on a scheduled GitHub Actions
//     workflow OR on-demand via workflow_dispatch; never on PR lane
//     (where the golden token is unavailable).
//
//  2. Fly secrets coherence (always-on, runs in PR lane too). Reads
//     scripts/required-fly-secrets.txt + scripts/disabled-fly-secrets-prefixes.txt
//     and asserts three invariants:
//     a. The required key set is DISJOINT from the disabled-prefix set.
//     b. Each OAuth provider whose key prefix appears in the required set
//     has a COMPLETE triple {CLIENT_ID, CLIENT_SECRET, REDIRECT_URI}.
//     c. A separate unit test (SecretCoherenceScript level) checks the
//     docs/OAUTH-PRODUCTION.md canonical-scope table.
//
// The hardcoded scope list here is the SINGLE SOURCE OF TRUTH. docs/
// OAUTH-PRODUCTION.md mirrors the list and is locked by
// TestOAuthScopes_DocsMatchCanonical covering each scope literal against
// markdown grep so docs cannot drift from code.
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
	"path/filepath"
	"regexp"
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
	"https://www.googleapis.com/auth/yt-analytics-monetary.readonly",
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

// runResult codes the binary prints + exits with. Bucketed so the
// scheduled GitHub Actions run + the on-PR secrets-coherence run
// each map to a distinct exit code for the operator's triage.
type runResult int

const (
	resultOK runResult = iota
	resultScopeDrift
	resultSecretCoherence
	resultBoth // = resultScopeDrift | resultSecretCoherence
)

// env-required-secrets-path + env-disabled-secrets-path are the two
// override knobs that bypass the repoRoot walk. Production callers
// leave them unset; tests set them to fixture paths so a single
// `run()` call exercises the end-to-end orchestration without
// having to chdir into a leaf tempdir.
const (
	envRequiredSecretsPath = "INSTAEDIT_REQUIRED_SECRETS_PATH"
	envDisabledSecretsPath = "INSTAEDIT_DISABLED_SECRETS_PATH"
	// envTokeninfoURL overrides Google's oauth2.googleapis.com/v3/tokeninfo
	// URL; tests point it at an httptest.Server. Production callers
	// leave it unset; the binary falls back to tokenInfoEndpoint.
	envTokeninfoURL = "OAUTH_TOKENINFO_URL"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		switch {
		case errors.Is(err, errScopeDrift):
			os.Exit(int(resultScopeDrift))
		case errors.Is(err, errSecretCoherence):
			os.Exit(int(resultSecretCoherence))
		case errors.Is(err, errBoth):
			os.Exit(int(resultBoth))
		default:
			fmt.Fprintln(os.Stderr, "oauth-scope-canary: unexpected:", err)
			os.Exit(1)
		}
	}
}

var (
	errScopeDrift      = errors.New("oauth scope drift detected")
	errSecretCoherence = errors.New("fly secrets list coherence failure")
	errBoth            = errors.New("oauth scope drift + secrets coherence failure")
)

// run is the orchestration entry point; returns nil on success or a
// bucketed sentinel error otherwise. Splitting live check + secrets
// check via separate helper funcs keeps the test surface clean: each
// test exercises ONE helper against a known fixture without firing
// up the parent orchestration.
//
// Secrets paths come from EITHER the env-var overrides
// (INSTAEDIT_REQUIRED_SECRETS_PATH / INSTAEDIT_DISABLED_SECRETS_PATH,
// used by tests) OR the production-side
// scripts/{required,disabled}-fly-secrets.txt derived from repoRoot().
// The tokeninfo endpoint likewise: tests set OAUTH_TOKENINFO_URL to
// point at an httptest.Server; production leaves it unset and falls
// back to tokenInfoEndpoint.
func run(logger *slog.Logger) error {
	requiredPath := os.Getenv(envRequiredSecretsPath)
	disabledPath := os.Getenv(envDisabledSecretsPath)
	if requiredPath == "" || disabledPath == "" {
		root, err := repoRoot()
		if err != nil {
			return fmt.Errorf("locate repo root: %w", err)
		}
		if requiredPath == "" {
			requiredPath = filepath.Join(root, "scripts", "required-fly-secrets.txt")
		}
		if disabledPath == "" {
			disabledPath = filepath.Join(root, "scripts", "disabled-fly-secrets-prefixes.txt")
		}
	}

	var failures []error

	if coherenceErrs := verifySecretCoherence(requiredPath, disabledPath); len(coherenceErrs) > 0 {
		for _, e := range coherenceErrs {
			logger.Warn("secrets coherence", "issue", e.Error())
		}
		failures = append(failures, errSecretCoherence)
	} else {
		logger.Info("secrets coherence: OK", "required", requiredPath, "disabled", disabledPath)
	}

	driveToken := os.Getenv("DRIVE_OAUTH_CANARY_TOKEN")
	if driveToken == "" {
		logger.Info("live tokeninfo check: SKIPPED (DRIVE_OAUTH_CANARY_TOKEN env var not set; secrets coherence only)")
	} else {
		endpoint := os.Getenv(envTokeninfoURL)
		if endpoint == "" {
			endpoint = tokenInfoEndpoint
		}
		if driftErr := checkLiveScopeDrift(context.Background(), logger, driveToken, endpoint); driftErr != nil {
			failures = append(failures, errScopeDrift)
		}
	}

	switch len(failures) {
	case 0:
		return nil
	case 1:
		return failures[0]
	case 2:
		return errBoth
	default:
		return errBoth
	}
}

// repoRoot walks upward from the executable's working directory until
// it finds go.mod (the canonical InstaEdit monorepo root). Both
// scripts live one level below; this lets `go run ./cmd/oauth-scope-canary`
// resolve the secrets-path arguments without a flag.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", wd)
		}
		dir = parent
	}
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

// readSecretList parses a Fly secrets-list file (one key per line,
// comments / blanks ignored) into a stable slice. Lines with a `#`
// anywhere are treated as commentary. Both required and disabled
// files share the same line shape; the caller picks the parser
// variant appropriate for the semantic (key set vs prefix set).
func readSecretList(path string) ([]string, error) {
	return readSecretListLineByLine(path)
}

// readSecretListLineByLine handles the production secret-file shape:
// one key per line, `#` comments allowed after optional whitespace,
// blank lines ignored, trailing whitespace trimmed. POSIX env-var
// character class enforced via a regex below.
func readSecretListLineByLine(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, ln := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		// Strip inline comments — the keys never contain `#`.
		if idx := strings.Index(trimmed, "#"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		if trimmed == "" {
			continue
		}
		if !validSecretKey(trimmed) {
			return nil, fmt.Errorf("invalid secret key %q in %s (must match ^[A-Z0-9_]+$)", trimmed, path)
		}
		keys = append(keys, trimmed)
	}
	sort.Strings(keys)
	return keys, nil
}

// validSecretKey matches the Fly secrets POSIX env-var character
// class: uppercase letters, digits, underscore. Mirrors the same
// constraint the .env parser enforces (scripts/_parse_envfile.py).
var secretKeyPattern = regexp.MustCompile(`^[A-Z0-9_]+$`)

func validSecretKey(k string) bool {
	return secretKeyPattern.MatchString(k)
}

// readDisabledPrefixes returns the deny-list prefixes as a sorted
// deduplicated slice. Comments + blanks are stripped by the same
// line-by-line parser as readSecretListLineByLine; the trailing
// `_` underscore on each prefix is what marks them as prefixes
// (a key like STRIPE_WEBHOOK_SECRET matches the STRIPE_ prefix).
func readDisabledPrefixes(path string) ([]string, error) {
	return readSecretListLineByLine(path)
}

// metaCognitoSubPlatforms maps the per-platform REDIRECT_URI prefixes
// that share ONE Meta OAuth client (META_APP_ID + META_APP_SECRET).
// These sub-platforms are NOT required to also expose their own
// CLIENT_ID / CLIENT_SECRET — the parent META_ cognito client covers
// them. Their only required key under each prefix is REDIRECT_URI.
//
// Without this carve-out, every META-cognito re-import would surface
// "incomplete triple" issues that are actually intentional by design.
// Adding a NEW Meta-cognito sub-platform requires updating BOTH this
// map AND scripts/required-fly-secrets.txt: a key like WHATSAPP_
// REDIRECT_URI would silently pass the disjoint check today; the
// metaCognitoSubPlatforms entry is the explicit allow-list the
// operator maintains.
var metaCognitoSubPlatforms = map[string]bool{
	"INSTAGRAM_": true, // sub-platform under META_
	"FACEBOOK_":  true, // sub-platform under META_
	"THREADS_":   true, // sub-platform under META_
}

// metaCognitoSubPlatformKeysPerProvider enumerates what each
// metaCognito sub-platform requires. Sub-platforms only need their
// REDIRECT_URI; the parent META_ provides the auth client.
var metaCognitoSubPlatformKeysPerProvider = map[string][]string{
	"INSTAGRAM_": {"REDIRECT_URI"},
	"FACEBOOK_":  {"REDIRECT_URI"},
	"THREADS_":   {"REDIRECT_URI"},
}

// metaCognitoAppKeys is what the parent META_ provider REQUIRES. It
// uses APP_ID + APP_SECRET semantics (NOT the CLIENT_ID + CLIENT_SECRET
// triple pattern used by YouTube / LinkedIn / TikTok / X). The canary
// treats META_APP_ID as substitutable for CLIENT_ID and META_APP_SECRET
// as substitutable for CLIENT_SECRET; REDIRECT_URI is NOT required on
// the META_ prefix because each sub-platform (INSTAGRAM_, FACEBOOK_,
// THREADS_) carries its own REDIRECT_URI.
var metaCognitoAppKeys = map[string][]string{
	"META_": {"APP_ID", "APP_SECRET"},
}

// verifySecretCoherence runs the three always-on invariants:
//  1. required-keys DISJOINT from disabled-prefix set (no key in
//     required is shadowed by a deny prefix).
//  2. Each OAuth provider prefix that has AT LEAST ONE key in the
//     required set has the expected keys for its auth model. Most
//     providers use the {CLIENT_ID, CLIENT_SECRET, REDIRECT_URI}
//     triple; Meta uses {APP_ID, APP_SECRET} on the parent META_
//     and {REDIRECT_URI} on each sub-platform (INSTAGRAM_, FACEBOOK_,
//     THREADS_ — Meta-cognito pattern).
//  3. (Owner-side warning, not a hard fail) Empty prefix tolerance:
//     a zero-prefix provider in required is unusual and worth noting.
//
// Returns nil on full coherence; non-empty slice on any issue. The
// caller decides whether to escalate each issue as ERROR or WARN;
// for now every issue is treated as ERR (exit 2).
func verifySecretCoherence(requiredPath, disabledPath string) []error {
	required, errReq := readSecretList(requiredPath)
	if errReq != nil {
		return []error{fmt.Errorf("read required list: %w", errReq)}
	}
	disabled, errDis := readDisabledPrefixes(disabledPath)
	if errDis != nil {
		return []error{fmt.Errorf("read disabled list: %w", errDis)}
	}

	var issues []error

	// Invariant 1: disjoint.
	requiredSet := make(map[string]bool, len(required))
	for _, k := range required {
		requiredSet[k] = true
	}
	disabledSet := make(map[string]string, len(disabled))
	for _, p := range disabled {
		disabledSet[p] = p
	}
	for _, k := range required {
		for _, p := range disabled {
			if strings.HasPrefix(k, p) {
				issues = append(issues, fmt.Errorf(
					"required key %q is shadowed by disabled prefix %q (required=%s, disabled=%s)",
					k, p, requiredPath, disabledPath))
			}
		}
	}

	// Invariant 2: complete OAuth keys per provider.
	providerPrefixes := map[string]bool{}
	for _, k := range required {
		prefix := inferProviderPrefix(k)
		if prefix == "" {
			continue
		}
		providerPrefixes[prefix] = true
	}
	for prefix := range providerPrefixes {
		var required []string
		switch {
		case metaCognitoSubPlatforms[prefix]:
			required = metaCognitoSubPlatformKeysPerProvider[prefix]
		case prefix == "META_":
			required = metaCognitoAppKeys[prefix]
		default:
			required = []string{"CLIENT_ID", "CLIENT_SECRET", "REDIRECT_URI"}
		}
		var missing []string
		for _, suffix := range required {
			if !requiredSet[prefix+suffix] {
				missing = append(missing, prefix+suffix)
			}
		}
		if len(missing) > 0 {
			issues = append(issues, fmt.Errorf(
				"provider %q has incomplete OAuth keys in %s: missing %v",
				prefix, requiredPath, missing))
		}
	}

	// Invariant 3 (warning, deliberate not-error): every Meta-cognito
	// sub-platform REQUIRES a matching parent META_ auth client.
	// Today that's META_APP_ID + META_APP_SECRET; future Meta-cognito
	// surfaces (e.g. WHATSAPP_) would also need META_ present. If
	// sub-platforms are listed but META_ is NOT, operator-side the
	// OAuth completed without an auth client backing them — a
	// production-incident shape we surface as a coherence issue.
	metaSubActive := false
	for p := range metaCognitoSubPlatforms {
		if providerPrefixes[p] {
			metaSubActive = true
		}
	}
	if metaSubActive && !providerPrefixes["META_"] {
		issues = append(issues, fmt.Errorf(
			"at least one Meta-cognito sub-platform (INSTAGRAM_, FACEBOOK_, THREADS_) is present in %s but parent META_ auth client (META_APP_ID + META_APP_SECRET) is MISSING — sub-platforms cannot authenticate without the parent",
			requiredPath))
	}

	return issues
}

// inferProviderPrefix extracts the OAuth-provider prefix from a key.
// For "YOUTUBE_CLIENT_ID" → "YOUTUBE_". For "META_APP_SECRET" → "META_".
// Returns "" when the key doesn't fit the OAuth pattern (e.g.
// DATABASE_URL, ADMIN_INVITE_TOKEN) so non-OAuth keys don't trip
// invariant 2.
//
// Heuristic: the prefix is the longest uppercase-leading segment
// ending at the first '_' whose remaining substring starts with one
// of {CLIENT_ID, CLIENT_SECRET, REDIRECT_URI, APP_ID, APP_SECRET}.
// Restricted to avoid false-positives on generic uppercase keys.
func inferProviderPrefix(key string) string {
	for _, suffix := range []string{"CLIENT_ID", "CLIENT_SECRET", "REDIRECT_URI", "APP_ID", "APP_SECRET"} {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		idx := strings.LastIndex(key, "_"+suffix)
		if idx <= 0 {
			return ""
		}
		return key[:idx+1] // include trailing underscore for HasPrefix match
	}
	return ""
}
