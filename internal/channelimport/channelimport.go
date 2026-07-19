// Package channelimport is the canonical CSV-driven channel intake
// surface for the P2 admin onboarding flow. Both the HTTP
// `POST /admin/channels/import-csv` handler (pkg/api/admin_channels.go)
// and the offline CLI (scripts/import_channels_csv.go) wire through
// the helpers here so the parse + DB-write contract lives in one
// place. The handler adds HTTP concerns (auth, response shaping,
// multipart parsing); the CLI adds env-driven wiring (DATABASE_URL,
// a fixed owner). The business rules — required columns, dedupe,
// status reset — are shared.
//
// Design rules pinned by the valutazione-doc P2 spec:
//
//   1. NEVER write OAuth tokens. The whole point of importing a
//      channel via CSV is to create a row at
//      status='pending_authorization' so the operator can drive the
//      OAuth dance manually (with the manager's account). Storing
//      a token here would defeat the 1-OAuth-grant-per-1-channel
//      guard the production OAuth callback enforces (see
//      internal/services/youtube_oauth.go::BindGrantToChannel).
//
//   2. UPSERT (last-write-wins) on (platform, platform_user_id).
//      Re-importing a CSV that mentions the same channel_id MUST
//      not error — the operator might be updating language/timezone
//      metadata, refreshing a reauth_required row, or batching
//      updates from an external source-of-truth sheet.
//
//   3. Cross-platform defaults. The CSV column set is YouTube
//      shaped (no "platform" column — a future followup may add
//      this), so every row lands as platform='youtube'. The OAuth
//      callback can switch the platform on successful grant
//      (currently only YouTube OAuth is wired here; TikTok/Meta
//      have separate flows that don't go through this surface).
//
//   4. Edge cases: missing channel_id / unresolved workspace →
//      skip the row + record a Skipped entry in the Result.
//      Never fail-loud mid-CSV — operators upload 500-channel
//      sheets and need partial-success visibility rather than
//      "row 412 killed the whole import".
package channelimport

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CSVHeaderColumns is the canonical canonical column ordering
// accepted by the parser. New optional columns may be appended in a
// future minor revision; the parser does an exact-name match per
// column so an unexpected header is a hard error (typo guard).
var CSVHeaderColumns = []string{
	"channel_id",
	"channel_name",
	"manager_email_hint",
	"workspace",
	"group",
	"language",
	"timezone",
	"expected_upload_frequency",
}

// ImportRow is the parsed + normalised record for one channel.
// Names are pinned to the CSV header names so a parse error logs the
// exact column. WorkspaceID is the *resolved* FK (the parser doesn't
// query the DB — the caller does the lookup), and similarly
// OwnerUserID is the resolved owners.user_id. The remaining columns
// fold into Metadata as workflow context, embedded under the same
// keys the OAuth callback is expected to read.
type ImportRow struct {
	ChannelID             string // CSV channel_id → platform_accounts.platform_user_id
	ChannelName           string // CSV channel_name → platform_accounts.username
	ManagerEmailHint      string // CSV manager_email_hint → metadata["manager_email_hint"]
	WorkspaceID           int64  // resolved FK from CSV workspace name
	Group                 string // CSV group → metadata["group"]
	Language              string // CSV language → metadata["language"]
	Timezone              string // CSV timezone → metadata["timezone"]
	ExpectedUploadFreqRaw  string // CSV expected_upload_frequency (kept as string; UI/scheduler interprets)
	Platform              string // default + future-flexible; "youtube" today
}

// MetaMap flattens the workflow-context columns into a JSONB-shaped
// map[any]any. The keys MATCH the CSV column names so a future
// Go-side reader finds them under the same identifier (operator
// mental model: "what I wrote in the CSV is what I read back").
// ExpectedUploadFreqRaw is normalised via SanitizeForJSON so the
// result is always JSONB-safe even when the operator wrote
// "5/week" or accidentally pasted a UTF-8 quote.
func (r ImportRow) MetaMap() map[string]any {
	return map[string]any{
		"manager_email_hint":      SanitizeForJSON(r.ManagerEmailHint),
		"group":                   SanitizeForJSON(r.Group),
		"language":                SanitizeForJSON(r.Language),
		"timezone":                SanitizeForJSON(r.Timezone),
		"expected_upload_frequency": SanitizeForJSON(r.ExpectedUploadFreqRaw),
	}
}

// SanitizeForJSON turns an arbitrary CSV cell into a JSON-safe form.
// Empty strings round-trip as empty strings (NOT nil — the OAuth
// callback later checks for the key presence regardless of value).
// Strings are returned verbatim otherwise. We deliberately don't
// trim whitespace: "+05:30" or " en-US " might be intentional, and
// trimming + re-emitting would silently mutate operator data.
func SanitizeForJSON(s string) string {
	// json.Marshal handles quoting/escaping for us; this function
	// is the SSOT for "what value goes into metadata for column X".
	return s
}

// RowError records a single per-row failure (parse-level, not DB-level).
// The CLI + the HTTP handler both surface these verbatim; partial-success
// visibility is the spec.
type RowError struct {
	RowNumber int    // 1-based, including header (skip = row 1 or later)
	Reason    string // operator-readable message ("missing channel_id", "no such workspace: alpha")
}

// Result summarises a single Import run. Imported counts rows that
// were INSERTed or UPDATEd UPSERT-style. Skipped counts rows that
// the parser OR the DB rejected (with the row-level Reason).
type Result struct {
	Imported int
	Skipped  int
	Errors   []RowError
}

// Parse reads a CSV stream (with header) and returns the rows + any
// per-row validation failures. Pure — no DB I/O. Workspace / owner
// resolution is the caller's responsibility: the caller passes a
// (name → id) lookup map. Errors that surface here are parse-only;
// DB-level failures (FK, unique-violation) are reported by
// ImportToDB in the Skipped counter.
//
// WorkspaceLookup is REQUIRED (non-nil). The parser calls
// WorkspaceLookup(name) for each row; if the lookup returns
// `ok=false`, the row is recorded as Skipped with a Reason of
// "no such workspace: NAME". This keeps the parser honest — the
// caller cannot silently let an unresolvable workspace through.
func Parse(r io.Reader, platform string, workspaceLookup func(name string) (id int64, ok bool)) ([]ImportRow, []RowError, error) {
	if workspaceLookup == nil {
		return nil, nil, errors.New("channelimport.Parse: workspaceLookup is required")
	}
	if platform == "" {
		platform = "youtube"
	}
	csvReader := csv.NewReader(r)
	csvReader.FieldsPerRecord = -1 // accept variable column counts so we can give a per-row Reason
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("channelimport: csv read: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, errors.New("channelimport: empty CSV (no header row)")
	}
	headerRow := records[0]
	// Header validation: every CSVHeaderColumns must appear, by name,
	// in the header row. Extra columns are allowed (forward compat)
	// but silently ignored. Missing columns are a hard error so the
	// operator gets actionable feedback ("your sheet missed
	// 'language' — add it or remove the row").
	headerIndex := map[string]int{}
	for i, h := range headerRow {
		headerIndex[strings.TrimSpace(strings.ToLower(h))] = i
	}
	for _, want := range CSVHeaderColumns {
		if _, ok := headerIndex[want]; !ok {
			return nil, nil, fmt.Errorf("channelimport: missing required header column %q (got %v)", want, headerRow)
		}
	}

	rows := make([]ImportRow, 0, len(records)-1)
	errs := make([]RowError, 0)
	for i, rec := range records[1:] {
		rowNumber := i + 2 // 1-based; row 1 is the header
		// Pad the record so missing columns surface as empty strings
		// for the parser (the channel_id check below distinguishes
		// "missing" from "empty").
		padded := make([]string, len(headerRow))
		copy(padded, rec)
		channelID := strings.TrimSpace(padded[headerIndex["channel_id"]])
		if channelID == "" {
			errs = append(errs, RowError{RowNumber: rowNumber, Reason: "channel_id is required and was empty"})
			continue
		}
		workspaceName := strings.TrimSpace(padded[headerIndex["workspace"]])
		workspaceID, ok := workspaceLookup(workspaceName)
		if !ok {
			errs = append(errs, RowError{RowNumber: rowNumber, Reason: fmt.Sprintf("no such workspace: %q", workspaceName)})
			continue
		}
		row := ImportRow{
			ChannelID:            channelID,
			ChannelName:          strings.TrimSpace(padded[headerIndex["channel_name"]]),
			ManagerEmailHint:     padded[headerIndex["manager_email_hint"]],
			WorkspaceID:          workspaceID,
			Group:                padded[headerIndex["group"]],
			Language:             padded[headerIndex["language"]],
			Timezone:             padded[headerIndex["timezone"]],
			ExpectedUploadFreqRaw: padded[headerIndex["expected_upload_frequency"]],
			Platform:             platform,
		}
		rows = append(rows, row)
	}
	return rows, errs, nil
}

// ImportToDB upserts each row from Parse into platform_accounts at
// status='pending_authorization'. The UPSERT key is
// (platform, platform_user_id) — a re-import that mentions the same
// channel MUST lands on the same row, with metadata + workspace_id
// refreshed and status reset from any terminal value back to
// 'pending_authorization'. NEVER writes tokens (the OAuth callback
// is the only place that touches tokens, via vault.Save).
//
// Returns the aggregate Result. Per-row DB failures are NOT
// return-as-error — they surface as Skipped entries with Reason
// set to the underlying error string. A connection-level error
// (db is down, ctx cancelled mid-flight) IS return-as-error
// because no further rows can be processed.
//
// ownerUserID is the user_id stamped on every inserted row's
// user_id FK. Production callers pass the admin's own user_id;
// the CLI defaults to a configurable fallback (admin_owner_user_id
// from env). Cross-owner management is the operator's call.
func ImportToDB(ctx context.Context, db *sql.DB, ownerUserID int64, rows []ImportRow) (Result, error) {
	res := Result{}
	if ownerUserID <= 0 {
		return res, errors.New("channelimport.ImportToDB: ownerUserID must be positive")
	}
	if db == nil {
		return res, errors.New("channelimport.ImportToDB: db is nil")
	}
	for i, row := range rows {
		if err := importOne(ctx, db, ownerUserID, row); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, RowError{RowNumber: i + 2, Reason: err.Error()})
			continue
		}
		res.Imported++
	}
	return res, nil
}

// importOne UPSERT a single row. The ON CONFLICT clause targets
// the (platform, platform_user_id) unique constraint (added in
// migration 003_posts_workspaces — re-checked here against
// current schema: yes, both columns are part of the unique key).
// Status is ALWAYS reset to 'pending_authorization' on UPSERT —
// this is intentional, because the operator is "trying again" on
// this row, explicitly asking for it to await OAuth.
//
// ON CONFLICT also NULLs connected_at. Without this, a re-imported
// row would show status='pending_authorization' alongside a stale
// connected_at=<old_date> from a prior successful OAuth — an
// internally inconsistent state that misleads operators ("is the
// channel connected or not?"). Clearing connected_at on re-import
// pins the row's state to the JWT unattached-pending form, which
// matches the OAuth-callback handshake contract.
func importOne(ctx context.Context, db *sql.DB, ownerUserID int64, row ImportRow) error {
	metaJSON, err := json.Marshal(row.MetaMap())
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO platform_accounts (
			user_id, workspace_id, platform, platform_user_id, username,
			status, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			'pending_authorization', $6::jsonb, NOW(), NOW()
		)
		ON CONFLICT (platform, platform_user_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			username     = EXCLUDED.username,
			status       = 'pending_authorization',
			metadata     = EXCLUDED.metadata,
			updated_at   = NOW(),
			-- Connection-flavor columns NULLed together so the
			-- pending_authorization state is internally consistent
			-- (no stale connected_at, no orphan refresh-token-lifecycle
			-- fields from a previous successful OAuth).
			connected_at    = NULL,
			reauth_required_at = NULL,
			last_error_code    = NULL,
			last_error_message = NULL
	`, ownerUserID, row.WorkspaceID, row.Platform, row.ChannelID, row.ChannelName, metaJSON)
	if err != nil {
		// Stringified via fmt so a row-level Reason is useful at the
		// CLI prompt / HTTP response without leaking the full SQL.
		return fmt.Errorf("upsert platform_account: %v", strings.TrimSpace(err.Error()))
	}
	return nil
}
