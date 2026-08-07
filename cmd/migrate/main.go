// cmd/migrate — InstaEditLogin database migrations (Blocco #2.1)
//
// Connects to the database, applies pending migrations via
// internal/database.Migrate, then exits. NO HTTP server. NO worker
// goroutines. Pure pre-deploy one-shot job.
//
// Production deploy pattern:
//  1. Run `cmd/migrate` as a one-shot job (Railway pre-deploy, k8s
//     initContainer, helm pre-install hook, etc.).
//  2. Block rollouts on its success exit code.
//  3. Then deploy `cmd/api` and `cmd/worker` pods in parallel.
//
// Fail-fast: any config / DB / migration error exits 1 with a
// descriptive log line. A successful migration log line is the
// canonical signal the deploy pipeline unblocks on.
//
// API and worker processes do not run migrations themselves; this binary
// is the only migration step in both local and production Compose flows.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

func main() {
	// Migrations are short-lived and use a small isolated pool budget.
	// This one-shot process owns the small maintenance pool budget.
	_ = os.Setenv("DB_POOL_ROLE", "maintenance")
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin migration (canonical split topology)")

	app, err := bootstrap.Wire(nil)
	if err != nil {
		// bootstrap.Wire panics-on-missing-required-env (Taglio 3.1)
		// — but config.Load + database.Connect errors are returnable.
		slog.Error("migrate: wire failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := app.DB.Close(); err != nil {
			slog.Warn("migrate: db close failed", "error", err)
		}
	}()

	if err := database.MigrateWithExpectedInstallationUUID(app.DB, app.Cfg.Database.ExpectedInstallationUUID); err != nil {
		slog.Error("migrate: database.Migrate failed", "error", err)
		os.Exit(1)
	}
	if err := database.VerifyInstallationIdentity(nil, app.DB, app.Cfg.Database.ExpectedInstallationUUID); err != nil {
		slog.Error("migrate: database identity verification failed", "error_class", "DATABASE_IDENTITY_MISMATCH")
		os.Exit(1)
	}

	slog.Info("migrate: all migrations applied successfully")
}
