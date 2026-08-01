package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UserRepository handles CRUD operations for users.
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new UserRepository.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// FindUserIDByEmail (P2 — admin CSV import) resolves an email to the
// underlying user_id (FK on platform_accounts). The admin /channels
// /import-csv endpoint uses this to honour the owner_email form field;
// the CLI (scripts/import_channels_csv.go) reuses the same method via
// a *repository.UserRepository wrapper.
//
// Returns ErrUserNotFound when the email is unknown (consistent with
// the rest of the package's "wrap with id" convention; callers do
// errors.Is(err, repository.ErrUserNotFound)). ctx is honoured for
// cancellation/deadline propagation under import load.
func (r *UserRepository) FindUserIDByEmail(ctx context.Context, email string) (int64, error) {
	if email == "" {
		return 0, fmt.Errorf("find user id by email: empty email")
	}
	var id int64
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = $1`,
		email,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: email=%q", ErrUserNotFound, email)
	}
	if err != nil {
		return 0, fmt.Errorf("find user id by email: %w", err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("find user id by email: zero id for %q", email)
	}
	return id, nil
}

func (r *UserRepository) FindByEmail(email string) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       is_admin, admin_granted_at, admin_granted_by,
	       created_at, updated_at FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.EmailVerified,
		&user.IsAdmin, &user.AdminGrantedAt, &user.AdminGrantedBy,
		&user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find user by email: %w", err)
	}
	return user, nil
}

// FindByID finds a user by their internal ID.
func (r *UserRepository) FindByID(id int64) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       is_admin, admin_granted_at, admin_granted_by,
	       created_at, updated_at FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.EmailVerified,
		&user.IsAdmin, &user.AdminGrantedAt, &user.AdminGrantedBy,
		&user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find user by id: %w", err)
	}
	return user, nil
}

// GrantAdmin (P2 — ops dashboard) atomically promotes a user to
// admin. Idempotent: re-calling on an already-admin user is a no-op
// that RE-stamps the granted_at + granted_by fields (audit-trail
// contract: every grant records WHO promoted WHEN, even if the
// user was already admin). Returns ErrUserNotFound when id is
// unknown.
//
// Bootstrap: cmd/grant-admin --email calls FindByEmail then this
// method (grantedBy is the bootstrapping operator's id, or self
// for the very first promotion).
func (r *UserRepository) GrantAdmin(ctx context.Context, id, grantedBy int64) error {
	if id <= 0 {
		return fmt.Errorf("grant admin: invalid target id %d", id)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE users
		 SET is_admin         = TRUE,
		     admin_granted_at = NOW(),
		     admin_granted_by = $2,
		     updated_at       = NOW()
		 WHERE id = $1`,
		id, grantedBy,
	)
	if err != nil {
		return fmt.Errorf("grant admin: update users: %w", err)
	}
	// UPDATE without a row returns RowsAffected=0; we treat unknown
	// id as a soft error here (callers want to know the id was
	// wrong, not silent). Wrap ErrUserNotFound the same way as
	// MarkReauthRequired.
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = $1`, id).Scan(&n); err != nil {
		return fmt.Errorf("grant admin: verify row exists: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, id)
	}
	return nil
}

// Create inserts a new user into the database.
func (r *UserRepository) Create(user *models.User) error {
	err := r.db.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id, created_at, updated_at`,
		user.Email, user.Name,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// Update updates an existing user. Returns ErrUserNotFound (wrapped with
// id context) when the user id does not match any row — the API layer
// can map this sentinel to 404 via errors.Is.
//
// NOTE: UserRepository.Update is NOT tenant-scoped (no workspace_id
// clause), unlike PostRepository.Update. Zero rows is unambiguous: the
// user is gone. No ErrUserUnauthorized variant exists for this layer.
func (r *UserRepository) Update(user *models.User) error {
	result, err := r.db.Exec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`,
		user.Email, user.Name, time.Now(), user.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, user.ID)
	}
	return nil
}

// CreateSaaSUser inserts a new user with an email, name, and bcrypt password hash.
// Used for email/password registration; OAuth users continue to use Create().
func (r *UserRepository) CreateSaaSUser(email, name string, passwordHash []byte) (*models.User, error) {
	user := &models.User{
		Email:        email,
		Name:         name,
		PasswordHash: passwordHash,
	}
	err := r.db.QueryRow(
		`INSERT INTO users (email, name, password_hash)
		 VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
		email, name, passwordHash,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create SaaS user: %w", err)
	}
	return user, nil
}

// SetEmailVerified marks a user's email as verified.
func (r *UserRepository) SetEmailVerified(userID int64) error {
	result, err := r.db.Exec(
		`UPDATE users SET email_verified = TRUE, updated_at = $1 WHERE id = $2`,
		time.Now(), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to verify email: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, userID)
	}
	return nil
}

// UpdatePassword sets a new bcrypt password hash for a user.
func (r *UserRepository) UpdatePassword(userID int64, passwordHash []byte) error {
	result, err := r.db.Exec(
		`UPDATE users SET password_hash = $1, updated_at = $2 WHERE id = $3`,
		passwordHash, time.Now(), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, userID)
	}
	return nil
}
