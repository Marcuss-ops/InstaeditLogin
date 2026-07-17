package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCookieFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return p
}

func TestParseCookieFile_Basic(t *testing.T) {
	p := writeCookieFile(t, strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"# This is a generated file! Do not edit.",
		"example.com\tFALSE\t/\tFALSE\t9999999999\tsession\tabc123",
		"example.com\tFALSE\t/\tFALSE\t9999999999\tcsrf_token\tdef456",
		"",
	}, "\n"))
	c, err := parseCookieFile(p)
	if err != nil {
		t.Fatalf("parseCookieFile: %v", err)
	}
	if c.session != "abc123" {
		t.Errorf("session: want abc123, got %q", c.session)
	}
	if c.csrf != "def456" {
		t.Errorf("csrf: want def456, got %q", c.csrf)
	}
}

func TestParseCookieFile_HttpOnly(t *testing.T) {
	p := writeCookieFile(t, strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"#HttpOnly_.example.com\tTRUE\t/\tFALSE\t9999999999\tsession\tabc-httponly",
		"#HttpOnly_.example.com\tTRUE\t/\tFALSE\t9999999999\tcsrf_token\tdef-httponly",
		"",
	}, "\n"))
	c, err := parseCookieFile(p)
	if err != nil {
		t.Fatalf("parseCookieFile: %v", err)
	}
	if c.session != "abc-httponly" || c.csrf != "def-httponly" {
		t.Errorf("HttpOnly: want abc-httponly/def-httponly, got %q/%q", c.session, c.csrf)
	}
}

func TestParseCookieFile_ShortLinesSkipped(t *testing.T) {
	p := writeCookieFile(t, strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"example.com\tFALSE\t/\tFALSE\t9999999999\tsession\tonly-session",
		"# a comment line",
		"this line has no tabs and is ignored",
		"example.com\tFALSE\t/\tFALSE\t9999999999\tcsrf_token\tdef-shorts",
		"",
	}, "\n"))
	c, err := parseCookieFile(p)
	if err != nil {
		t.Fatalf("parseCookieFile: %v", err)
	}
	if c.session != "only-session" || c.csrf != "def-shorts" {
		t.Errorf("short lines: want only-session/def-shorts, got %q/%q", c.session, c.csrf)
	}
}

func TestParseCookieFile_MissingFileReturnsError(t *testing.T) {
	_, err := parseCookieFile(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err == nil {
		t.Errorf("expected error when file does not exist")
	}
}

func TestParseCookieFile_EmptyFileReturnsZeroValues(t *testing.T) {
	p := writeCookieFile(t, "# Netscape HTTP Cookie File\n")
	c, err := parseCookieFile(p)
	if err != nil {
		t.Fatalf("parseCookieFile: %v", err)
	}
	if c.session != "" || c.csrf != "" {
		t.Errorf("empty file should yield empty session+csrf, got %q/%q", c.session, c.csrf)
	}
}

func TestLoadConfig_ReportsAllMissingEnvsvarsOnOnePass(t *testing.T) {
	// Critical: operator sets the missing ones in one editor pass
	// instead of fix-rerun-fix-rerun.
	for _, e := range requiredEnvs {
		t.Setenv(e, "")
	}
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("expected error when all required envs unset")
	}
	for _, e := range requiredEnvs {
		if !strings.Contains(err.Error(), e) {
			t.Errorf("error should mention %s; got: %v", e, err)
		}
	}
}

func TestLoadConfig_AllEnvvarSet_NoError(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.FolderID != "fid" || cfg.WorkspaceID != 1 || cfg.FacebookAccountID != 50 {
		t.Errorf("loaded config wrong: %+v", cfg)
	}
}

func TestLoadConfig_JitterBothUnset_DefaultsToZero(t *testing.T) {
	// Required envs set so the happy path wins.
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "")
	t.Setenv(EnvMaxJitterSeconds, "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with jitter unset: %v", err)
	}
	if cfg.MinJitterSeconds != 0 || cfg.MaxJitterSeconds != 0 {
		t.Errorf("unset jitter envs should parse to 0, got min=%d max=%d",
			cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
	}
}

func TestLoadConfig_JitterBothSet_ParsedCorrectly(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	// 4h 30 min window centred on 4h (matches the user's "every 4h ±30min" cadence).
	t.Setenv(EnvMinJitterSeconds, "12600")
	t.Setenv(EnvMaxJitterSeconds, "16200")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with jitter set: %v", err)
	}
	if cfg.MinJitterSeconds != 12600 || cfg.MaxJitterSeconds != 16200 {
		t.Errorf("jitter parse wrong: want min=12600 max=16200, got min=%d max=%d",
			cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
	}
}

func TestLoadConfig_JitterBothZero_AcceptsServerDefaultSentinel(t *testing.T) {
	// Both envs explicitly set to "0" IS the same as both unset:
	// omitempty drops the body fields, server applies 60-3600 s default.
	// This pins the "explicit-zero" form of the sentinel so a future
	// refactor that collapses 0+0 into "set" doesn't accidentally
	// start sending `{"min_jitter_seconds":0,"max_jitter_seconds":0}`
	// (which would override the server default with bogus 0-0 jitter).
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "0")
	t.Setenv(EnvMaxJitterSeconds, "0")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("both-zero jitter should be accepted (server-default sentinel): %v", err)
	}
	if cfg.MinJitterSeconds != 0 || cfg.MaxJitterSeconds != 0 {
		t.Errorf("both-zero not preserved: %d/%d", cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
	}
}

// Mixed states (only MIN set OR only MAX set) are REJECTED because
// Go's omitempty on int64 drops a 0 field, leaving the server with
// an ambiguous single number in the body. Catch these early at the
// CLI boundary so the operator learns the rule at startup.
func TestLoadConfig_JitterOnlyMinSet_Rejects(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "60")
	t.Setenv(EnvMaxJitterSeconds, "0")
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("only-MIN-set should be rejected (would emit single max=0 in body)")
	}
	if !strings.Contains(err.Error(), "BOTH must be set") {
		t.Errorf("error should explain the both-or-neither rule; got: %v", err)
	}
}

func TestLoadConfig_JitterOnlyMaxSet_Rejects(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "0")
	t.Setenv(EnvMaxJitterSeconds, "60")
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("only-MAX-set should be rejected (omitempty drops min=0, server sees ambiguous single number)")
	}
	if !strings.Contains(err.Error(), "BOTH must be set") {
		t.Errorf("error should explain the both-or-neither rule; got: %v", err)
	}
}

func TestLoadConfig_JitterMinGreaterThanMax_Rejects(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "999")
	t.Setenv(EnvMaxJitterSeconds, "100") // min > max
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("min > max should be rejected")
	}
	if !strings.Contains(err.Error(), EnvMinJitterSeconds) || !strings.Contains(err.Error(), EnvMaxJitterSeconds) {
		t.Errorf("error should mention both env names so operator knows where to fix; got: %v", err)
	}
}

func TestLoadConfig_JitterNegative_Rejects(t *testing.T) {
	t.Setenv(EnvCookieFile, "/tmp/cookies.txt")
	t.Setenv(EnvFolderID, "fid")
	t.Setenv(EnvWorkspaceID, "1")
	t.Setenv(EnvFacebookAccountID, "50")
	t.Setenv(EnvMinJitterSeconds, "-1")
	t.Setenv(EnvMaxJitterSeconds, "60")
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("negative jitter should be rejected")
	}
	if !strings.Contains(err.Error(), ">=") {
		t.Errorf("error should mention the >= constraint; got: %v", err)
	}
}

func TestBuildPageBody_IncludesJitterWhenSet(t *testing.T) {
	cfg := Config{
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
		MinJitterSeconds:  12600,
		MaxJitterSeconds:  16200,
	}
	body := buildPageBody(cfg, "", nil)
	var parsed pageBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.MinJitterSeconds != 12600 || parsed.MaxJitterSeconds != 16200 {
		t.Errorf("jitter should be in body; got min=%d max=%d",
			parsed.MinJitterSeconds, parsed.MaxJitterSeconds)
	}
}

func TestBuildPageBody_OmitsJitterWhenZero(t *testing.T) {
	// Critical: when MIN_JITTER_SECONDS / MAX_JITTER_SECONDS are unset
	// (parsed to 0), the JSON body MUST omit them so the server applies
	// its 60-3600 s default. Otherwise operators without jitter envs
	// would silently get 0-0 jitter (every video published the same
	// instant + anti-pattern-detection triggers).
	cfg := Config{
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	body := buildPageBody(cfg, "", nil)
	raw := string(body)
	if strings.Contains(raw, "min_jitter_seconds") || strings.Contains(raw, "max_jitter_seconds") {
		t.Errorf("zero jitter must be omitted from JSON (causes 0-0 server-side); body: %s", raw)
	}
}

// fakePageResponse builds a JSON-encoded response for the mock server.
func writeJSONResp(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func TestRunChain_HappyPath_TwoPagesThenDone(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Assert cookie + CSRF + body shape on all calls.
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=S") {
			t.Errorf("call %d: missing session cookie, got %q", calls, got)
		}
		if got := r.Header.Get("X-CSRF-Token"); got != "C" {
			t.Errorf("call %d: X-CSRF-Token want C, got %q", calls, got)
		}
		switch calls {
		case 1:
			var got pageBody
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode body 1: %v", err)
			}
			if got.PageToken != "" {
				t.Errorf("first call PageToken must be empty (from PAGE_TOKEN env), got %q", got.PageToken)
			}
			if got.CursorScheduledAt != nil {
				t.Errorf("first call CursorScheduledAt must be nil (no CURSOR env), got %v", got.CursorScheduledAt)
			}
			writeJSONResp(t, w, 202, pageResponse{
				FolderID:        "fid",
				ScheduledCount:  3,
				FirstPublishAt:  time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC),
				LastScheduledAt: time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC),
				NextPageToken:   "tok-2",
				Entries: []entry{
					{Index: 0, JobID: 101},
					{Index: 1, JobID: 102},
					{Index: 2, JobID: 103},
				},
			})
		case 2:
			var got pageBody
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode body 2: %v", err)
			}
			if got.PageToken != "tok-2" {
				t.Errorf("second call PageToken must equal previous next_page_token, got %q", got.PageToken)
			}
			expectedCursor, _ := time.Parse(time.RFC3339, "2026-07-17T22:00:00Z")
			if got.CursorScheduledAt == nil || !got.CursorScheduledAt.Equal(expectedCursor) {
				t.Errorf("second call CursorScheduledAt must equal previous last_scheduled_at, got %v", got.CursorScheduledAt)
			}
			writeJSONResp(t, w, 202, pageResponse{
				FolderID:        "fid",
				ScheduledCount:  2,
				FirstPublishAt:  time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC),
				LastScheduledAt: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC),
				NextPageToken:   "", // DONE
				Entries: []entry{
					{Index: 0, JobID: 201},
					{Index: 1, JobID: 202},
				},
			})
		}
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	out := &strings.Builder{}
	exit := runChain(context.Background(), cfg, "S", "C", do, out)
	if exit != 0 {
		t.Errorf("exit: want 0, got %d (log: %s)", exit, out.String())
	}
	if calls != 2 {
		t.Errorf("server should serve 2 pages, got %d", calls)
	}
	log := out.String()
	if !strings.Contains(log, "[page=1 ") || !strings.Contains(log, "[page=2 ") {
		t.Errorf("log should contain page=1 and page=2 markers: %s", log)
	}
	if !strings.Contains(log, "[done] total 5 jobs across 2 pages") {
		t.Errorf("log should contain final tally: %s", log)
	}
	if !strings.Contains(log, "[101,102,103]") {
		t.Errorf("log should preview first page job_ids: %s", log)
	}
	// Per-page log MUST echo the active jitter so a multi-page run
	// that fails mid-pagination shows the cadence without scrolling
	// back to bootLog.
	if !strings.Contains(log, "jitter=server-default (60-3600s)") {
		t.Errorf("per-page log must echo server-default jitter when unset; got: %s", log)
	}
}

func TestRunChain_HappyPath_ForwardsJitterOnEveryPage(t *testing.T) {
	// Same shape as TestRunChain_HappyPath_TwoPagesThenDone but the
	// cfg has jitter set — the body MUST carry min_jitter_seconds +
	// max_jitter_seconds on every page call (multi-page exhaustiveness
	// check: a future refactor that resets jitter mid-loop would
	// surface as "second call's body missing the fields" here).
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var got pageBody
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body call %d: %v", calls, err)
		}
		if got.MinJitterSeconds != 12600 || got.MaxJitterSeconds != 16200 {
			t.Errorf("call %d: jitter must be forwarded; got min=%d max=%d",
				calls, got.MinJitterSeconds, got.MaxJitterSeconds)
		}
		switch calls {
		case 1:
			writeJSONResp(t, w, 202, pageResponse{
				FolderID:       "fid",
				ScheduledCount: 1,
				NextPageToken:  "tok-2",
			})
		case 2:
			writeJSONResp(t, w, 202, pageResponse{
				FolderID:       "fid",
				ScheduledCount: 1,
				NextPageToken:  "",
			})
		}
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
		MinJitterSeconds:  12600,
		MaxJitterSeconds:  16200,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	out := &strings.Builder{}
	if exit := runChain(context.Background(), cfg, "S", "C", do, out); exit != 0 {
		t.Fatalf("exit: want 0, got %d (log: %s)", exit, out.String())
	}
	if calls != 2 {
		t.Errorf("server should serve 2 pages, got %d", calls)
	}
	// Per-page log must echo the active jitter so a long-running
	// pagination that fails on page N shows the cadence inline.
	if !strings.Contains(out.String(), "jitter=min=12600s/max=16200s") {
		t.Errorf("per-page log must echo active jitter; got: %s", out.String())
	}
}

func TestRunChain_RetryOn5xxThenSucceed(t *testing.T) {
	// First call returns 502; second returns 202 OK with next_page empty.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "upstream blip", http.StatusBadGateway)
			return
		}
		writeJSONResp(t, w, 202, pageResponse{
			FolderID:       "fid",
			ScheduledCount: 1,
			Entries:        []entry{{Index: 0, JobID: 99}},
			NextPageToken:  "",
		})
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	// Shorten the retry backoff so the test runs fast.
	originalBackoffs := retryBackoffs
	retryBackoffs = []time.Duration{0, 5 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = originalBackoffs })

	out := &strings.Builder{}
	exit := runChain(context.Background(), cfg, "S", "C", do, out)
	if exit != 0 {
		t.Errorf("exit: want 0, got %d (log: %s)", exit, out.String())
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (one 502 + one 202), got %d", calls)
	}
}

func TestRunChain_RetryOn429ThenSucceed(t *testing.T) {
	// 429 (Too Many Requests / rate limited) should be retried with
	// the same exponential backoff as 5xx. Retry-After header is
	// intentionally not honoured — exponential backoff is simpler and
	// avoids a long server-suggested wait.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		writeJSONResp(t, w, 202, pageResponse{
			FolderID:       "fid",
			ScheduledCount: 1,
			Entries:        []entry{{Index: 0, JobID: 99}},
			NextPageToken:  "",
		})
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	originalBackoffs := retryBackoffs
	retryBackoffs = []time.Duration{0, 5 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = originalBackoffs })

	out := &strings.Builder{}
	exit := runChain(context.Background(), cfg, "S", "C", do, out)
	if exit != 0 {
		t.Errorf("exit: want 0, got %d (log: %s)", exit, out.String())
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (one 429 + one 202), got %d", calls)
	}
}

func TestRunChain_ConfigGap_ReturnsExitCode2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResp(t, w, 200, pageResponse{
			NeedsGoogleDriveAPIKey: true,
			NeedsDriveAccount:      true,
			Note:                   "Set GOOGLE_DRIVE_API_KEY or pass drive_account_id.",
		})
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	out := &strings.Builder{}
	exit := runChain(context.Background(), cfg, "S", "C", do, out)
	if exit != 2 {
		t.Errorf("exit: want 2 for config gap, got %d (log: %s)", exit, out.String())
	}
	if !strings.Contains(out.String(), "needs_google_drive_api_key=true") {
		t.Errorf("log should call out the missing API key: %s", out.String())
	}
}

func TestRunChain_AbortMidChain_ReturnsExitCode130(t *testing.T) {
	// Server returns next_page non-empty; we cancel mid-flight before
	// the second call to confirm the abort path returns 130.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSONResp(t, w, 202, pageResponse{
			FolderID:       "fid",
			ScheduledCount: 1,
			Entries:        []entry{{Index: 0, JobID: 1}},
			NextPageToken:  fmt.Sprintf("tok-%d", calls),
		})
	}))
	defer srv.Close()

	cfg := Config{
		APIBase:           srv.URL,
		FolderID:          "fid",
		WorkspaceID:       1,
		FacebookAccountID: 50,
	}
	do := func(req *http.Request) (*http.Response, error) { return srv.Client().Do(req) }

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the very first call's HTTP ctx propagates cancel

	out := &strings.Builder{}
	exit := runChain(ctx, cfg, "S", "C", do, out)
	if exit != 130 {
		t.Errorf("exit: want 130 for cancel-before-first-page, got %d (log: %s)", exit, out.String())
	}
}

func TestFormatJobIDs_ShortList(t *testing.T) {
	got := formatJobIDs([]int64{101, 102, 103})
	want := "[101,102,103]"
	if got != want {
		t.Errorf("short list: want %q, got %q", want, got)
	}
}

func TestFormatJobIDs_LongList_RangeNotation(t *testing.T) {
	ids := make([]int64, 200)
	for i := range ids {
		ids[i] = int64(1000 + i)
	}
	got := formatJobIDs(ids)
	// First 5 + omitted count (200 - 2*5 = 190 middle IDs) + last 5.
	want := "[1000,1001,1002,1003,1004,\u2026190\u2026,1195,1196,1197,1198,1199]"
	if got != want {
		t.Errorf("long list: want %q, got %q", want, got)
	}
}

func TestFormatJobIDs_EmptyList(t *testing.T) {
	if got := formatJobIDs(nil); got != "[]" {
		t.Errorf("empty list: want [], got %q", got)
	}
}
