package channelimport

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	// Blank import: registers the pgx stdlib driver under the "pgx"
	// alias so sql.Open("pgx", dsn) works in the integration test
	// below (the CLI script uses the same registered name). The
	// integration test reads DATABASE_URL from env and skips if
	// unset — matches the rest of the migration/integration test
	// suite convention.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// fakeWorkspace is the deterministic test resolver for Parse.
// Returns id=42 for "alpha" and id=99 for "beta"; ok=false for
// anything else. Tests assert which call paths map which names.
func fakeWorkspace(name string) (int64, bool) {
	switch name {
	case "alpha":
		return 42, true
	case "beta":
		return 99, true
	default:
		return 0, false
	}
}

// TestParse_Happy asserts the canonical 8-column header + a
// well-formed data row yields a single ImportRow with every field
// mapped verbatim. Empty-string optional columns round-trip as
// empty strings (NOT nil) so the OAuth callback can search by key
// presence regardless of value.
func TestParse_Happy(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		"channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency",
		"UC123,My Channel,alice@example.com,alpha,priority-tier,it-IT,Europe/Rome,2/week",
	}, "\n"))
	rows, errs, err := Parse(csv, "youtube", fakeWorkspace)
	if err != nil {
		t.Fatalf("Parse happy: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("Parse happy: want 0 errs, got %v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("Parse happy: want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.ChannelID != "UC123" {
		t.Errorf("ChannelID: got %q, want UC123", r.ChannelID)
	}
	if r.ChannelName != "My Channel" {
		t.Errorf("ChannelName: got %q, want 'My Channel'", r.ChannelName)
	}
	if r.ManagerEmailHint != "alice@example.com" {
		t.Errorf("ManagerEmailHint: got %q", r.ManagerEmailHint)
	}
	if r.WorkspaceID != 42 {
		t.Errorf("WorkspaceID: got %d, want 42 (lookup alpha)", r.WorkspaceID)
	}
	if r.Group != "priority-tier" {
		t.Errorf("Group: got %q", r.Group)
	}
	if r.Language != "it-IT" {
		t.Errorf("Language: got %q", r.Language)
	}
	if r.Timezone != "Europe/Rome" {
		t.Errorf("Timezone: got %q", r.Timezone)
	}
	if r.ExpectedUploadFreqRaw != "2/week" {
		t.Errorf("ExpectedUploadFreqRaw: got %q", r.ExpectedUploadFreqRaw)
	}
	if r.Platform != "youtube" {
		t.Errorf("Platform: got %q, want 'youtube'", r.Platform)
	}
}

// TestParse_DefaultPlatformWhenEmpty covers the case where the
// caller doesn't pass a platform name (CLI / quick-test shape):
// Parse MUST default to "youtube" so the row lands as the canonical
// YouTube pending_authorization slot. Tests do not assume other
// platforms here — the canonical-user-spec interpretation is
// platform="youtube"; the OAuth callback may override later.
func TestParse_DefaultPlatformWhenEmpty(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		"channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency",
		"UCxx,,,alpha,,,,",
	}, "\n"))
	rows, _, err := Parse(csv, "", fakeWorkspace)
	if err != nil {
		t.Fatalf("Parse default platform: %v", err)
	}
	if rows[0].Platform != "youtube" {
		t.Errorf("default Platform: got %q, want 'youtube'", rows[0].Platform)
	}
}

// TestParse_MissingChannelID_SkipRow asserts that an empty
// channel_id is a per-row skip (Reason set to a clear
// operator-readable message) — NOT a global parse error — so a
// 500-channel CSV with a couple of bad rows still imports the
// other 498 instead of failing the whole batch.
func TestParse_MissingChannelID_SkipRow(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		"channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency",
		",name-only-row,,,,,,",                                 // missing channel_id
		"UCok,OK row,a@b,alpha,,,,",                            // valid
	}, "\n"))
	rows, errs, err := Parse(csv, "youtube", fakeWorkspace)
	if err != nil {
		t.Fatalf("Parse skip-row: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelID != "UCok" {
		t.Errorf("expect exactly 1 valid row (UCok); got %d rows", len(rows))
	}
	if len(errs) != 1 {
		t.Fatalf("expect 1 skip; got %d (errs=%v)", len(errs), errs)
	}
	if errs[0].RowNumber != 2 {
		t.Errorf("skip row number: got %d, want 2 (header is row 1)", errs[0].RowNumber)
	}
	if !strings.Contains(errs[0].Reason, "channel_id") {
		t.Errorf("skip Reason: got %q, want mention of channel_id", errs[0].Reason)
	}
}

// TestParse_UnresolvableWorkspace_SkipRow covers the case the
// operator typed a workspace name that doesn't exist (typo guard).
// The skip is per-row + Reason names the offending workspace so
// the operator can fix the sheet.
func TestParse_UnresolvableWorkspace_SkipRow(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		"channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency",
		"UCx,Foo,a@b,nosuch,priority-tier,it-IT,Europe/Rome,2/week",
		"UCy,Bar,c@d,alpha,,,,,",
	}, "\n"))
	rows, errs, err := Parse(csv, "youtube", fakeWorkspace)
	if err != nil {
		t.Fatalf("Parse unresolvable ws: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelID != "UCy" {
		t.Errorf("expect exactly 1 valid row (UCy); got %d", len(rows))
	}
	if len(errs) != 1 {
		t.Fatalf("expect 1 skip; got %d (errs=%v)", len(errs), errs)
	}
	if !strings.Contains(errs[0].Reason, "nosuch") {
		t.Errorf("skip Reason should name the offending workspace; got %q", errs[0].Reason)
	}
}

// TestParse_MissingColumn_HardError asserts that a header row
// missing a required column (e.g. "language" typoed-out) is a
// HARD error before any row parsing — operator gets the clearest
// possible signal ("missing required header column 'language'") so
// they fix the sheet, not 200 confusing row-level skips.
func TestParse_MissingColumn_HardError(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		// Deliberately omitted 'language'.
		"channel_id,channel_name,manager_email_hint,workspace,group,,timezone,expected_upload_frequency",
		"UCx,Foo,a@b,alphe,,,Europe/Rome,2/week",
	}, "\n"))
	_, _, err := Parse(csv, "youtube", fakeWorkspace)
	if err == nil {
		t.Fatal("Parse missing header column: want hard error, got nil")
	}
	if !strings.Contains(err.Error(), "language") {
		t.Errorf("error: got %q, want mention of 'language'", err.Error())
	}
}

// TestParse_NilWorkspaceLookup_PanicsAsError covers the
// fail-fast contract: workspaceLookup is REQUIRED. Operator code
// that accidentally calls Parse without a resolver sees a typed
// error rather than a silent NIL-deref panic deep in workspaceID
// resolution later.
func TestParse_NilWorkspaceLookup_PanicsAsError(t *testing.T) {
	csv := strings.NewReader("channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency\nUCx,Foo,a@b,alphe,,,,\n")
	_, _, err := Parse(csv, "youtube", nil)
	if err == nil {
		t.Fatal("Parse nil lookup: want typed error, got nil")
	}
	if !strings.Contains(err.Error(), "workspaceLookup") {
		t.Errorf("error: got %q, want mention of workspaceLookup", err.Error())
	}
}

// TestParse_TrimsChannelIDForLookup pins the whitespace-trim
// contract: "  UC123  " in the CSV must lookup the same as
// "UC123" so an operator who accidentally pasted extra whitespace
// doesn't end up with an "unresolvable workspace" skip due to a
// typo. (Workspace name itself is NOT trimmed — "+05:30" and
// " en-US " might be intentional; trimming + re-emitting would
// silently mutate operator data, see SanitizeForJSON godoc.)
func TestParse_TrimsChannelIDForLookup(t *testing.T) {
	csv := strings.NewReader(strings.Join([]string{
		"channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency",
		"  UC123  ,trimmed-channel,a@b,alpha,,,,",
	}, "\n"))
	rows, _, err := Parse(csv, "youtube", fakeWorkspace)
	if err != nil {
		t.Fatalf("Parse trim: %v", err)
	}
	if rows[0].ChannelID != "UC123" {
		t.Errorf("ChannelID after trim: got %q, want 'UC123'", rows[0].ChannelID)
	}
}

// TestMetaMap_RoundTrip asserts the JSONB-shape keys match the
// CSV column names so a future OAuth callback reading "metadata"
// finds entries under IDENTIFIERS the operator will recognise
// ("manager_email_hint" not "managerEmail").
func TestMetaMap_RoundTrip(t *testing.T) {
	r := ImportRow{
		ManagerEmailHint:      "a@b",
		Group:                 "g1",
		Language:              "en-US",
		Timezone:              "UTC",
		ExpectedUploadFreqRaw: "5/week",
	}
	m := r.MetaMap()
	wantKeys := []string{
		"manager_email_hint", "group", "language", "timezone",
		"expected_upload_frequency",
	}
	if len(m) != len(wantKeys) {
		t.Errorf("MetaMap keys: want %d, got %d (map=%v)", len(wantKeys), len(m), m)
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("MetaMap missing key %q (map=%v)", k, m)
		}
	}
	// Empty-string optional columns round-trip as empty strings
	// (NOT nil) so the OAuth callback sees a present key.
	if r.Group == "" {
		m2 := (ImportRow{}).MetaMap()
		if _, ok := m2["group"]; !ok {
			t.Error("MetaMap when Group empty: 'group' key missing — should round-trip as empty string")
		}
	}
}

// TestImportToDB_NoDB_NoRows covers the defensive error path:
// caller passes a nil *sql.DB MUST get a typed error rather than
// a panic when iterating rows.
func TestImportToDB_NoDB_NoRows(t *testing.T) {
	_, err := ImportToDB(context.Background(), nil, 1, nil)
	if err == nil {
		t.Fatal("ImportToDB nil db: want error, got nil")
	}
}

// TestImportToDB_ZeroOwnerUserID covers the negative input guard:
// a zero or negative ownerUserID is a configuration bug, not a
// per-row data error, so the call site must fail loud rather
// than writing rows stamped with user_id=0.
func TestImportToDB_ZeroOwnerUserID(t *testing.T) {
	_, err := ImportToDB(context.Background(), nil, 0, nil)
	if err == nil {
		t.Fatal("ImportToDB ownerUID 0: want error, got nil")
	}
}

// INT-DB test: only runs when DATABASE_URL is set AND the test
// container is reachable. The skip-if-no-DB convention matches the
// rest of the migration/integration test suite.
func TestImportToDB_DBIntegration_InsertAndReimport(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — integration test skipped")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Ensure the FK fixtures (user_id=1 must exist before our
	// import lands; mirrors the FK guard in the production CLI).
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name)
		VALUES (1, 'channelimport-test@instaedit.local', 'ChannelImport Test')
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("FK guard users(1): %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id)
		VALUES (1, 'ChannelImport Test Workspace', 1)
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("FK guard workspaces(1): %v", err)
	}

	cleanup := func(t *testing.T) {
		_, _ = db.Exec(`DELETE FROM platform_accounts WHERE platform_user_id = $1`,
			"UCimport-test-1")
	}
	cleanup(t)
	defer cleanup(t)

	// Pre-condition: no row with this channel_id.
	var preCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_accounts WHERE platform_user_id = $1`,
		"UCimport-test-1").Scan(&preCount); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if preCount != 0 {
		t.Fatalf("pre-condition: expected 0 rows for UCimport-test-1, got %d", preCount)
	}

	// INSERT run
	rows := []ImportRow{{
		ChannelID: "UCimport-test-1", ChannelName: "Import Test 1",
		WorkspaceID: 1, Platform: "youtube", Group: "alpha-tier",
		Language: "en-US", Timezone: "UTC",
		ExpectedUploadFreqRaw: "3/week",
	}}
	res, err := ImportToDB(context.Background(), db, 1, rows)
	if err != nil {
		t.Fatalf("ImportToDB first run: %v", err)
	}
	if res.Imported != 1 || res.Skipped != 0 {
		t.Errorf("first run: got Imported=%d Skipped=%d, want 1/0", res.Imported, res.Skipped)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM platform_accounts WHERE platform_user_id = $1`,
		"UCimport-test-1").Scan(&status); err != nil {
		t.Fatalf("status readback: %v", err)
	}
	if status != "pending_authorization" {
		t.Errorf("first-run status: got %q, want 'pending_authorization'", status)
	}

	// UPSERT second run: status MUST NOT change (still
	// pending_authorization), and the IndexedColumn must be the
	// same row (no INSERT-duplicate).
	rows2 := []ImportRow{{
		ChannelID: "UCimport-test-1", ChannelName: "Renamed Import Test",
		WorkspaceID: 1, Platform: "youtube", Group: "beta-tier",
		Language: "fr-FR", Timezone: "Europe/Paris",
		ExpectedUploadFreqRaw: "1/month",
	}}
	res2, err := ImportToDB(context.Background(), db, 1, rows2)
	if err != nil {
		t.Fatalf("ImportToDB second run: %v", err)
	}
	if res2.Imported != 1 || res2.Skipped != 0 {
		t.Errorf("second run: got Imported=%d Skipped=%d, want 1/0", res2.Imported, res2.Skipped)
	}
	var postCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_accounts WHERE platform_user_id = $1`,
		"UCimport-test-1").Scan(&postCount); err != nil {
		t.Fatalf("post-count: %v", err)
	}
	if postCount != 1 {
		t.Errorf("UPSERT check: expected 1 row (NOT INSERT-duplicate), got %d", postCount)
	}
	// Fly back the new metadata the operator inserted in run 2.
	var (
		gotChannelName  string
		gotWorkspaceID  int64
	)
	if err := db.QueryRow(
		`SELECT username, workspace_id FROM platform_accounts WHERE platform_user_id = $1`,
		"UCimport-test-1",
	).Scan(&gotChannelName, &gotWorkspaceID); err != nil {
		t.Fatalf("second-run readback: %v", err)
	}
	if gotChannelName != "Renamed Import Test" {
		t.Errorf("UPSERT username: got %q, want 'Renamed Import Test'", gotChannelName)
	}
	if gotWorkspaceID != 1 {
		t.Errorf("UPSERT workspace_id: got %d, want 1", gotWorkspaceID)
	}
}
