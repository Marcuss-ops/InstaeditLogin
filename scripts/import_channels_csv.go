//go:build csvimport

// Package main — offline CLI for the P2 admin onboarding flow.
// Mirrors scripts/set_password.go in spirit (a small, idempotent
// env-driven DB writer that ops can run from a bastion host with
// only a DATABASE_URL and a fully-vetted export in hand).
//
// Build tag `csvimport` (see the `//go:build` directive above)
// keeps this CLI out of the default `go build ./...` pass so it
// doesn't collide with scripts/set_password.go (also `package main`
// in the same folder). Operator invocation:
//
//	go run -tags=csvimport ./scripts/import_channels_csv.go \
//	    --file channels.csv --owner-email operator@example.com
//
// Or pin the binary: `go build -tags=csvimport -o bin/import-channels
// ./scripts/import_channels_csv.go`.
//
// The CSV columns (header row, in any order) are:
//
//	channel_id,channel_name,manager_email_hint,
//	workspace,group,language,timezone,expected_upload_frequency
//
// Each row lands as a fresh platform_accounts row at
// status='pending_authorization' (UPSERT on (platform,
// platform_user_id) — re-running a CSV is safe). NO tokens are
// ever written here; the OAuth callback is the only path that
// creates the cipher row in credentials.vault.
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// runConfig is the parsed CLI shape.
type runConfig struct {
	FilePath    string
	OwnerEmail  string
	OwnerUserID int64
	DatabaseURL string
	Platform    string
	Verbose     bool
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := parseFlags(log)
	if err != nil {
		log.Error("flag parse failed", "err", err.Error())
		os.Exit(2)
	}

	// Capture the workspace names referenced in the CSV BEFORE we
	// open the parser, so the per-row workspaceLookup callback below
	// hits an in-memory map (no N+1 QueryRowContext per row).
	csvFile, err := os.Open(cfg.FilePath)
	if err != nil {
		log.Error("open csv for scan", "err", err.Error())
		os.Exit(1)
	}
	wanted, err := scanWorkspaceNames(csvFile)
	_ = csvFile.Close()
	if err != nil {
		log.Error("scan workspace names", "err", err.Error())
		os.Exit(1)
	}

	log.Info("import_channels_csv: starting",
		"file", cfg.FilePath,
		"platform", cfg.Platform,
		"distinct_workspaces", len(wanted),
		"owner_email_provided", cfg.OwnerEmail != "",
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runImport(ctx, log, cfg, wanted); err != nil {
		log.Error("import_channels_csv: failed", "err", err.Error())
		os.Exit(1)
	}
	log.Info("import_channels_csv: done")
}

func parseFlags(_ *slog.Logger) (runConfig, error) {
	cfg := runConfig{Platform: "youtube"}
	flag.StringVar(&cfg.FilePath, "file", "", "Path to the channels CSV (REQUIRED)")
	flag.StringVar(&cfg.OwnerEmail, "owner-email", "", "Email of the user stamped on every inserted row's user_id FK")
	flag.Int64Var(&cfg.OwnerUserID, "owner-id", 0, "Optional. Numeric user_id to bypass --owner-email resolution")
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN (defaults to $DATABASE_URL)")
	flag.StringVar(&cfg.Platform, "platform", cfg.Platform, "Platform label (default: youtube)")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Verbose (per-row) logging")
	flag.Parse()
	if cfg.FilePath == "" {
		return cfg, errors.New("required: --file <path-to-csv>")
	}
	if cfg.DatabaseURL == "" {
		return cfg, errors.New("required: --database-url <dsn> OR set $DATABASE_URL")
	}
	if cfg.OwnerUserID == 0 && cfg.OwnerEmail == "" {
		return cfg, errors.New("required: either --owner-id <int> or --owner-email <addr>")
	}
	if cfg.Platform == "" {
		return cfg, errors.New("--platform may not be empty (default is youtube)")
	}
	return cfg, nil
}

// scanWorkspaceNames re-reads the CSV header + data rows and
// returns the distinct workspace values (dedupe via map key).
// Used to pre-fetch the workspace-id lookup map with a SINGLE bulk
// SELECT instead of N QueryRowContext round trips per row
// (500-channel CSVs = hundreds of round trips otherwise).
func scanWorkspaceNames(r io.Reader) (map[string]struct{}, error) {
	csvr := csv.NewReader(r)
	csvr.FieldsPerRecord = -1
	records, err := csvr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("scan workspace names: csv read: %w", err)
	}
	if len(records) < 2 {
		return map[string]struct{}{}, nil // header only — no rows
	}
	workspaceIdx := -1
	for i, h := range records[0] {
		if strings.TrimSpace(strings.ToLower(h)) == "workspace" {
			workspaceIdx = i
			break
		}
	}
	if workspaceIdx < 0 {
		return nil, errors.New("scan workspace names: column 'workspace' missing from header")
	}
	wanted := make(map[string]struct{}, 8)
	for _, row := range records[1:] {
		if workspaceIdx >= len(row) {
			continue
		}
		v := strings.TrimSpace(row[workspaceIdx])
		if v == "" {
			continue
		}
		wanted[v] = struct{}{}
	}
	return wanted, nil
}

// openDB wraps the production connect path with a short context so
// a stuck dial doesn't hang the operator on a flaky proxy.
func openDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// resolveOwner turns the (--owner-email, --owner-id) flag pair into
// a positive int64 user_id. --owner-id wins when supplied.
func resolveOwner(ctx context.Context, db *sql.DB, cfg runConfig) (int64, error) {
	if cfg.OwnerUserID > 0 {
		return cfg.OwnerUserID, nil
	}
	userRepo := repository.NewUserRepository(db)
	id, err := userRepo.FindUserIDByEmail(ctx, cfg.OwnerEmail)
	if err != nil {
		return 0, fmt.Errorf("resolve owner_email=%q: %w", cfg.OwnerEmail, err)
	}
	return id, nil
}

// bulkWorkspaceLookup pre-fetches all (id, name) rows for the
// workspaces the CSV reader referenced, then returns the callback
// Parse expects. ONE QueryContext replaces the per-row N+1 path.
func bulkWorkspaceLookup(ctx context.Context, db *sql.DB, wanted map[string]struct{}) (func(name string) (int64, bool), error) {
	if len(wanted) == 0 {
		// Empty CSV (header-only): no workspaces to look up; return
		// a callback that always rejects so any row produces a
		// skip + operator-readable "no such workspace" Reason.
		return func(name string) (int64, bool) { return 0, false }, nil
	}
	names := make([]string, 0, len(wanted))
	for n := range wanted {
		names = append(names, n)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, name FROM workspaces WHERE name = ANY($1)`,
		pqStringArray(names),
	)
	if err != nil {
		return nil, fmt.Errorf("bulk workspace lookup: %w", err)
	}
	defer rows.Close()
	lookup := make(map[string]int64, len(names))
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("bulk workspace scan: %w", err)
		}
		lookup[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk workspace iterate: %w", err)
	}
	return func(name string) (int64, bool) {
		id, ok := lookup[name]
		return id, ok
	}, nil
}

// pqStringArray formats a []string as a Postgres array literal
// "{a,b,c}" for ANY($1). Quoting each element via %q keeps the
// lookup injection-safe (a comma+quote in a CSV cell cannot break
// out of its quoted element).
func pqStringArray(elems []string) interface{} {
	if len(elems) == 0 {
		return "{}"
	}
	return elems // pgx encodes []string as a TEXT[] natively
}

// runImport is the main driver. The parse + DB-write contract is
// deliberately identical to the HTTP handler path so behavioural
// regressions surface in exactly one place.
func runImport(ctx context.Context, log *slog.Logger, cfg runConfig, wanted map[string]struct{}) error {
	db, err := openDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ownerID, err := resolveOwner(ctx, db, cfg)
	if err != nil {
		return err
	}
	log.Info("resolved owner", "owner_user_id", ownerID)

	// Second pass: open the file for the parser + supply the
	// workspace lookup map from the prev scan pass.
	file, err := os.Open(cfg.FilePath)
	if err != nil {
		return fmt.Errorf("open csv: %w", err)
	}
	defer file.Close()

	lookup, err := bulkWorkspaceLookup(ctx, db, wanted)
	if err != nil {
		return fmt.Errorf("workspace lookup: %w", err)
	}

	rows, parseErrs, err := channelimport.Parse(file, cfg.Platform, lookup)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if cfg.Verbose {
		log.Info("csv parsed",
			"valid_rows", len(rows),
			"parse_errors", len(parseErrs),
		)
	}

	res, err := channelimport.ImportToDB(ctx, db, ownerID, rows)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// Stitch parse-level + DB-level skips into one envelope.
	allErrors := res.Errors
	for _, pe := range parseErrs {
		allErrors = append(allErrors, pe)
	}

	out := struct {
		File           string                  `json:"file"`
		OwnerUserID    int64                   `json:"owner_user_id"`
		Platform       string                  `json:"platform"`
		Imported       int                     `json:"imported"`
		SkippedTotal   int                     `json:"skipped"`
		SkippedEntries []channelimport.RowError `json:"skipped_entries,omitempty"`
	}{
		File:           cfg.FilePath,
		OwnerUserID:    ownerID,
		Platform:       cfg.Platform,
		Imported:       res.Imported,
		SkippedTotal:   res.Skipped + len(parseErrs),
		SkippedEntries: allErrors,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	return nil
}
