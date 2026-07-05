package models

import "time"

// User represents an authenticated Meta user.
type User struct {
	ID         int64     `json:"id"`
	Email      string    `json:"email,omitempty"`
	MetaUserID string    `json:"meta_user_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// InstagramAccount represents an Instagram Business/Creator account
// linked to a Meta user.
type InstagramAccount struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	InstagramUserID string    `json:"instagram_user_id"`
	Username        string    `json:"username"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Token represents an encrypted OAuth token stored in the database.
// The token itself is AES-256 encrypted at rest.
type Token struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	AccountID      *int64    `json:"account_id,omitempty"`
	TokenType      string    `json:"token_type"`
	EncryptedToken []byte    `json:"-"` // Never expose in JSON
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Scopes         []string  `json:"scopes"`
	CreatedAt      time.Time `json:"created_at"`
}

// Token types
const (
	TokenTypeShortLived  = "short_lived"
	TokenTypeLongLived   = "long_lived"
	TokenTypePageAccess  = "page_access"
	TokenTypeInstagram   = "instagram"
)

// OAuthToken represents a decrypted token ready for API use.
type OAuthToken struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Scopes      []string  `json:"scopes,omitempty"`
}

// MetaTokenResponse is the response from Meta's OAuth token exchange endpoint.
type MetaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// MetaLongLivedTokenResponse is the response from Meta's long-lived token endpoint.
type MetaLongLivedTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}
