package models

import "time"

// Platform constants identify supported social platforms.
const (
	PlatformMeta    = "meta"
	PlatformTikTok  = "tiktok"
	PlatformTwitter = "twitter"
	PlatformYouTube = "youtube"
)

// User represents an application user (platform-agnostic).
type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PlatformAccount links a User to a social platform profile.
type PlatformAccount struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	Username       string    `json:"username"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

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

// --- Legacy Meta-specific types (kept for facebook_oauth.go refactoring) ---

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
