package repository

import (
	"database/sql"
	"encoding/json"
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

// FindByEmail finds a user by their email address.
func (r *UserRepository) FindByEmail(email string) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
		       created_at, updated_at FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.EmailVerified,
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
		       created_at, updated_at FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.EmailVerified,
		&user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find user by id: %w", err)
	}
	return user, nil
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

// FindPlatformAccount finds a platform account by platform and platform user ID.
func (r *UserRepository) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	account := &models.PlatformAccount{}
	var metadata []byte
	err := r.db.QueryRow(
		`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
		        last_validated_at, last_refresh_at, reauth_required_at,
		        COALESCE(last_error_code, '') AS last_error_code,
		        COALESCE(last_error_message, '') AS last_error_message,
		        metadata, created_at, updated_at
		 FROM platform_accounts WHERE platform = $1 AND platform_user_id = $2`,
		platform, platformUserID,
	).Scan(&account.ID, &account.UserID, &account.Platform, &account.PlatformUserID,
		&account.Username, &account.Status, &account.ConnectedAt, &account.LastValidatedAt,
		&account.LastRefreshAt, &account.ReauthRequiredAt, &account.LastErrorCode,
		&account.LastErrorMessage, &metadata, &account.CreatedAt, &account.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find platform account: %w", err)
	}
	account.Metadata = scanMetadata(metadata)
	return account, nil
}

// CreatePlatformAccount inserts a new platform account.
func (r *UserRepository) CreatePlatformAccount(account *models.PlatformAccount) error {
	if account.Status == "" {
		account.Status = models.AccountStatusActive
	}
	now := time.Now()
	account.ConnectedAt = &now
	err := r.db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, status, connected_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at, updated_at`,
		account.UserID, account.Platform, account.PlatformUserID, account.Username, account.Status, account.ConnectedAt,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create platform account: %w", err)
	}
	return nil
}

// ListPlatformAccountsByUser returns all platform accounts for a user, optionally filtered by platform.
func (r *UserRepository) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	var rows *sql.Rows
	var err error

	if platform == "" {
		rows, err = r.db.Query(
			`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
			        last_validated_at, last_refresh_at, reauth_required_at,
			        COALESCE(last_error_code, '') AS last_error_code,
			        COALESCE(last_error_message, '') AS last_error_message,
			        metadata, created_at, updated_at
			 FROM platform_accounts WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	} else {
		rows, err = r.db.Query(
			`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
			        last_validated_at, last_refresh_at, reauth_required_at,
			        COALESCE(last_error_code, '') AS last_error_code,
			        COALESCE(last_error_message, '') AS last_error_message,
			        metadata, created_at, updated_at
			 FROM platform_accounts WHERE user_id = $1 AND platform = $2 ORDER BY created_at DESC`,
			userID, platform)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list platform accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*models.PlatformAccount
	for rows.Next() {
		a := &models.PlatformAccount{}
		var metadata []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.Platform, &a.PlatformUserID, &a.Username, &a.Status, &a.ConnectedAt,
			&a.LastValidatedAt, &a.LastRefreshAt, &a.ReauthRequiredAt, &a.LastErrorCode,
			&a.LastErrorMessage, &metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan platform account: %w", err)
		}
		a.Metadata = scanMetadata(metadata)
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// FindPlatformAccountByID fetches a platform account by internal id, or
// (nil, nil) when no row matches. (nil, nil) matches the rest of the
// repository layer's not-found convention so callers can write
//
//	if pa == nil { /* skip — row vanished */ }
//
// without inspecting sql.ErrNoRows.
//
// Used by background workers (publish worker) that need to look up an
// account knowing only its id, typically from a post_targets join row.
func (r *UserRepository) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	account := &models.PlatformAccount{}
	var metadata []byte
	err := r.db.QueryRow(
		`SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
		        last_validated_at, last_refresh_at, reauth_required_at,
		        COALESCE(last_error_code, '') AS last_error_code,
		        COALESCE(last_error_message, '') AS last_error_message,
		        metadata, created_at, updated_at
		 FROM platform_accounts
		 WHERE id = $1`, id,
	).Scan(&account.ID, &account.UserID, &account.Platform, &account.PlatformUserID,
		&account.Username, &account.Status, &account.ConnectedAt, &account.LastValidatedAt,
		&account.LastRefreshAt, &account.ReauthRequiredAt, &account.LastErrorCode,
		&account.LastErrorMessage, &metadata, &account.CreatedAt, &account.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find platform account by id: %w", err)
	}
	account.Metadata = scanMetadata(metadata)
	return account, nil
}

// AttachPlatformAccount links an OAuth platform profile to an EXISTING
// user identified by userID. It does NOT create users — SPRINT 7.1
// (P0#14) closed the OAuth-auto-create gap; users are created via the
// product onboarding flow (email/password register) before they
// can ever hit /api/v1/auth/{provider}/callback.
//
// Behaviour:
//   - If (platform, platform_user_id) does not exist → INSERT a new
//     platform_accounts row bound to userID. Returns the new row.
//   - If (platform, platform_user_id) exists AND existing.UserID == userID
//     → idempotent: update the username in place (provider-side renames
//     do happen) and return the existing row.
//   - If (platform, platform_user_id) exists AND existing.UserID != userID
//     → return ErrAccountAlreadyLinked. We never silently rebind a
//     platform identity to a different session user; that's an
//     account-takeover vector. The operator's runbook is for the
//     human owner of the existing link to disconnect via
//     DELETE /api/v1/accounts/{id} before re-link is possible.
//
// userID > 0 is enforced (SPRINT 2.1 + Taglio 1.1): a zero user id
// means the caller hijacked a sessionless request, which is the
// exact scenario this method is designed to refuse.
func (r *UserRepository) AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("attach platform account: invalid user id %d (must be > 0)", userID)
	}
	if profile == nil {
		return nil, fmt.Errorf("attach platform account: nil profile")
	}
	if profile.PlatformUserID == "" {
		return nil, fmt.Errorf("attach platform account: empty platform_user_id")
	}
	if platform == "" {
		return nil, fmt.Errorf("attach platform account: empty platform")
	}

	existing, err := r.FindPlatformAccount(platform, profile.PlatformUserID)
	if err != nil {
		return nil, fmt.Errorf("attach platform account: lookup: %w", err)
	}
	if existing != nil {
		if existing.UserID != userID {
			// 409 surface echoes this message verbatim — keep it minimal:
			// do NOT embed profile.PlatformUserID (provider-scoped stable
			// id that the requester already knows) or any PII that would
			// otherwise leak to a stranger's logs.
			return nil, fmt.Errorf("%w: platform=%s owned_by=%d requested_by=%d",
				ErrAccountAlreadyLinked, platform, existing.UserID, userID)
		}
		// Same user — idempotent re-link. Refresh username if the
		// provider says it's changed.
		if profile.Username != "" && profile.Username != existing.Username {
			if _, err := r.db.Exec(
				`UPDATE platform_accounts SET username = $1, updated_at = $2 WHERE id = $3`,
				profile.Username, time.Now(), existing.ID,
			); err != nil {
				return nil, fmt.Errorf("attach platform account: update username: %w", err)
			}
			existing.Username = profile.Username
		}
		return existing, nil
	}

	// No prior link — create bound to the authenticated user.
	account := &models.PlatformAccount{
		UserID:         userID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
		Status:         models.AccountStatusActive,
	}
	now := time.Now()
	account.ConnectedAt = &now
	if err := r.CreatePlatformAccount(account); err != nil {
		return nil, fmt.Errorf("attach platform account: create: %w", err)
	}
	return account, nil
}

// coalesceStr returns the first non-empty string.
func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// scanMetadata unmarshals a JSONB byte slice into a Metadata map.
func scanMetadata(data []byte) models.Metadata {
	if len(data) == 0 {
		return models.Metadata{}
	}
	var m models.Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return models.Metadata{}
	}
	return m
}

// UpdatePlatformAccount persists lifecycle changes to a platform account.
func (r *UserRepository) UpdatePlatformAccount(account *models.PlatformAccount) error {
	metadataJSON, err := json.Marshal(account.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	result, err := r.db.Exec(
		`UPDATE platform_accounts
		 SET status = $1, connected_at = $2, last_validated_at = $3, last_refresh_at = $4,
		     reauth_required_at = $5, last_error_code = $6, last_error_message = $7,
		     metadata = $8, updated_at = $9
		 WHERE id = $10`,
		account.Status, account.ConnectedAt, account.LastValidatedAt, account.LastRefreshAt,
		account.ReauthRequiredAt, account.LastErrorCode, account.LastErrorMessage,
		metadataJSON, time.Now(), account.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update platform account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, account.ID)
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

// DeletePlatformAccount removes a platform account and its tokens (cascading).
func (r *UserRepository) DeletePlatformAccount(id int64) error {
	result, err := r.db.Exec(`DELETE FROM platform_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete platform account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, id)
	}
	return nil
}
