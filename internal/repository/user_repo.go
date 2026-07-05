package repository

import (
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

// FindByEmail finds a user by their email address.
func (r *UserRepository) FindByEmail(email string) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, name, created_at, updated_at FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.UpdatedAt)

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
		`SELECT id, email, name, created_at, updated_at FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.UpdatedAt)

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

// Update updates an existing user.
func (r *UserRepository) Update(user *models.User) error {
	_, err := r.db.Exec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`,
		user.Email, user.Name, time.Now(), user.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	return nil
}

// FindPlatformAccount finds a platform account by platform and platform user ID.
func (r *UserRepository) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	account := &models.PlatformAccount{}
	err := r.db.QueryRow(
		`SELECT id, user_id, platform, platform_user_id, username, created_at, updated_at
		 FROM platform_accounts WHERE platform = $1 AND platform_user_id = $2`,
		platform, platformUserID,
	).Scan(&account.ID, &account.UserID, &account.Platform, &account.PlatformUserID,
		&account.Username, &account.CreatedAt, &account.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find platform account: %w", err)
	}
	return account, nil
}

// CreatePlatformAccount inserts a new platform account.
func (r *UserRepository) CreatePlatformAccount(account *models.PlatformAccount) error {
	err := r.db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username)
		 VALUES ($1, $2, $3, $4) RETURNING id, created_at, updated_at`,
		account.UserID, account.Platform, account.PlatformUserID, account.Username,
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
			`SELECT id, user_id, platform, platform_user_id, username, created_at, updated_at
			 FROM platform_accounts WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	} else {
		rows, err = r.db.Query(
			`SELECT id, user_id, platform, platform_user_id, username, created_at, updated_at
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
		if err := rows.Scan(&a.ID, &a.UserID, &a.Platform, &a.PlatformUserID, &a.Username, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan platform account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// FindOrCreateUserByPlatform finds an existing user linked to the given platform profile,
// or creates a new user with a linked platform account if none exists.
func (r *UserRepository) FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
	// Try to find existing platform account
	existing, err := r.FindPlatformAccount(platform, profile.PlatformUserID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find platform account: %w", err)
	}

	if existing != nil {
		// Update username if changed
		user, err := r.FindByID(existing.UserID)
		if err != nil {
			return nil, nil, err
		}
		// Update user info
		if profile.Name != "" || profile.Email != "" {
			user.Name = coalesceStr(profile.Name, user.Name)
			user.Email = coalesceStr(profile.Email, user.Email)
			_ = r.Update(user)
		}
		// Update platform account username
		if profile.Username != "" && profile.Username != existing.Username {
			existing.Username = profile.Username
			_, _ = r.db.Exec(
				`UPDATE platform_accounts SET username = $1, updated_at = $2 WHERE id = $3`,
				profile.Username, time.Now(), existing.ID,
			)
		}
		return user, existing, nil
	}

	// Create new user and platform account in a transaction
	tx, err := r.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	user := &models.User{
		Email: profile.Email,
		Name:  profile.Name,
	}
	err = tx.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id, created_at, updated_at`,
		user.Email, user.Name,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create user: %w", err)
	}

	account := &models.PlatformAccount{
		UserID:         user.ID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
	}
	err = tx.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username)
		 VALUES ($1, $2, $3, $4) RETURNING id, created_at, updated_at`,
		account.UserID, account.Platform, account.PlatformUserID, account.Username,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create platform account: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return user, account, nil
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
