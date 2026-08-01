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
// Note: the deprecated `cmd/server` wrapper also calls Migrate for
// local recovery compatibility. The wrapper assumes exclusive DB access;
// production and standard development MUST use this binary as the
// one-shot migration step to avoid that race.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

func main() {
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin migration (Blocco #2.1: split from cmd/server)")

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

	if err := database.Migrate(app.DB); err != nil {
		slog.Error("migrate: database.Migrate failed", "error", err)
		os.Exit(1)
	}

	slog.Info("migrate: all migrations applied successfully")
}
