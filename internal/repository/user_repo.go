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

// FindByMetaUserID finds a user by their Meta user ID.
func (r *UserRepository) FindByMetaUserID(metaUserID string) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, meta_user_id, name, created_at, updated_at 
		 FROM users WHERE meta_user_id = $1`,
		metaUserID,
	).Scan(&user.ID, &user.Email, &user.MetaUserID, &user.Name, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find user by meta_user_id: %w", err)
	}
	return user, nil
}

// FindByID finds a user by their internal ID.
func (r *UserRepository) FindByID(id int64) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(
		`SELECT id, email, meta_user_id, name, created_at, updated_at 
		 FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.MetaUserID, &user.Name, &user.CreatedAt, &user.UpdatedAt)

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
		`INSERT INTO users (email, meta_user_id, name) 
		 VALUES ($1, $2, $3) 
		 RETURNING id, created_at, updated_at`,
		user.Email, user.MetaUserID, user.Name,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// Update updates an existing user.
func (r *UserRepository) Update(user *models.User) error {
	_, err := r.db.Exec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 
		 WHERE id = $4`,
		user.Email, user.Name, time.Now(), user.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	return nil
}

// FindInstagramAccount finds an Instagram account by its Instagram user ID.
func (r *UserRepository) FindInstagramAccount(instagramUserID string) (*models.InstagramAccount, error) {
	account := &models.InstagramAccount{}
	err := r.db.QueryRow(
		`SELECT id, user_id, instagram_user_id, username, created_at, updated_at 
		 FROM instagram_accounts WHERE instagram_user_id = $1`,
		instagramUserID,
	).Scan(&account.ID, &account.UserID, &account.InstagramUserID, &account.Username, &account.CreatedAt, &account.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find instagram account: %w", err)
	}
	return account, nil
}

// CreateInstagramAccount inserts a new Instagram account.
func (r *UserRepository) CreateInstagramAccount(account *models.InstagramAccount) error {
	err := r.db.QueryRow(
		`INSERT INTO instagram_accounts (user_id, instagram_user_id, username) 
		 VALUES ($1, $2, $3) 
		 RETURNING id, created_at, updated_at`,
		account.UserID, account.InstagramUserID, account.Username,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create instagram account: %w", err)
	}
	return nil
}

// ListInstagramAccountsByUser returns all Instagram accounts for a user.
func (r *UserRepository) ListInstagramAccountsByUser(userID int64) ([]*models.InstagramAccount, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, instagram_user_id, username, created_at, updated_at
		 FROM instagram_accounts WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list instagram accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*models.InstagramAccount
	for rows.Next() {
		a := &models.InstagramAccount{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.InstagramUserID, &a.Username, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan instagram account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}
