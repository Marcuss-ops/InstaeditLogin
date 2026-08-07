// Command migrate-velox-bridges performs the one-time, relationship-only
// migration from real Velox project handles to existing InstaEdit projects.
// It never copies or modifies editor-native data.
//
// Safety: dry-run is the default. --apply is required for writes. A mapping
// JSON file is mandatory and must contain explicit external_project_id/project_id
// pairs. --rollback-report removes only bridges created by that exact applied
// report, and only when their full context is unchanged.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxmigration"
)

func main() {
	mappingPath := flag.String("mapping", "", "JSON file with explicit external_project_id -> project_id mappings")
	reportPath := flag.String("report", "", "write JSON report to this path (stdout when omitted)")
	apply := flag.Bool("apply", false, "write bridge rows; without this flag the command is a dry-run")
	rollbackPath := flag.String("rollback-report", "", "rollback an applied migration report instead of running a migration")
	flag.Parse()

	if *rollbackPath != "" && (*mappingPath != "" || *apply) {
		fatal("--rollback-report cannot be combined with --mapping or --apply")
	}
	if *rollbackPath == "" && *mappingPath == "" {
		fatal("--mapping is required (use --rollback-report for rollback)")
	}
	if os.Getenv("DATABASE_URL") == "" {
		fatal("DATABASE_URL is required; no database operation was attempted")
	}

	cfg := &config.DatabaseConfig{DatabaseURL: os.Getenv("DATABASE_URL"), DBPoolRole: config.DBPoolRoleMaintenance, ExpectedInstallationUUID: os.Getenv("EXPECTED_DATABASE_INSTALLATION_UUID")}
	db, err := database.Connect(cfg)
	if err != nil {
		fatal("connect database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := database.VerifyInstallationIdentity(ctx, db, cfg.ExpectedInstallationUUID); err != nil {
		fatal("database identity verification failed: %v", err)
	}
	if err := veloxmigration.VerifyMigrationReady(ctx, db); err != nil {
		fatal("migration schema preflight failed: %v", err)
	}

	if *rollbackPath != "" {
		report := readReport(*rollbackPath)
		rolled, err := veloxmigration.Rollback(ctx, db, report)
		if err != nil {
			writeReportOrFatal(*reportPath, rolled, err)
		}
		writeReportOrFatal(*reportPath, rolled, nil)
		return
	}
	data, err := os.ReadFile(*mappingPath)
	if err != nil {
		fatal("read mapping: %v", err)
	}
	mappings, err := veloxmigration.DecodeMappings(data)
	if err != nil {
		fatal("mapping validation: %v", err)
	}
	report, runErr := veloxmigration.Run(ctx, db, mappings, veloxmigration.Options{DryRun: !*apply})
	writeReportOrFatal(*reportPath, report, runErr)
	if runErr != nil {
		os.Exit(1)
	}
}

func readReport(path string) veloxmigration.Report {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal("read rollback report: %v", err)
	}
	var report veloxmigration.Report
	if err := json.Unmarshal(data, &report); err != nil {
		fatal("decode rollback report: %v", err)
	}
	return report
}

func writeReportOrFatal(path string, report *veloxmigration.Report, runErr error) {
	if report == nil {
		fatal("migration failed: %v", runErr)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal("encode report: %v", err)
	}
	data = append(data, '\n')
	if path == "" {
		_, _ = os.Stdout.Write(data)
	} else if err := os.WriteFile(path, data, 0o600); err != nil {
		fatal("write report: %v", err)
	}
	if runErr != nil {
		fatal("migration refused: %v", runErr)
	}
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
