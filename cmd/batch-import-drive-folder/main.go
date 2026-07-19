// Command batch-import-drive-folder drives the
// /api/v1/media/import/drive/folder endpoint in a paginated loop until
// next_page_token is empty. It is an operator-facing CLI for the case
// where a Drive folder contains >200 videos and the user wants to
// auto-page through them without manually copying page_token values
// into curl.
//
// Configuration is environment-only (no flags) so it composes cleanly
// with `make` targets / Docker runtimes / shell scripts:
//
//	APP_HOST                  (default http://localhost:8080)
//	COOKIE_FILE               path to a curl-style cookies.txt
//	FOLDER_ID                 Drive folder id (the part after /folders/)
//	PAGE_TOKEN                optional, initial page token (empty = page 1)
//	CURSOR                    optional, RFC3339 (empty = NOW())
//	WORKSPACE_ID              required (int)
//	FACEBOOK_ACCOUNT_ID       required (int)
//	MIN_JITTER_SECONDS        optional (int, >= 0). Floor of the random gap
//	                          between consecutive scheduled posts. Unset/0
//	                          → server default (60 s).
//	MAX_JITTER_SECONDS        optional (int, >= MIN_JITTER_SECONDS). Ceiling
//	                          of the same uniform gap. Unset/0 → server
//	                          default (3600 s). If you want exact-N-second
//	                          cadence, set both to N.
//	GOOGLE_DRIVE_API_KEY      informational only (server-side)
//
// Abort: SIGINT (Ctrl-C) or SIGTERM. The in-flight HTTP request is
// cancelled and the CLI exits 130 with a final tally of jobs that
// were scheduled before the abort.
//
// Jitter field semantics: when both MIN and MAX are unset (or 0) the
// CLI omits them from the request body so the server keeps its
// 60-3600 s fallback. Setting either to >0 means "use this value";
// both must be set + MIN <= MAX, otherwise loadConfig fails fast.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Env-var names. Exported so tests reference a single source of truth.
const (
	EnvAPIBase           = "APP_HOST"
	EnvCookieFile        = "COOKIE_FILE"
	EnvFolderID          = "FOLDER_ID"
	EnvPageToken         = "PAGE_TOKEN"
	EnvCursor            = "CURSOR"
	EnvWorkspaceID       = "WORKSPACE_ID"
	EnvFacebookAccountID = "FACEBOOK_ACCOUNT_ID"
	EnvMinJitterSeconds  = "MIN_JITTER_SECONDS"
	EnvMaxJitterSeconds  = "MAX_JITTER_SECONDS"
	EnvDriveAPIKey       = "GOOGLE_DRIVE_API_KEY"
)

// requiredEnvs are checked up-front; if any is empty the CLI prints a
// single multi-env error pointing at every missing one so the operator
// sets them all in one pass (avoiding the "fix one, rerun, hit the
// next missing env" pattern).
var requiredEnvs = []string{
	EnvCookieFile,
	EnvFolderID,
	EnvWorkspaceID,
	EnvFacebookAccountID,
}

// Config holds the values parsed from process env at startup.
type Config struct {
	APIBase                  string
	CookieFile               string
	FolderID                 string
	PageToken                string
	CursorRFC3339            string
	WorkspaceID              int64
	FacebookAccountID        int64
	// MinJitterSeconds / MaxJitterSeconds are forwarded on every page
	// request when non-zero; zero means "omit and let the server apply
	// its 60-3600 s default". Both must be >= 0 and MIN <= MAX when
	// set; loadConfig() rejects invalid combinations up-front.
	MinJitterSeconds int64
	MaxJitterSeconds int64
	DriveAPIKeyInformational string // logged for the operator only
}

func loadConfig() (Config, error) {
	cfg := Config{
		APIBase:                  envOrDefault(EnvAPIBase, "http://localhost:8080"),
		CookieFile:               strings.TrimSpace(os.Getenv(EnvCookieFile)),
		FolderID:                 strings.TrimSpace(os.Getenv(EnvFolderID)),
		PageToken:                strings.TrimSpace(os.Getenv(EnvPageToken)),
		CursorRFC3339:            strings.TrimSpace(os.Getenv(EnvCursor)),
		WorkspaceID:              parseInt64(os.Getenv(EnvWorkspaceID), 0),
		FacebookAccountID:        parseInt64(os.Getenv(EnvFacebookAccountID), 0),
		MinJitterSeconds:         parseInt64(os.Getenv(EnvMinJitterSeconds), 0),
		MaxJitterSeconds:         parseInt64(os.Getenv(EnvMaxJitterSeconds), 0),
		DriveAPIKeyInformational: strings.TrimSpace(os.Getenv(EnvDriveAPIKey)),
	}
	var missing []string
	for _, e := range requiredEnvs {
		if strings.TrimSpace(os.Getenv(e)) == "" {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	// Jitter validation: OPTIONAL but the only valid configurations
	// are "BOTH set AND > 0" (forward to server) or "BOTH unset / 0"
	// (server default via JSON omitempty). Mixed states like
	// "MIN=0 MAX>0" are rejected because Go's omitempty on int64 drops
	// the zero field, leaving the server with an ambiguous single
	// number (a single int in the body could be read as min OR max).
	hasMin := cfg.MinJitterSeconds != 0
	hasMax := cfg.MaxJitterSeconds != 0
	if hasMin != hasMax {
		return cfg, fmt.Errorf("if either %s or %s is set, BOTH must be set (got min=%d max=%d). Set both to 0 (or unset both) to use server defaults.",
			EnvMinJitterSeconds, EnvMaxJitterSeconds, cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
	}
	if hasMin {
		if cfg.MinJitterSeconds < 0 || cfg.MaxJitterSeconds < 0 {
			return cfg, fmt.Errorf("%s and %s must be >= 0 (got min=%d max=%d)",
				EnvMinJitterSeconds, EnvMaxJitterSeconds, cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
		}
		if cfg.MinJitterSeconds > cfg.MaxJitterSeconds {
			return cfg, fmt.Errorf("%s (%d) must be <= %s (%d)",
				EnvMinJitterSeconds, cfg.MinJitterSeconds,
				EnvMaxJitterSeconds, cfg.MaxJitterSeconds)
		}
	}
	return cfg, nil
}

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func parseInt64(s string, def int64) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// cookieFile holds the two cookies the CLI needs: the session cookie
// (auth) and the csrf_token cookie (CSRF double-submit for the POST).
// Both are HttpOnly in production but Curl's `-c` flag strips the
// HttpOnly_ prefix on lines starting with `#HttpOnly_<domain>`; we
// accept both shapes.
type cookieFile struct {
	session string
	csrf    string
}

func parseCookieFile(path string) (cookieFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cookieFile{}, fmt.Errorf("read cookie file %q: %w", path, err)
	}
	var out cookieFile
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip plain `# …` comments; HttpOnly cookies come in with a
		// `#HttpOnly_<domain>` prefixed line that we strip before parsing.
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}
		line = strings.TrimPrefix(line, "#HttpOnly_")
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		switch parts[5] {
		case "session":
			out.session = parts[6]
		case "csrf_token":
			out.csrf = parts[6]
		}
	}
	if err := scanner.Err(); err != nil {
		return cookieFile{}, fmt.Errorf("scan cookie file %q: %w", path, err)
	}
	return out, nil
}

// entry mirrors pkg/api/drive_batch.go's DriveBatchImportItem.
type entry struct {
	Index       int       `json:"index"`
	DriveFileID string    `json:"drive_file_id"`
	Name        string    `json:"name"`
	JobID       int64     `json:"job_id"`
	PublishAt time.Time `json:"scheduled_at"`
}

// pageResponse mirrors pkg/api/drive_batch.go's DriveBatchImportResponse.
// Only the fields the CLI operates on are populated.
type pageResponse struct {
	FolderID               string    `json:"folder_id"`
	ScheduledCount         int       `json:"scheduled_count"`
	FirstPublishAt         time.Time `json:"first_publish_at"`
	LastScheduledAt        time.Time `json:"last_scheduled_at"`
	NextPageToken          string    `json:"next_page_token"`
	Note                   string    `json:"note"`
	NeedsGoogleDriveAPIKey bool      `json:"needs_google_drive_api_key"`
	NeedsDriveAccount      bool      `json:"needs_drive_account"`
	Error                  string    `json:"error"`
	Entries                []entry   `json:"entries"`
}

// pageBody is the JSON POST body sent on every iteration. omitempty
// on PageToken / CursorScheduledAt means the first call (no token,
// no cursor) sends a minimal body; subsequent calls include the
// servers's last_scheduled_at as the cursor so the cumulative jitter
// continues uninterrupted across pages.
//
// MinJitterSeconds / MaxJitterSeconds also carry omitempty so an
// operator who doesn't care about cadence (or accepts the server
// 60-3600 s default) doesn't accidentally pin a narrow gap. Setting
// MIN == MAX == N produces an exact-N-second cadence; setting them
// apart lets the server apply uniform-random jitter in that range,
// which the publish worker's anti-pattern detection treats as more
// human-like.
type pageBody struct {
	FolderID          string     `json:"folder_id"`
	WorkspaceID       int64      `json:"workspace_id"`
	FacebookAccountID int64      `json:"facebook_account_id"`
	PageToken         string     `json:"page_token,omitempty"`
	CursorScheduledAt *time.Time `json:"cursor_scheduled_at,omitempty"`
	MinJitterSeconds  int64      `json:"min_jitter_seconds,omitempty"`
	MaxJitterSeconds  int64      `json:"max_jitter_seconds,omitempty"`
}

// transportFn is a small interface so tests can plug an httptest
// server without depending on a real network.
type transportFn func(*http.Request) (*http.Response, error)

// retryBackoffs is package-level so tests can shrink it. Production
// values give 4 total attempts with exponential 2/4/8s waits between
// them (max ~14s before fail-fast on a stuck 5xx).
var retryBackoffs = []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// callPage POSTs one page. Retries 5xx + network errors with
// exponential backoff (2/4/8s, 4 attempts total); fail-fast on 4xx or
// on server-side needs_* guidance. Returns (parsed-body, last-status-code, err).
func callPage(ctx context.Context, apiBase, session, csrf string, body []byte, do transportFn) (*pageResponse, int, error) {
	url := strings.TrimRight(apiBase, "/") + "/api/v1/media/import/drive/folder"
	// retryBackoffs[0]=0 = first attempt without wait; last entry = final retry window.
	backoffs := retryBackoffs
	var lastStatus int
	for attempt, wait := range backoffs {
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, lastStatus, ctx.Err()
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, lastStatus, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "session="+session+"; csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := do(req)
		if err != nil {
			lastStatus = 0
			if errors.Is(err, context.Canceled) {
				return nil, lastStatus, err
			}
			if attempt < len(backoffs)-1 {
				slog.Warn("HTTP transport error, retrying", "attempt", attempt+1, "err", err)
				continue
			}
			return nil, lastStatus, fmt.Errorf("HTTP after %d attempts: %w", attempt+1, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		// Retry on transient status codes: 5xx (server blip), 429
		// (rate-limited; the endpoint has its own bucket), and 408
		// (timeout). Per HTTP convention 429 may carry a Retry-After
		// header we intentionally do not parse — exponential backoff
		// with bounded attempts is simpler and avoids honouring a
		// long server-suggested wait that the operator can override
		// by killing the CLI.
		if resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if attempt < len(backoffs)-1 {
				slog.Warn("transient status, retrying", "status", resp.StatusCode, "attempt", attempt+1)
				continue
			}
			return nil, lastStatus, fmt.Errorf("server returned %d after %d attempts: %s", resp.StatusCode, attempt+1, string(raw))
		}
		if resp.StatusCode >= 400 {
			return nil, lastStatus, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(raw))
		}
		var parsed pageResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, lastStatus, fmt.Errorf("decode response body: %w (raw prefix: %.200s)", err, string(raw))
		}
		return &parsed, lastStatus, nil
	}
	return nil, lastStatus, nil // unreachable
}

func buildPageBody(cfg Config, pageToken string, cursor *time.Time) []byte {
	body := pageBody{
		FolderID:          cfg.FolderID,
		WorkspaceID:       cfg.WorkspaceID,
		FacebookAccountID: cfg.FacebookAccountID,
		PageToken:         pageToken,
		CursorScheduledAt: cursor,
		MinJitterSeconds:  cfg.MinJitterSeconds,
		MaxJitterSeconds:  cfg.MaxJitterSeconds,
	}
	b, _ := json.Marshal(body)
	return b
}

// runChain drives the page loop until next_page_token is empty or the
// context is cancelled. Writes per-page + progress logs to out.
// Returns the desired exit code (0 = done, 1 = error, 2 = config gap,
// 130 = aborted).
func runChain(ctx context.Context, cfg Config, session, csrf string, do transportFn, out io.Writer) int {
	var pageNum int
	var totalScheduled int

	var cursor *time.Time
	if cfg.CursorRFC3339 != "" {
		t, err := time.Parse(time.RFC3339, cfg.CursorRFC3339)
		if err != nil {
			fmt.Fprintf(out, "[fatal] CURSOR parse (%q): %v (use RFC3339 like 2026-07-19T07:30:00Z)\n",
				cfg.CursorRFC3339, err)
			return 1
		}
		cursor = &t
	}
	pageToken := cfg.PageToken

	fmt.Fprintf(out, "[start] folder_id=%s workspace_id=%d facebook_account_id=%d initial_page_token=%q initial_cursor=%s api_host=%s\n",
		cfg.FolderID, cfg.WorkspaceID, cfg.FacebookAccountID, pageToken, formatTimePtr(cursor), cfg.APIBase)

	for {
		body := buildPageBody(cfg, pageToken, cursor)
		result, status, err := callPage(ctx, cfg.APIBase, session, csrf, body, do)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintf(out, "[abort] in-flight request cancelled mid-page. Total scheduled so far: %d jobs across %d pages.\n",
					totalScheduled, pageNum)
				return 130
			}
			fmt.Fprintf(out, "[fatal] page %d failed (status=%d): %v\n", pageNum+1, status, err)
			return 1
		}
		if result.NeedsGoogleDriveAPIKey || result.NeedsDriveAccount {
			fmt.Fprintf(out, "[fatal] server rejected list (config gap on server): needs_google_drive_api_key=%v needs_drive_account=%v note=%q\n",
				result.NeedsGoogleDriveAPIKey, result.NeedsDriveAccount, result.Note)
			return 2
		}
		if result.Error != "" {
			fmt.Fprintf(out, "[fatal] server returned error: %s\n", result.Error)
			return 1
		}

		pageNum++
		totalScheduled += result.ScheduledCount

		jobIDs := make([]int64, 0, len(result.Entries))
		for _, e := range result.Entries {
			jobIDs = append(jobIDs, e.JobID)
		}

		fmt.Fprintf(out, "[page=%d scheduled=%d first_publish=%s last_scheduled=%s jitter=%s job_ids=%s next_page_token=%q note=%q]\n",
			pageNum,
			result.ScheduledCount,
			formatTime(result.FirstPublishAt),
			formatTime(result.LastScheduledAt),
			formatJitterEcho(cfg),
			formatJobIDs(jobIDs),
			result.NextPageToken,
			result.Note,
		)

		if result.NextPageToken == "" {
			fmt.Fprintf(out, "[done] total %d jobs across %d pages (next_page_token empty)\n",
				totalScheduled, pageNum)
			return 0
		}
		pageToken = result.NextPageToken
		cursor = &result.LastScheduledAt
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "(none)"
	}
	return t.UTC().Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "(none)"
	}
	return formatTime(*t)
}

// formatJitterEcho renders the per-page log's jitter label so a
// 50-page run that fails mid-pagination tells the operator the active
// cadence without scrolling back to bootLog. Empty string when cfg
// has jitter unset (the bootLog line already explains the
// "server-default" choice).
func formatJitterEcho(cfg Config) string {
	if cfg.MinJitterSeconds == 0 && cfg.MaxJitterSeconds == 0 {
		return "server-default (60-3600s)"
	}
	return fmt.Sprintf("min=%ds/max=%ds", cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
}

// formatJobIDs prints `[101,102,103,…N…,201,202,203]` for long lists so
// the operator sees the first + last few IDs even on a 200-item
// page. Short lists print inline.
func formatJobIDs(ids []int64) string {
	const preview = 5
	if len(ids) == 0 {
		return "[]"
	}
	if len(ids) <= preview*2+1 {
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	parts := make([]string, 0, preview*2+3)
	for i := 0; i < preview; i++ {
		parts = append(parts, strconv.FormatInt(ids[i], 10))
	}
	parts = append(parts, "…"+strconv.Itoa(len(ids)-preview*2)+"…")
	for i := len(ids) - preview; i < len(ids); i++ {
		parts = append(parts, strconv.FormatInt(ids[i], 10))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func bootLog(out io.Writer, cfg Config) {
	fmt.Fprintln(out, "batch-import-drive-folder — auto-paginate /api/v1/media/import/drive/folder")
	fmt.Fprintln(out, "Send SIGINT (Ctrl-C) or SIGTERM to abort (skips remaining pages).")
	if cfg.DriveAPIKeyInformational == "" {
		fmt.Fprintln(out, "[info] GOOGLE_DRIVE_API_KEY is unset in this shell. Public folders will be denied unless you pass drive_account_id server-side.")
	} else {
		fmt.Fprintf(out, "[info] GOOGLE_DRIVE_API_KEY present in env (len=%d) — server-side Drive listing can use it.\n",
			len(cfg.DriveAPIKeyInformational))
	}
	if cfg.MinJitterSeconds > 0 || cfg.MaxJitterSeconds > 0 {
		fmt.Fprintf(out, "[info] jitter override active: min=%ds max=%ds (forwarded on every page)\n",
			cfg.MinJitterSeconds, cfg.MaxJitterSeconds)
	} else {
		fmt.Fprintln(out, "[info] jitter override unset → using server default (60-3600s). Set MIN_JITTER_SECONDS / MAX_JITTER_SECONDS to override.")
	}
}

func main() {
	out := os.Stdout

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(out, "[fatal] config: %v (set the listed env vars and rerun)\n", err)
		os.Exit(1)
	}

	bootLog(out, cfg)

	cookies, err := parseCookieFile(cfg.CookieFile)
	if err != nil {
		fmt.Fprintf(out, "[fatal] cookies: %v\n", err)
		os.Exit(2)
	}
	if cookies.session == "" {
		fmt.Fprintf(out, "[fatal] cookie file %q has no 'session' cookie. Re-login to refresh:\n  curl -c %s -X POST %s/api/v1/auth/login -d '{\"email\":\"...\",\"password\":\"...\"}'\n",
			cfg.CookieFile, cfg.CookieFile, cfg.APIBase)
		os.Exit(2)
	}
	if cookies.csrf == "" {
		fmt.Fprintf(out, "[fatal] cookie file %q is missing csrf_token. The endpoint is POST and CSRF-protected; this binary will 403 without it. Re-export your cookies (use your browser session that visited /login first).\n", cfg.CookieFile)
		os.Exit(2)
	}

	// signal.NotifyContext bridges SIGINT/SIGTERM into ctx.Done() so
	// the loop can check it cleanly between iterations, and the
	// in-flight HTTP request gets cancelled automatically (via
	// NewRequestWithContext propagation).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient := &http.Client{Timeout: 60 * time.Second}
	transport := func(req *http.Request) (*http.Response, error) { return httpClient.Do(req) }

	code := runChain(ctx, cfg, cookies.session, cookies.csrf, transport, out)
	os.Exit(code)
}
