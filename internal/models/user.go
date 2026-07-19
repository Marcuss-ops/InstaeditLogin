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

// UserID uniquely identifies an application User.
type UserID int64

// User represents an application user (platform-agnostic).
type User struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email,omitempty"`
	Name          string    `json:"name"`
	PasswordHash  []byte    `json:"-"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// P2 — ops dashboard admin gate. Bootstrap via
	// cmd/grant-admin --email <email>; the JWT admin claim
	// surfaces IsAdmin() through Identity. Stored per-row so
	// admin promotion is a single UPDATE + immediate token
	// re-mint, with no separate permissions table needed.
	// Legacy User-id-only SELECTs continue to return zero-valued
	// admin fields (which is the safe default for the rest of
	// the codebase); the admin endpoints read via FindByEmail /
	// FindByID (extended in migration 051 + user_repo extensions).
	IsAdmin        bool       `json:"is_admin"`
	AdminGrantedAt *time.Time `json:"admin_granted_at,omitempty"`
	AdminGrantedBy *int64     `json:"admin_granted_by,omitempty"`
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
	// OAuthConnectionID is the FK to oauth_connections.id (migration
	// 043). Populated by the backfill for historical rows (one
	// oauth_connection per (user, platform, platform_user_id)
	// tuple, provider_subject_id left empty until the next
	// callback re-stores the token under the lineage). For rows
	// created AFTER migration 043, the handler / repo must INSERT
	// into oauth_connections at attach time AND link this FK in
	// the same statement (a follow-up commit wires that lifecycle).
	OAuthConnectionID *int64    `json:"oauth_connection_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Metadata is a generic JSONB container for platform-specific account data.
type Metadata map[string]interface{}

// Token represents an encrypted OAuth token stored in the database.
type Token struct {
	ID                    int64      `json:"id"`
	PlatformAccountID     int64      `json:"platform_account_id,omitempty"`
	// OAuthConnectionID (P0#3 — migration 053) is the vault's PRIMARY
	// storage key. The vault resolves it from platform_account_id via
	// platform_accounts.oauth_connection_id on every Save/Get/Renew/Revoke
	// (the lookup is a single indexed SELECT — the resolver is internal to
	// internal/credentials/vault.go so caller signatures stay
	// backwards-compatible on platformAccountID). Set after migration 053
	// has applied; pre-053 rows have it populated by the migration's
	// backfill, NOT NULL.
	OAuthConnectionID     int64      `json:"-"`
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

	// PrivacyLevel controls who can view the post. Required by TikTok
	// (PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY), YouTube
	// (public, unlisted, private), and LinkedIn (PUBLIC, CONNECTIONS).
	// Taglio 4b: no default — empty value causes a validation_error.
	PrivacyLevel string `json:"privacy_level,omitempty"`

	// CommentMode controls whether comments are allowed on the post. TikTok-only.
	// Accepted: "allow_all" / "allow" (default) or "no_comments" / "disabled".
	CommentMode string `json:"comment_mode,omitempty"`

	// DuetMode controls whether others can create duets from the post. TikTok-only.
	// Accepted: "allow" (default) or "no_duet" / "disabled".
	DuetMode string `json:"duet_mode,omitempty"`

	// IdempotencyKey (Taglio 4.7 LEVEL 2, migration 022) is the
	// per-target provider-side dedup key. The worker writes this onto
	// each post_target post-claim and forwards it here on the publish
	// call. Providers that support per-call idempotency keys
	// (LinkedIn's "X-Restli-Idempotency-Key", Twitter v2's request_id,
	// TikTok's "idempotent" query param, etc) forward it on their
	// upstream HTTP call; providers without native idempotency simply
	// ignore this field — our DB-level
	// UNIQUE(platform_account_id, provider_idempotency_key) constraint
	// is the catch-all safety net.
	//
	// `json:"-"` because this field is OUR internal worker plumbing,
	// not part of the user-facing API contract.
	IdempotencyKey string `json:"-"`

	// Source discriminates the platform-specific publish path. Empty
	// (default) or PublishSourcePULLFromURL lets the platform fetch
	// the video from VideoURL via CDN (TikTok Direct Post, Instagram
	// Graph, Threads container, etc.). PublishSourcePULLFromFile
	// triggers the chunked-upload path: backend downloads bytes from
	// VideoURL, chunks them, PUTs each chunk to the platform's
	// returned upload_url, and calls the platform's
	// upload/complete/{platform} endpoint to finalize. PULL_FROM_FILE
	// is the only way to use the platform's `video.upload` scope
	// (Upload-as-Draft) — TikTok today; future async platforms may
	// accept the same field.
	Source string `json:"source,omitempty"`

	// PublishAt is the desired public publish time for platforms that
	// support scheduled publishing (YouTube). When set and in the
	// future, the provider uploads the video as private and asks the
	// platform to make it public at the specified time.
	PublishAt *time.Time `json:"publish_at,omitempty"`
}

// PublishSource* values are the canonical Source discriminator. An
// empty PublishPayload.Source means PULL_FROM_URL (the historical
// default); only set a Source when a per-call override is needed.
//
// Taglio cull (post-merge): the empty-string sentinel `PublishSource`
// constant was removed — nothing in the codebase compares against
// PublishPayload.Source == PublishSource (the dispatcher in
// tiktok_oauth.go uses an explicit `strings.EqualFold(..., PublishSourcePULLFromFile)`
// check, so anything other than `PULL_FROM_FILE` falls through to
// the legacy PULL_FROM_URL path including the empty default).
const (
	PublishSourcePULLFromURL  = "PULL_FROM_URL"
	PublishSourcePULLFromFile = "PULL_FROM_FILE"
)

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
