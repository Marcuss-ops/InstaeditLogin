package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database/seeds"
)

// main applies development seed data to the database configured by the
// current environment. It is intended for local development only.
func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if cfg.AppEnv == "production" {
		slog.Error("seed command refused: APP_ENV=production")
		os.Exit(1)
	}

	db, err := database.Connect(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := database.Migrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	if _, err := db.Exec(seeds.DevSQL); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to apply seed data: %v\n", err)
		os.Exit(1)
	}

	slog.Info("Seed data applied successfully")
}
