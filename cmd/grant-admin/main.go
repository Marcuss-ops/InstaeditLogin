// Command grant-admin promotes a user to the P2 admin gate.
//
// Usage:
//
//	cmd/grant-admin --email <ops@instaedit.org>
//	cmd/grant-admin --email <ops@instaedit.org> --granted-by <bootstrap-operator-id>
//
// Bootstrap design (per the user's P2 ask in the D1 open question):
//   - On FIRST promotion (no admin exists yet), the operator
//     runs the CLI against their own account. grantedBy == id
//     (self-grant). This is acceptable because admin privileges are
//     scoped to the /admin/* endpoints (single-tenant operator
//     surface); no cross-tenant privilege is granted.
//   - On SUBSEQUENT promotions, the bootstrapping admin already
//     has a JWT with admin=true and runs the CLI passing
//     --granted-by <their-own-id>. The audit trail records who
//     promoted whom.
//
// Idempotent: re-running on an already-admin user is a no-op that
// RE-stamps admin_granted_at + admin_granted_by (audit contract:
// every grant records WHO promoted WHEN, even if the user was
// already admin). The user's existing JWT continues to work
// because Verify reads claims.Admin which is set at next Issue
// — to pick up the change, the operator must re-authenticate
// (their next /refresh issues a fresh token with admin=true).
//
// Migration requirement: 051_users_admin.sql MUST be applied before
// this CLI is run on a fresh database; the column is NOT NULL
// DEFAULT FALSE so pre-migration rows are safe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func main() {
	email := flag.String("email", "", "Email of the user to promote to admin (required)")
	grantedBy := flag.Int64("granted-by", 0, "Optional: id of the admin doing the promotion. Defaults to 0 (no audit record; first-time bootstrap path).")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "usage: grant-admin --email <ops@instaedit.org> [--granted-by <operator-id>]")
		os.Exit(2)
	}

	if err := run(*email, *grantedBy); err != nil {
		log.Fatalf("grant-admin: %v", err)
	}
}

func run(email string, grantedBy int64) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Database.DatabaseURL == "" && cfg.Database.DBHost == "" {
		return fmt.Errorf("DATABASE_URL (or DB_HOST) is required")
	}

	db, err := database.Connect(&cfg.Database)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer db.Close()

	repo := repository.NewUserRepository(db)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	user, err := repo.FindByEmail(email)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user with email %q not found; have they registered?", email)
	}

	if err := repo.GrantAdmin(ctx, user.ID, grantedBy); err != nil {
		return fmt.Errorf("grant admin to user_id=%d: %w", user.ID, err)
	}

	fmt.Printf("admin granted: user_id=%d email=%s granted_by=%d\n", user.ID, email, grantedBy)
	fmt.Println("note: the user must re-authenticate to pick up the admin claim on their next JWT.")
	return nil
}
