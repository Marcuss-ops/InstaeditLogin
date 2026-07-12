package models

import "time"

// Platform constants identify supported social platforms.
const (
	PlatformInstagram = "instagram"
	PlatformFacebook  = "facebook"
	PlatformThreads   = "threads"
	PlatformTikTok    = "tiktok"
	PlatformTwitter   = "twitter"
	PlatformYouTube   = "youtube"
	PlatformLinkedIn  = "linkedin"
)

// User represents an application user (platform-agnostic).
type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Account status constants for the lifecycle of a linked social account.
const (
	AccountStatusActive         = "active"
	AccountStatusExpired        = "expired"
	AccountStatusReauthRequired = "reauth_required"
	AccountStatusRevoked        = "revoked"
	AccountStatusDisconnected   = "disconnected"
	AccountStatusError          = "error"
)

// PlatformAccount links a User to a social platform profile.
type PlatformAccount struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"user_id"`
	Platform         string     `json:"platform"`
	PlatformUserID   string     `json:"platform_user_id"`
	Username         string     `json:"username"`
	Status           string     `json:"status"`
	ConnectedAt      *time.Time `json:"connected_at,omitempty"`
	LastValidatedAt  *time.Time `json:"last_validated_at,omitempty"`
	LastRefreshAt    *time.Time `json:"last_refresh_at,omitempty"`
	ReauthRequiredAt *time.Time `json:"reauth_required_at,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorMessage string     `json:"last_error_message,omitempty"`
	Metadata         Metadata   `json:"metadata,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Metadata is a generic JSONB container for platform-specific account data.
type Metadata map[string]interface{}

// Token represents an encrypted OAuth token stored in the database.
type Token struct {
	ID                    int64      `json:"id"`
	PlatformAccountID     int64      `json:"platform_account_id"`
	TokenType             string     `json:"token_type"`
	EncryptedToken        []byte     `json:"-"`
	EncryptedRefreshToken []byte     `json:"-"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	Scopes                []string   `json:"scopes"`
	CreatedAt             time.Time  `json:"created_at"`
}

// Token types
const (
	TokenTypeShortLived = "short_lived"
	TokenTypeLongLived  = "long_lived"
	TokenTypePageAccess = "page_access"
	TokenTypeBearer     = "bearer"
)

// OAuthToken represents a decrypted token ready for API use.
type OAuthToken struct {
	AccessToken string     `json:"access_token"`
	TokenType   string     `json:"token_type"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Scopes      []string   `json:"scopes,omitempty"`
}

// PlatformProfile is returned by HandleCallback with user and account info.
type PlatformProfile struct {
	PlatformUserID string
	Username       string
	Email          string
	Name           string
}

// TokenData is the encrypted token returned by HandleCallback.
// RefreshToken is populated when the platform issues one (YouTube, Twitter, TikTok).
// Meta long-lived tokens do not produce a refresh token.
type TokenData struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int64
	Scopes       []string
}

// PublishPayload is the content to publish on a platform.
//
// Text is reused as the caption/description across providers (Meta `caption`,
// YouTube snippet.description, TikTok post_info.title). Privacy/Comment/Duet
// fields are TikTok-specific at the moment but live here so the router
// doesn't need to know which platform supports what.
type PublishPayload struct {
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
	Title    string `json:"title,omitempty"`

	// PrivacyLevel controls who can view the post. TikTok-only.
	// Empty value falls back to PUBLIC_TO_EVERYONE inside the TikTok service.
	PrivacyLevel string `json:"privacy_level,omitempty"`

	// CommentMode controls whether comments are allowed on the post. TikTok-only.
	// Accepted: "allow_all" / "allow" (default) or "no_comments" / "disabled".
	CommentMode string `json:"comment_mode,omitempty"`

	// DuetMode controls whether others can create duets from the post. TikTok-only.
	// Accepted: "allow" (default) or "no_duet" / "disabled".
	DuetMode string `json:"duet_mode,omitempty"`
}

// PublishResult is returned after successful content publishing.
type PublishResult struct {
	PlatformMediaID string `json:"platform_media_id"`
	PlatformURL     string `json:"platform_url,omitempty"`
}

// --- Meta Graph API response types (shared across Instagram / Facebook / Threads) ---

// MetaTokenResponse is the response from Meta's OAuth token exchange endpoint.
type MetaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// MetaLongLivedTokenResponse is the response from Meta's long-lived token
// endpoint (fb_exchange_token), shared across all Meta-family providers.
type MetaLongLivedTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// MetaPage represents a Facebook Page returned by GET /me/accounts.
type MetaPage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token"`
	Category    string `json:"category"`
}

// MetaAccountsResponse is the response from GET /me/accounts.
type MetaAccountsResponse struct {
	Data   []MetaPage `json:"data"`
	Paging struct {
		Cursors struct {
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"cursors"`
	} `json:"paging"`
}
