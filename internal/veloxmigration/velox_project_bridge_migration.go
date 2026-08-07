// Package veloxmigration contains the one-time, relationship-only migration
// from existing Velox editor handles to InstaEdit application projects.
// It never reads or writes editor snapshots, revisions, assets, or renders.
package veloxmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const ReportVersion = "instaedit.velox.bridge-migration.v1"

var (
	ErrInvalidMapping    = errors.New("invalid Velox bridge mapping")
	ErrUnsafeApply       = errors.New("migration apply refused because the report is not fully safe")
	ErrRollbackUnsafe    = errors.New("rollback refused because a bridge changed after migration")
	ErrMigrationNotReady = errors.New("Velox bridge migration schema is not ready")
)

type Mapping struct {
	ExternalProjectID string `json:"external_project_id"`
	ProjectID         string `json:"project_id"`
}

type Status string

const (
	StatusMatched       Status = "matched"
	StatusCreated       Status = "created"
	StatusAlreadyLinked Status = "already_linked"
	StatusMissing       Status = "missing"
	StatusAmbiguous     Status = "ambiguous"
	StatusConflict      Status = "conflict"
)

type Entry struct {
	Mapping     Mapping   `json:"mapping"`
	Status      Status    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	WorkspaceID int64     `json:"workspace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

type Summary struct {
	Total         int `json:"total"`
	Matched       int `json:"matched"`
	Created       int `json:"created"`
	AlreadyLinked int `json:"already_linked"`
	Missing       int `json:"missing"`
	Ambiguous     int `json:"ambiguous"`
	Conflicts     int `json:"conflicts"`
}

type Report struct {
	Version    string    `json:"version"`
	RunID      string    `json:"run_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DryRun     bool      `json:"dry_run"`
	Applied    bool      `json:"applied"`
	Aborted    bool      `json:"aborted,omitempty"`
	Error      string    `json:"error,omitempty"`
	Entries    []Entry   `json:"entries"`
	Summary    Summary   `json:"summary"`
}

type Options struct {
	DryRun bool
	RunID  string
}

func VerifyMigrationReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("%w: database is required", ErrMigrationNotReady)
	}
	var columnExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'velox_project_bridges' AND column_name = 'migration_run_id')`).Scan(&columnExists); err != nil {
		return fmt.Errorf("%w: check migration_run_id column: %v", ErrMigrationNotReady, err)
	}
	if !columnExists {
		return fmt.Errorf("%w: apply 114_velox_project_bridge_run_id.sql first", ErrMigrationNotReady)
	}
	var bridgeMigrationsRecorded int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename IN ('112_velox_project_bridges.sql', '114_velox_project_bridge_run_id.sql', '115_velox_project_bridge_editor_metadata.sql', '116_velox_project_bridge_minimal.sql')`).Scan(&bridgeMigrationsRecorded); err != nil {
		return fmt.Errorf("%w: check schema_migrations: %v", ErrMigrationNotReady, err)
	}
	if bridgeMigrationsRecorded != 4 {
		return fmt.Errorf("%w: bridge migration chain is incomplete; apply migrations 112, 114, 115 and 116 first", ErrMigrationNotReady)
	}
	var legacyColumns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'velox_project_bridges' AND column_name IN ('velox_project_id', 'platform', 'platform_account_id', 'channel_id', 'video_id', 'language')`).Scan(&legacyColumns); err != nil {
		return fmt.Errorf("%w: check legacy bridge columns: %v", ErrMigrationNotReady, err)
	}
	if legacyColumns != 0 {
		return fmt.Errorf("%w: legacy bridge columns are still present", ErrMigrationNotReady)
	}
	var canonicalColumns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'velox_project_bridges' AND column_name IN ('project_id', 'workspace_id', 'external_project_id')`).Scan(&canonicalColumns); err != nil {
		return fmt.Errorf("%w: check canonical bridge columns: %v", ErrMigrationNotReady, err)
	}
	if canonicalColumns != 3 {
		return fmt.Errorf("%w: canonical bridge columns are incomplete", ErrMigrationNotReady)
	}
	var metadataColumns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'velox_project_bridges' AND column_name IN ('editor_provider', 'editor_status', 'last_editor_sync_at')`).Scan(&metadataColumns); err != nil {
		return fmt.Errorf("%w: check editor metadata columns: %v", ErrMigrationNotReady, err)
	}
	if metadataColumns != 3 {
		return fmt.Errorf("%w: editor metadata columns are incomplete", ErrMigrationNotReady)
	}
	return nil
}

func normalizeMapping(m Mapping) (Mapping, error) {
	m.ExternalProjectID = strings.TrimSpace(m.ExternalProjectID)
	m.ProjectID = strings.TrimSpace(m.ProjectID)
	if m.ExternalProjectID == "" || m.ProjectID == "" {
		return Mapping{}, fmt.Errorf("%w: external_project_id and project_id are required", ErrInvalidMapping)
	}
	return m, nil
}

func NormalizeMappings(input []Mapping) ([]Mapping, error) {
	out := make([]Mapping, 0, len(input))
	seenExternal := map[string]struct{}{}
	seenProject := map[string]struct{}{}
	for _, raw := range input {
		m, err := normalizeMapping(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seenExternal[m.ExternalProjectID]; ok {
			return nil, fmt.Errorf("%w: duplicate external_project_id %q", ErrInvalidMapping, m.ExternalProjectID)
		}
		if _, ok := seenProject[m.ProjectID]; ok {
			return nil, fmt.Errorf("%w: duplicate project_id %q", ErrInvalidMapping, m.ProjectID)
		}
		seenExternal[m.ExternalProjectID] = struct{}{}
		seenProject[m.ProjectID] = struct{}{}
		out = append(out, m)
	}
	return out, nil
}

type sessionRow struct {
	WorkspaceID int64
	AccountID   int64
	VideoID     string
}

type projectRow struct {
	WorkspaceID int64
	Status      string
}

type bridgeRow struct {
	ProjectID         string
	WorkspaceID       int64
	ExternalProjectID string
	CreatedAt         time.Time
	MigrationRunID    sql.NullString
}

func queryOneSession(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]sessionRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT workspace_id, platform_account_id, youtube_video_id FROM youtube_video_edits WHERE velox_project_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("lookup youtube_video_edits: %w", err)
	}
	defer rows.Close()
	var out []sessionRow
	for rows.Next() {
		var s sessionRow
		if err := rows.Scan(&s.WorkspaceID, &s.AccountID, &s.VideoID); err != nil {
			return nil, fmt.Errorf("scan youtube_video_edits: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func queryProject(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]projectRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT workspace_id, status FROM thumbnail_projects WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("lookup thumbnail_projects: %w", err)
	}
	defer rows.Close()
	var out []projectRow
	for rows.Next() {
		var p projectRow
		if err := rows.Scan(&p.WorkspaceID, &p.Status); err != nil {
			return nil, fmt.Errorf("scan thumbnail_projects: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func queryExistingBridge(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, m Mapping) ([]bridgeRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT project_id, workspace_id, external_project_id, created_at, migration_run_id FROM velox_project_bridges WHERE project_id = $1 OR external_project_id = $2`, m.ProjectID, m.ExternalProjectID)
	if err != nil {
		return nil, fmt.Errorf("lookup velox_project_bridges: %w", err)
	}
	defer rows.Close()
	var out []bridgeRow
	for rows.Next() {
		var b bridgeRow
		if err := rows.Scan(&b.ProjectID, &b.WorkspaceID, &b.ExternalProjectID, &b.CreatedAt, &b.MigrationRunID); err != nil {
			return nil, fmt.Errorf("scan velox_project_bridges: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func sameBridge(b bridgeRow, workspaceID int64, m Mapping) bool {
	return b.ProjectID == m.ProjectID && b.ExternalProjectID == m.ExternalProjectID && b.WorkspaceID == workspaceID
}

func validateOne(ctx context.Context, db *sql.DB, m Mapping) (Entry, error) {
	entry := Entry{Mapping: m}
	sessions, err := queryOneSession(ctx, db, m.ExternalProjectID)
	if err != nil {
		return entry, err
	}
	if len(sessions) == 0 {
		entry.Status, entry.Reason = StatusMissing, "no youtube_video_edits row for the external project handle"
		return entry, nil
	}
	if len(sessions) != 1 {
		entry.Status, entry.Reason = StatusAmbiguous, "multiple youtube_video_edits rows for the external project handle"
		return entry, nil
	}
	s := sessions[0]
	entry.WorkspaceID = s.WorkspaceID
	projects, err := queryProject(ctx, db, m.ProjectID)
	if err != nil {
		return entry, err
	}
	if len(projects) == 0 {
		entry.Status, entry.Reason = StatusMissing, "project_id does not exist in thumbnail_projects"
		return entry, nil
	}
	if len(projects) != 1 {
		entry.Status, entry.Reason = StatusAmbiguous, "project_id resolved to multiple projects"
		return entry, nil
	}
	if projects[0].Status == "deleted" {
		entry.Status, entry.Reason = StatusMissing, "target thumbnail project is deleted"
		return entry, nil
	}
	if projects[0].WorkspaceID != s.WorkspaceID {
		entry.Status, entry.Reason = StatusConflict, "thumbnail project and Velox session belong to different workspaces"
		return entry, nil
	}
	bridges, err := queryExistingBridge(ctx, db, m)
	if err != nil {
		return entry, err
	}
	for _, b := range bridges {
		if sameBridge(b, s.WorkspaceID, m) {
			entry.Status = StatusAlreadyLinked
			return entry, nil
		}
		entry.Status, entry.Reason = StatusConflict, "project_id or external_project_id is already linked to a different project"
		return entry, nil
	}
	entry.Status = StatusMatched
	return entry, nil
}

func updateSummary(r *Report) {
	r.Summary = Summary{Total: len(r.Entries)}
	for _, e := range r.Entries {
		switch e.Status {
		case StatusMatched:
			r.Summary.Matched++
		case StatusCreated:
			r.Summary.Created++
		case StatusAlreadyLinked:
			r.Summary.AlreadyLinked++
		case StatusMissing:
			r.Summary.Missing++
		case StatusAmbiguous:
			r.Summary.Ambiguous++
		case StatusConflict:
			r.Summary.Conflicts++
		}
	}
}

func safeToApply(r *Report) bool {
	return r.Summary.Missing == 0 && r.Summary.Ambiguous == 0 && r.Summary.Conflicts == 0
}

func failedReport(r *Report, err error, aborted bool) (*Report, error) {
	r.Aborted = aborted
	r.Error = err.Error()
	r.FinishedAt = time.Now().UTC()
	return r, err
}

func Run(ctx context.Context, db *sql.DB, mappings []Mapping, opts Options) (*Report, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidMapping)
	}
	normalized, err := NormalizeMappings(mappings)
	if err != nil {
		return nil, err
	}
	r := &Report{Version: ReportVersion, RunID: strings.TrimSpace(opts.RunID), StartedAt: time.Now().UTC(), DryRun: opts.DryRun, Entries: make([]Entry, 0, len(normalized))}
	if r.RunID == "" {
		r.RunID = "bridge-migration-" + uuid.NewString()
	}
	for _, m := range normalized {
		e, err := validateOne(ctx, db, m)
		if err != nil {
			return failedReport(r, err, false)
		}
		r.Entries = append(r.Entries, e)
	}
	updateSummary(r)
	if !opts.DryRun {
		if !safeToApply(r) {
			return failedReport(r, fmt.Errorf("%w: missing=%d ambiguous=%d conflicts=%d", ErrUnsafeApply, r.Summary.Missing, r.Summary.Ambiguous, r.Summary.Conflicts), false)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return failedReport(r, fmt.Errorf("begin migration transaction: %w", err), false)
		}
		defer tx.Rollback()
		for i := range r.Entries {
			if r.Entries[i].Status != StatusMatched {
				continue
			}
			e := &r.Entries[i]
			e.CreatedAt = time.Now().UTC()
			var insertedAt time.Time
			err = tx.QueryRowContext(ctx, `INSERT INTO velox_project_bridges (project_id, workspace_id, external_project_id, editor_provider, created_at, migration_run_id) VALUES ($1, $2, $3, 'velox', $4, $5) ON CONFLICT DO NOTHING RETURNING created_at`, e.Mapping.ProjectID, e.WorkspaceID, e.Mapping.ExternalProjectID, e.CreatedAt, r.RunID).Scan(&insertedAt)
			if err == nil {
				e.CreatedAt = insertedAt
				e.Status = StatusCreated
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return failedReport(r, fmt.Errorf("insert bridge project_id=%s: %w", e.Mapping.ProjectID, err), true)
			}
			bridges, findErr := queryExistingBridge(ctx, tx, e.Mapping)
			if findErr != nil {
				return failedReport(r, findErr, true)
			}
			foundEquivalent := false
			for _, b := range bridges {
				if sameBridge(b, e.WorkspaceID, e.Mapping) {
					foundEquivalent = true
					e.Status = StatusAlreadyLinked
					break
				}
			}
			if !foundEquivalent {
				return failedReport(r, fmt.Errorf("%w: concurrent bridge conflict for project_id=%s", ErrUnsafeApply, e.Mapping.ProjectID), true)
			}
		}
		if err := tx.Commit(); err != nil {
			return failedReport(r, fmt.Errorf("commit bridge migration: %w", err), true)
		}
		r.Applied = true
		updateSummary(r)
	}
	r.FinishedAt = time.Now().UTC()
	return r, nil
}

func lockBridgeForRollback(ctx context.Context, tx *sql.Tx, e *Entry, runID string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT project_id FROM velox_project_bridges WHERE project_id = $1 AND workspace_id = $2 AND external_project_id = $3 AND created_at = $4 AND migration_run_id = $5 FOR UPDATE`, e.Mapping.ProjectID, e.WorkspaceID, e.Mapping.ExternalProjectID, e.CreatedAt, runID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func Rollback(ctx context.Context, db *sql.DB, report Report) (*Report, error) {
	if strings.TrimSpace(report.RunID) == "" {
		return nil, fmt.Errorf("%w: report run_id is required", ErrRollbackUnsafe)
	}
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", ErrRollbackUnsafe)
	}
	if !report.Applied {
		return nil, fmt.Errorf("%w: report is not an applied migration", ErrRollbackUnsafe)
	}
	out := report
	out.FinishedAt = time.Now().UTC()
	out.Applied = false
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return &out, fmt.Errorf("begin rollback transaction: %w", err)
	}
	defer tx.Rollback()
	for i := range out.Entries {
		if out.Entries[i].Status != StatusCreated {
			continue
		}
		e := &out.Entries[i]
		count, err := lockBridgeForRollback(ctx, tx, e, report.RunID)
		if err != nil {
			return &out, failedRollbackReport(&out, fmt.Errorf("rollback preflight: %w", err))
		}
		if count != 1 {
			return &out, failedRollbackReport(&out, fmt.Errorf("%w: expected exactly one unchanged bridge for project_id=%s, found %d", ErrRollbackUnsafe, e.Mapping.ProjectID, count))
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM velox_project_bridges WHERE project_id = $1 AND workspace_id = $2 AND external_project_id = $3 AND created_at = $4 AND migration_run_id = $5`, e.Mapping.ProjectID, e.WorkspaceID, e.Mapping.ExternalProjectID, e.CreatedAt, report.RunID)
		if err != nil {
			return &out, failedRollbackReport(&out, fmt.Errorf("rollback bridge project_id=%s: %w", e.Mapping.ProjectID, err))
		}
		deleted, err := result.RowsAffected()
		if err != nil || deleted != 1 {
			if err == nil {
				err = fmt.Errorf("expected one deleted bridge, found %d", deleted)
			}
			return &out, failedRollbackReport(&out, fmt.Errorf("%w: %v", ErrRollbackUnsafe, err))
		}
		e.Status = StatusMatched
	}
	if err := tx.Commit(); err != nil {
		return &out, failedRollbackReport(&out, fmt.Errorf("commit bridge rollback: %w", err))
	}
	updateSummary(&out)
	return &out, nil
}

func failedRollbackReport(r *Report, err error) error {
	r.Error = err.Error()
	r.FinishedAt = time.Now().UTC()
	return err
}

func DecodeMappings(data []byte) ([]Mapping, error) {
	var mappings []Mapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("decode mapping JSON: %w", err)
	}
	return NormalizeMappings(mappings)
}
