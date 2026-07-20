package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ─── Live tokeninfo drift detection ─────────────────────────────────────

// TestDiffScopes_NoDrift pins the canonical happy-path: granted == canonical.
// ANY scenario where this test fails indicates either:
//   - a new scope was added to canonicalScopes and not yet deployed to
//     Google (operator must bump + redeploy + re-consent)
//   - a wrong scope made it into the token (fix the OAuth URL builder)
//   - a forbidden scope was granted (immediate consent-screen hardening)
func TestDiffScopes_NoDrift(t *testing.T) {
	granted := append([]string(nil), canonicalScopes...)
	logger := newDiscardLogger()
	if err := diffScopes(granted, canonicalScopes, forbiddenScopes, logger); err != nil {
		t.Errorf("diffScopes on perfect canonical match: want nil, got %v", err)
	}
}

// TestDiffScopes_MissingDetected pins the missing-scope branch: the
// canonical set holds N scopes, granted holds N-1. The diff must surface
// errScopeDrift and the log lines tag exactly the MISSING scope.
func TestDiffScopes_MissingDetected(t *testing.T) {
	granted := append([]string(nil), canonicalScopes[:len(canonicalScopes)-1]...) // drop last
	logger := newDiscardLogger()
	err := diffScopes(granted, canonicalScopes, forbiddenScopes, logger)
	if err == nil {
		t.Fatal("want errScopeDrift on missing scope, got nil")
	}
	if !strings.Contains(err.Error(), "missing=") {
		t.Errorf("error must include missing= count, got %v", err)
	}
}

// TestDiffScopes_ExtraDetected pins the extra-scope branch: granted
// holds a scope not in canonical (e.g. someone added a new identity
// scope at the OAuth URL builder without updating this manifest).
func TestDiffScopes_ExtraDetected(t *testing.T) {
	granted := append([]string(nil), canonicalScopes...)
	granted = append(granted, "https://www.googleapis.com/auth/cloud-platform")
	logger := newDiscardLogger()
	err := diffScopes(granted, canonicalScopes, forbiddenScopes, logger)
	if err == nil {
		t.Fatal("want errScopeDrift on extra scope, got nil")
	}
	if !strings.Contains(err.Error(), "extra=1") {
		t.Errorf("error must surface extra=1, got %v", err)
	}
}

// TestDiffScopes_ForbiddenDetected pins the strict-equality check on
// the deny list: even ONE forbidden scope surface must trigger drift.
// Uses yt-analytics.readonly as the canonical-because-historical-,
// MUST-NEVER-be-granted scope (per docs/OAUTH-PRODUCTION.md Step 3).
func TestDiffScopes_ForbiddenDetected(t *testing.T) {
	granted := append([]string(nil), canonicalScopes...)
	granted = append(granted, "https://www.googleapis.com/auth/yt-analytics.readonly")
	logger := newDiscardLogger()
	err := diffScopes(granted, canonicalScopes, forbiddenScopes, logger)
	if err == nil {
		t.Fatal("want errScopeDrift on FORBIDDEN scope, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden=1") {
		t.Errorf("error must surface forbidden=1, got %v", err)
	}
}

// TestDiffScopes_BothMissingAndExtra covers a "something changed in
// both directions simultaneously" case — the diff report must surface
// both classes of drift in one pass so the operator can see the full
// picture without rerunning.
func TestDiffScopes_BothMissingAndExtra(t *testing.T) {
	granted := []string{
		"https://www.googleapis.com/auth/youtube.upload",
		"https://www.googleapis.com/auth/youtube.readonly",
		"https://www.googleapis.com/auth/cloud-platform", // extra
		// drive.readonly, userinfo.email, userinfo.profile, openid missing
	}
	logger := newDiscardLogger()
	err := diffScopes(granted, canonicalScopes, forbiddenScopes, logger)
	if err == nil {
		t.Fatal("want errScopeDrift on both, got nil")
	}
}

// ─── Live tokeninfo end-to-end with httptest.Server ─────────────────────

// TestCheckLiveScopeDrift_Ok pins the end-to-end fast path: an
// httptest.Server returns the canonical scope claim; checkLiveScopeDrift
// returns nil. The endpoint arg overrides the production URL so this
// test does NOT depend on oauth2.googleapis.com being reachable.
func TestCheckLiveScopeDrift_Ok(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"aud":"client-id","azp":"client-id","scope":"` +
			strings.Join(canonicalScopes, " ") + `"}`))
	}))
	defer srv.Close()
	logger := newDiscardLogger()
	if err := checkLiveScopeDrift(context.Background(), logger, "fake-token", srv.URL); err != nil {
		t.Errorf("checkLiveScopeDrift on perfect response: want nil, got %v", err)
	}
}

// TestCheckLiveScopeDrift_Drift pins the end-to-end drift path: a
// scalped scope claim (missing drive.readonly) returns errScopeDrift.
func TestCheckLiveScopeDrift_Drift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scope":"https://www.googleapis.com/auth/youtube.upload openid"}`))
	}))
	defer srv.Close()
	logger := newDiscardLogger()
	err := checkLiveScopeDrift(context.Background(), logger, "fake-token", srv.URL)
	if err == nil {
		t.Fatal("want errScopeDrift on missing scopes, got nil")
	}
}

// TestCheckLiveScopeDrift_ForbiddenEndToEnd pins the FORBIDDEN-surface
// end-to-end: yt-analytics.readonly slips into the claim, the canary
// MUST surface it as drift.
func TestCheckLiveScopeDrift_ForbiddenEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scope":"` + strings.Join(canonicalScopes, " ") +
			` https://www.googleapis.com/auth/yt-analytics.readonly"}`))
	}))
	defer srv.Close()
	err := checkLiveScopeDrift(context.Background(), newDiscardLogger(), "fake-token", srv.URL)
	if err == nil {
		t.Fatal("want errScopeDrift on FORBIDDEN scope, got nil")
	}
}

// TestCheckLiveScopeDrift_5xxTranslation: a 502 from tokeninfo
// translates to errScopeDrift so CI exit code is non-zero (fail-loud
// vs. silent 200).
func TestCheckLiveScopeDrift_5xxTranslation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer srv.Close()
	logger := newDiscardLogger()
	if err := checkLiveScopeDrift(context.Background(), logger, "fake-token", srv.URL); err == nil {
		t.Fatal("want errScopeDrift on 5xx upstream, got nil")
	}
}

// ─── Secret list parsing ────────────────────────────────────────────────

func TestReadSecretList_LineByLine(t *testing.T) {
	dir := t.TempDir()
	want := []string{"YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET", "YOUTUBE_REDIRECT_URI"}
	body := "# header comment\n\nYOUTUBE_CLIENT_ID\nYOUTUBE_CLIENT_SECRET  # trailing\nYOUTUBE_REDIRECT_URI\n"
	if err := os.WriteFile(filepath.Join(dir, "secrets.txt"), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := readSecretList(filepath.Join(dir, "secrets.txt"))
	if err != nil {
		t.Fatalf("readSecretList: %v", err)
	}
	sort.Strings(got)
	if !equalSlices(got, want) {
		t.Errorf("keys: want %v, got %v", want, got)
	}
}

func TestReadSecretList_RejectsInvalidKey(t *testing.T) {
	dir := t.TempDir()
	body := "lowercase-is-bad  # invalid POSIX env-var\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.txt"), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := readSecretList(filepath.Join(dir, "bad.txt")); err == nil {
		t.Fatal("want error on lowercase secret key, got nil")
	}
}

// ─── Secrets coherence invariants ───────────────────────────────────────

// TestVerifySecretCoherence_Disjoint pins invariant (1): a required
// key that has any disabled-prefix as a prefix is incoherent.
func TestVerifySecretCoherence_Disjoint(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"STRIPE_WEBHOOK_SECRET", // required + disabled prefix STRIPE_ → incoherent
		"YOUTUBE_CLIENT_ID",
	}, []string{
		"STRIPE_",
	})
	errs := verifySecretCoherence(
		filepath.Join(dir, "required.txt"),
		filepath.Join(dir, "disabled.txt"),
	)
	if len(errs) == 0 {
		t.Fatal("want at least 1 issue (STRIPE_ collision), got 0")
	}
	foundCollider := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "STRIPE_WEBHOOK_SECRET") &&
			strings.Contains(e.Error(), "STRIPE_") {
			foundCollider = true
		}
	}
	if !foundCollider {
		t.Errorf("issue list must include the STRIPE_* collision; got %v", errs)
	}
}

// TestVerifySecretCoherence_CompleteTriple pins invariant (2): the
// YOUTUBE_ provider is in required with its full {CLIENT_ID,
// CLIENT_SECRET, REDIRECT_URI} triple. No issue.
func TestVerifySecretCoherence_CompleteTriple(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID",
		"YOUTUBE_CLIENT_SECRET",
		"YOUTUBE_REDIRECT_URI",
	}, []string{
		"STRIPE_",
	})
	errs := verifySecretCoherence(
		filepath.Join(dir, "required.txt"),
		filepath.Join(dir, "disabled.txt"),
	)
	if len(errs) != 0 {
		t.Errorf("want coherence on complete triple, got %v", errs)
	}
}

// TestVerifySecretCoherence_IncompleteTriple pins invariant (2)'s
// failure half: YOUTUBE_ is in required but only 2/3 keys.
func TestVerifySecretCoherence_IncompleteTriple(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID",
		"YOUTUBE_CLIENT_SECRET",
		// YOUTUBE_REDIRECT_URI missing
	}, []string{})
	errs := verifySecretCoherence(
		filepath.Join(dir, "required.txt"),
		filepath.Join(dir, "disabled.txt"),
	)
	if len(errs) == 0 {
		t.Fatal("want at least 1 issue on incomplete triple, got 0")
	}
	foundMissing := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "YOUTUBE_") &&
			strings.Contains(e.Error(), "YOUTUBE_REDIRECT_URI") {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Errorf("issue list must call out YOUTUBE_REDIRECT_URI missing; got %v", errs)
	}
}

// TestVerifySecretCoherence_RealProjectLists pins that the actual
// project-side scripts/required-fly-secrets.txt +
// scripts/disabled-fly-secrets-prefixes.txt pass coherence today.
// Catches a regression that breaks the production fly secret contract.
func TestVerifySecretCoherence_RealProjectLists(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("could not locate repo root (not running inside the InstaeditLogin tree?): %v", err)
	}
	requiredPath := filepath.Join(root, "scripts", "required-fly-secrets.txt")
	disabledPath := filepath.Join(root, "scripts", "disabled-fly-secrets-prefixes.txt")
	if _, err := os.Stat(requiredPath); err != nil {
		t.Skipf("required-fly-secrets.txt not present at %s (skipping)", requiredPath)
	}
	errs := verifySecretCoherence(requiredPath, disabledPath)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("coherence issue: %v", e)
		}
		t.Errorf("production fly secrets lists MUST be coherent today; got %d issue(s); see git log for canonical OK state", len(errs))
	}
}

// TestVerifySecretCoherence_LinksAndMETA pins that META provider
// (which uses APP_ID + APP_SECRET suffixes — distinct from the
// CLIENT_ID/SECRET pattern) is recognised by inferProviderPrefix.
// A regression that misses META's pattern would let the operator
// ship a half-configured facebook/Instagram config silently.
func TestVerifySecretCoherence_LinksAndMETA(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"META_APP_ID",
		"META_APP_SECRET",
		"LINKEDIN_CLIENT_ID",
		"LINKEDIN_CLIENT_SECRET",
		"LINKEDIN_REDIRECT_URI",
	}, []string{})
	errs := verifySecretCoherence(
		filepath.Join(dir, "required.txt"),
		filepath.Join(dir, "disabled.txt"),
	)
	if len(errs) != 0 {
		t.Errorf("META + LINKEDIN complete triples must be coherent; got %v", errs)
	}
}

// TestVerifySecretCoherence_MetaSubPlatformWithoutParentMeta exercises
// invariant 3: sub-platforms (INSTAGRAM_) present WITHOUT META_APP_ID/SECRET.
// run() must surface this as a coherence issue (sub-platforms cannot
// authenticate without the parent META cognito client).
func TestVerifySecretCoherence_MetaSubPlatformWithoutParentMeta(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		// META_APP_ID/SECRET DELIBERATELY missing
		"INSTAGRAM_REDIRECT_URI", "FACEBOOK_REDIRECT_URI", "THREADS_REDIRECT_URI",
		"YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET", "YOUTUBE_REDIRECT_URI",
	}, []string{})
	errs := verifySecretCoherence(
		filepath.Join(dir, "required.txt"),
		filepath.Join(dir, "disabled.txt"),
	)
	if len(errs) == 0 {
		t.Fatal("want at least 1 issue (META_ parent missing), got 0")
	}
	foundMETA := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "META_") {
			foundMETA = true
		}
	}
	if !foundMETA {
		t.Errorf("issue list must call out META_ parent missing; got %v", errs)
	}
}

// ─── Provider prefix inference ─────────────────────────────────────────

func TestInferProviderPrefix_MatchesOAuthSuffixes(t *testing.T) {
	cases := map[string]string{
		"YOUTUBE_CLIENT_ID":      "YOUTUBE_",
		"YOUTUBE_CLIENT_SECRET":  "YOUTUBE_",
		"YOUTUBE_REDIRECT_URI":   "YOUTUBE_",
		"LINKEDIN_CLIENT_ID":     "LINKEDIN_",
		"TIKTOK_CLIENT_SECRET":   "TIKTOK_",
		"META_APP_ID":            "META_",
		"META_APP_SECRET":        "META_",
		"X_CLIENT_ID":            "X_",
		"INSTAGRAM_REDIRECT_URI": "INSTAGRAM_",
		"FACEBOOK_REDIRECT_URI":  "FACEBOOK_",
		"THREADS_REDIRECT_URI":   "THREADS_",
		"DATABASE_URL":           "",
		"JWT_SECRET":             "",
		"ADMIN_INVITE_TOKEN":     "",
	}
	for k, want := range cases {
		got := inferProviderPrefix(k)
		if got != want {
			t.Errorf("inferProviderPrefix(%q): want %q, got %q", k, want, got)
		}
	}
}

// ─── Docs-vs-canonical sync ─────────────────────────────────────────────

// TestOAuthScopes_DocsMatchCanonical pins that docs/OAUTH-PRODUCTION.md
// Step 3 actually lists every canonical scope.
func TestOAuthScopes_DocsMatchCanonical(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("could not locate repo root: %v", err)
	}
	docPath := filepath.Join(root, "docs", "OAUTH-PRODUCTION.md")
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("docs/OAUTH-PRODUCTION.md not present at %s (skipping): %v", docPath, err)
	}
	md := string(body)
	for _, s := range canonicalScopes {
		if !strings.Contains(md, s) {
			t.Errorf("docs/OAUTH-PRODUCTION.md missing canonical scope literal %q; editor must add it", s)
		}
	}
	if !strings.Contains(md, "yt-analytics.readonly") {
		t.Errorf("docs/OAUTH-PRODUCTION.md must explicitly mention yt-analytics.readonly (per Step 3 anti-scope note); missing")
	}
}

// ─── End-to-end orchestration ───────────────────────────────────────────

// TestRun_SecretsOnly_NoTokenEnv covers the most common CI path:
// DRIVE_OAUTH_CANARY_TOKEN is unset. run() executes the secrets
// coherence check via env-var-overridden fixture paths and exits nil
// if both fixture files are coherent. The live tokeninfo leg is
// skipped.
func TestRun_SecretsOnly_NoTokenEnv(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET", "YOUTUBE_REDIRECT_URI",
		"DATABASE_URL",
	}, []string{
		"STRIPE_",
	})
	requiredPath, disabledPath := fixturePaths(t, dir)
	t.Setenv(envRequiredSecretsPath, requiredPath)
	t.Setenv(envDisabledSecretsPath, disabledPath)
	t.Setenv(envTokeninfoURL, "")
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "")

	err := run(newDiscardLogger())
	if err != nil {
		t.Errorf("run() with no token + coherent fixtures: want nil, got %v", err)
	}
}

// TestRun_TokenPlusDriftEnv: DRIVE_OAUTH_CANARY_TOKEN set, OAUTH_TOKENINFO_URL
// points at an httptest.Server returning wrong scope. run() must return
// errScopeDrift (or errBoth if secrets also incoherent; here secrets
// are coherent so we expect errScopeDrift alone).
func TestRun_TokenPlusDriftEnv(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET", "YOUTUBE_REDIRECT_URI",
	}, []string{
		"STRIPE_",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scope":"https://www.googleapis.com/auth/openid"}`)) // scalped
	}))
	defer srv.Close()

	requiredPath, disabledPath := fixturePaths(t, dir)
	t.Setenv(envRequiredSecretsPath, requiredPath)
	t.Setenv(envDisabledSecretsPath, disabledPath)
	t.Setenv(envTokeninfoURL, srv.URL)
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "fake-token-xyz")

	err := run(newDiscardLogger())
	if err == nil {
		t.Fatal("run() with drift in tokeninfo: want errScopeDrift, got nil")
	}
	if !errors.Is(err, errScopeDrift) && !errors.Is(err, errBoth) {
		t.Errorf("run() must wrap errScopeDrift or errBoth; got %v", err)
	}
}

// TestRun_SecretsIncoherentEvenWithoutToken: secrets list has an
// incomplete triple (YOUTUBE_REDIRECT_URI missing). No token set.
// run() must errSecretCoherence regardless of the live-token leg.
func TestRun_SecretsIncoherentEvenWithoutToken(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID",
		"YOUTUBE_CLIENT_SECRET",
		// YOUTUBE_REDIRECT_URI missing
	}, []string{})

	requiredPath, disabledPath := fixturePaths(t, dir)
	t.Setenv(envRequiredSecretsPath, requiredPath)
	t.Setenv(envDisabledSecretsPath, disabledPath)
	t.Setenv(envTokeninfoURL, "")
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "")

	err := run(newDiscardLogger())
	if err == nil {
		t.Fatal("run() with incomplete triple + no token: want errSecretCoherence, got nil")
	}
	if !errors.Is(err, errSecretCoherence) {
		t.Errorf("run() must wrap errSecretCoherence; got %v", err)
	}
}

// TestRun_MetaCognitoOk covers the full Meta-cognito cohesion path:
// META_APP_ID + META_APP_SECRET (the parent auth client) + each
// sub-platform REDIRECT_URI (the per-platform redirect). run() must
// return nil even though the sub-platforms lack CLIENT_ID/SECRET.
func TestRun_MetaCognitoOk(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"META_APP_ID", "META_APP_SECRET",
		"INSTAGRAM_REDIRECT_URI", "FACEBOOK_REDIRECT_URI", "THREADS_REDIRECT_URI",
		"YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET", "YOUTUBE_REDIRECT_URI",
	}, []string{})

	requiredPath, disabledPath := fixturePaths(t, dir)
	t.Setenv(envRequiredSecretsPath, requiredPath)
	t.Setenv(envDisabledSecretsPath, disabledPath)
	t.Setenv(envTokeninfoURL, "")
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "")

	err := run(newDiscardLogger())
	if err != nil {
		t.Errorf("run() with Meta-cognito coherent fixtures: want nil, got %v", err)
	}
}

// TestRun_BothCoherenceAndDrift: secrets missing YOUTUBE_REDIRECT_URI
// (incoherent) AND a scalped tokeninfo response. run() returns errBoth
// (coherence=2, drift=1, bitwise-OR'd = 3) so the operator sees both
// classes of failure in a single exit-code bucket.
func TestRun_BothCoherenceAndDrift(t *testing.T) {
	dir := writeCoherenceFixtures(t, []string{
		"YOUTUBE_CLIENT_ID",
		"YOUTUBE_CLIENT_SECRET",
		// YOUTUBE_REDIRECT_URI missing
	}, []string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scope":"openid"}`))
	}))
	defer srv.Close()

	requiredPath, disabledPath := fixturePaths(t, dir)
	t.Setenv(envRequiredSecretsPath, requiredPath)
	t.Setenv(envDisabledSecretsPath, disabledPath)
	t.Setenv(envTokeninfoURL, srv.URL)
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "fake-token-xyz")

	err := run(newDiscardLogger())
	if err == nil {
		t.Fatal("run() with both coherence+drift: want errBoth, got nil")
	}
	if !errors.Is(err, errBoth) {
		t.Errorf("run() must wrap errBoth (coherence=2 | drift=1); got %v", err)
	}
}

// ─── Utilities ──────────────────────────────────────────────────────────

// newDiscardLogger returns a real *slog.Logger whose TextHandler
// writes to io.Discard so WARN/INFO lines from diffScopes / run()
// don't pollute test output.
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeCoherenceFixtures writes the two secrets-fixture files into a
// fresh tempdir and returns the dir path. Tests pair this with
// fixturePaths(t, dir) to discover the (required, disabled) paths
// and pass them into run() via t.Setenv on the
// INSTAEDIT_REQUIRED_SECRETS_PATH / INSTAEDIT_DISABLED_SECRETS_PATH
// env vars — the production-side paths are bypassed entirely, no
// os.Chdir global-state trick required.
func writeCoherenceFixtures(t *testing.T, required, disabled []string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "required.txt"),
		[]byte(strings.Join(required, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write required: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disabled.txt"),
		[]byte(strings.Join(disabled, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write disabled: %v", err)
	}
	return dir
}

// fixturePaths returns the canonical two-path signatures that the
// tests pass to t.Setenv. Centralised so adding more path-style env
// vars is a one-line scrub.
func fixturePaths(t *testing.T, dir string) (required, disabled string) {
	t.Helper()
	return filepath.Join(dir, "required.txt"), filepath.Join(dir, "disabled.txt")
}
