package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// ─── Docs-vs-canonical sync ─────────────────────────────────────────────

// TestOAuthScopes_DocsMatchCanonical pins that docs/OAUTH-PRODUCTION.md
// Step 3 actually lists every canonical scope. The test is the
// single source-of-truth lock: edit canonicalScopes, then either
// edit the docs too OR expect this test to fail.
func TestOAuthScopes_DocsMatchCanonical(t *testing.T) {
	root, err := repoRootForTest()
	if err != nil {
		t.Skipf("could not locate repo root (not running inside the InstaeditLogin tree?): %v", err)
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

// TestRun_NoTokenEnv_ExitsCleanly covers the most common CI path:
// DRIVE_OAUTH_CANARY_TOKEN is unset. run() logs SKIPPED and returns
// nil — the secrets-coherence leg has been removed post-cutover, so
// the only orchestration remaining is the live tokeninfo leg.
func TestRun_NoTokenEnv_ExitsCleanly(t *testing.T) {
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "")
	t.Setenv(envTokeninfoURL, "")
	if err := run(newDiscardLogger()); err != nil {
		t.Errorf("run() with no token: want nil (SKIPPED), got %v", err)
	}
}

// TestRun_TokenPlusDriftEnv: DRIVE_OAUTH_CANARY_TOKEN set, OAUTH_TOKENINFO_URL
// points at an httptest.Server returning wrong scope. run() must wrap
// errScopeDrift. The previous secrets-coherence coupling has been removed
// (the Fly env parser and its CI regression job were retired in 2026-08-07).
func TestRun_TokenPlusDriftEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scope":"https://www.googleapis.com/auth/openid"}`)) // scalped
	}))
	defer srv.Close()

	t.Setenv(envTokeninfoURL, srv.URL)
	t.Setenv("DRIVE_OAUTH_CANARY_TOKEN", "fake-token-xyz")

	err := run(newDiscardLogger())
	if err == nil {
		t.Fatal("run() with drift in tokeninfo: want errScopeDrift, got nil")
	}
	if !errors.Is(err, errScopeDrift) {
		t.Errorf("run() must wrap errScopeDrift; got %v", err)
	}
}

// ─── Utilities ──────────────────────────────────────────────────────────

// newDiscardLogger returns a real *slog.Logger whose TextHandler
// writes to io.Discard so WARN/INFO lines from diffScopes / run()
// don't pollute test output.
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// repoRootForTest walks upward from the binary's working directory
// until it finds go.mod (the canonical InstaEdit monorepo root).
// Test-only helper; previously repoRoot() lived in main.go but was
// dropped along with the second responsibility (hosted-platform
// secrets-coherence) that consumed it. Kept
// here so TestOAuthScopes_DocsMatchCanonical can locate the docs.
func repoRootForTest() (string, error) {
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
