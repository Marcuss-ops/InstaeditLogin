package repository

import (
	"context"
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

// FinalizeAttach (P2 — admin connect-link) creates (or reuses) the
// oauth_connections row that anchors the platform_account ↔ encrypted
// token storage relationship, and promotes the platform_account from
// 'pending_authorization' (the CSV-import reset state) to 'active'
// with a fresh connected_at. Called by the OAuth callback AFTER a
// successful AttachPlatformAccount + vault.Save wire-up so the
// flow order is:
//
//	1. AttachPlatformAccount (creates platform_accounts row, NULL
//	   oauth_connection_id, status='pending_authorization')
//	2. FinalizeAttach (UPSERT oauth_connections; UPDATE
//	   platform_accounts.oauth_connection_id + status +
//	   connected_at; in one tx so a partial failure can't leave
//	   the FK dangling)
//	3. vault.Save (FK oauth_connection_id is now set in
//	   platform_accounts so the FK from tokens → oauth_connections
//	   resolves)
//
// Idempotent on (user_id, provider, provider_resource_id) via ON
// CONFLICT DO UPDATE so a re-authorize for the same channel flips
// status back to 'active' + refreshes connected_at + scopes
// without losing the existing oauth_connection row.
//
// Returns the oauth_connection_id used so the caller can verify
// what was stamped onto platform_accounts.
func (r *UserRepository) FinalizeAttach(ctx context.Context, accountID int64, scopes []string) (int64, error) {
	if accountID <= 0 {
		return 0, fmt.Errorf("finalize attach: accountID must be > 0 (got %d)", accountID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("finalize attach: begin tx: %w", err)
	}
	defer func() {
		// Rollback is a no-op after a successful Commit; this is
		// the standard pgx/database/sql idiom.
		_, _ = tx.ExecContext(ctx, "SELECT 1")
		_ = tx.Rollback()
	}()

	var (
		platform          string
		providerResourceID string
		userID            int64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT platform, platform_user_id, user_id FROM platform_accounts WHERE id = $1`,
		accountID,
	).Scan(&platform, &providerResourceID, &userID); err != nil {
		return 0, fmt.Errorf("finalize attach: load account %d: %w", accountID, err)
	}
	if userID <= 0 {
		return 0, fmt.Errorf("finalize attach: platform_accounts.user_id is zero for account %d", accountID)
	}

	// UPSERT oauth_connections. The unique key (user_id, provider,
	// provider_resource_id) makes this idempotent across rechannels
	// of the same grant (e.g. if a manager reconsents after a
	// token rotation). pgx v5 stdlib binds Go []string → TEXT[]
	// natively through its default type map; textual literal
	// formatting is NOT needed.
	var oauthConnID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, provider, provider_resource_id)
		 DO UPDATE SET scopes = EXCLUDED.scopes, last_validated_at = NOW(), updated_at = NOW()
		 RETURNING id`,
		userID, platform, providerResourceID, scopes,
	).Scan(&oauthConnID); err != nil {
		return 0, fmt.Errorf("finalize attach: upsert oauth_connections: %w", err)
	}

	// Promote the platform_account: link the FK, status='active',
	// connected_at=NOW(). connected_at is what the dashboard's
	// "last successful auth" freshness field reads from.
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		     SET oauth_connection_id = $1,
		         status = 'active',
		         connected_at = NOW(),
		         last_validated_at = NOW(),
		         reauth_required_at = NULL,
		         last_error_code = NULL,
		         last_error_message = NULL,
		         updated_at = NOW()
		   WHERE id = $2`,
		oauthConnID, accountID,
	); err != nil {
		return 0, fmt.Errorf("finalize attach: update platform_accounts %d: %w", accountID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("finalize attach: commit: %w", err)
	}
	return oauthConnID, nil
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

// MarkReauthRequired (P0#3 server-side YouTube channel binding check,
// publish_worker.go) atomically flips a platform_account's lifecycle
// flags to "the grant is structurally unfit, prompt the operator to
// reconnect before the next publish attempt". The flag write is a
// single UPDATE so two concurrent calls (e.g. two worker replicas
// reacting to the same denied upload) cannot drift out of sync.
//
// Behaviour:
//   - status -> 'reauth_required'
//   - reauth_required_at -> NOW()
//   - last_error_code -> code (short, programmatic; e.g.
//     "youtube_channel_mismatch")
//   - last_error_message -> message (human-readable; e.g.
//     "expected UCabc..., grant bound to [UCxyz...]")
//   - updated_at -> NOW()
//
// Idempotent: re-calling refreshes timestamps. The publish worker
// treats the returned error as a soft failure (logs at WARN) and
// still proceeds to mark the post_target 'failed' so the user sees
// a structured error message in the dashboard.
//
// Returns ErrUserNotFound when the id does not match any row,
// wrapped with the id for caller diagnostics.
func (r *UserRepository) MarkReauthRequired(ctx context.Context, id int64, code, message string) error {
	if id <= 0 {
		return fmt.Errorf("mark reauth required: invalid id %d (must be > 0)", id)
	}
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE platform_accounts
		 SET status = 'reauth_required',
		     reauth_required_at = $1,
		     last_error_code = $2,
		     last_error_message = $3,
		     updated_at = $4
		 WHERE id = $5`,
		now, code, message, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark reauth required: update platform_accounts: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark reauth required: read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, id)
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
