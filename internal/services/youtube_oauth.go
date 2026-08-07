package services

import (
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"net/http"
	"time"
)

// YouTubeOAuthService implements the YouTube provider. Taglio 2.1:
//
// Capabilities exposed:
//   - OAuthProvider (Google OAuth 2.0 with offline access)
//   - ContentValidator (video required)
//   - Publisher (resumable upload protocol)
//   - AccountManager (Validate / Revoke)
type YouTubeOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
	// uploadOpts (P1#6) — every chunked-PUT retry + backoff knob.
	// Populated from cfg in NewYouTubeOAuthService; tests override
	// backoff/sleep via the unexported uploadDeps fields.
	uploadOpts youTubeUploadOptions
	// uploadDeps (P1#6) — test-injectable backoff/sleep functions.
	// nil in production: NewYouTubeOAuthService installs the
	// defaults (computeYouTubeBackoff + defaultYouTubeSleep).
	uploadDeps *youTubeUploadDeps
	// sessionStore persists the resumable-upload session URI + offset
	// across worker crashes (P1#5 / migration 048). Wired in
	// NewYouTubeOAuthService from *repository.UploadJobRepository
	// (concrete type kept out of this struct via the
	// YouTubeSessionStore narrow interface). Optional in tests.
	sessionStore YouTubeSessionStore
	// sessionEncryptor wraps the YouTube session URI before
	// persistence. Required when sessionStore != nil: storing the
	// plaintext URI in upload_jobs.youtube_session_uri defeats the
	// "credential-adjacent" intent of migration 048 + the
	// json:"-" redaction on the Go side. nil encryptor on a nil
	// store is the production default (the publish path doesn't
	// need it for single-shot uploads); nil encryptor on a non-nil
	// store surfaces as a constructor error.
	sessionEncryptor SessionEncryptor
	// sessionJobID + sessionWorkerID are stamped onto every
	// sessionStore.* call so the CAS in SaveYouTubeSession /
	// ClearYouTubeSession can refuse a write against a row that
	// has been re-claimed (or lease-expired) by another worker.
	// Defaults to empty; the upload worker injects both via
	// SetSessionContext before calling Publish/StartPublish.
	sessionJobID    int64
	sessionWorkerID string
	// pool (YouTube OAuth Client Pool, R4) is the optional A/B client
	// registry used by RefreshOAuthToken. When wired (bootstrap), every
	// YouTube refresh resolves the grant's oauth_client_key (stamped on
	// ctx by CredentialVault.Renew) against this registry and refreshes
	// with the EXACT client that issued the token. nil keeps the legacy
	// single-client refresh path (cfg.Auth.YouTubeClientID) untouched.
	pool *YouTubeOAuthClientRegistry
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection.
func NewYouTubeOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*YouTubeOAuthService, error) {
	if cfg.Auth.YouTubeClientID == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	opts := loadYouTubeUploadOptions(cfg)
	return &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
		uploadOpts: opts,
		uploadDeps: loadYouTubeUploadDeps(opts),
	}, nil
}

// SetYouTubeOAuthPool wires the optional YouTube OAuth Client Pool
// registry onto the service so RefreshOAuthToken refreshes each grant
// with the client that issued it (resolved via the grant's
// oauth_client_key). Nil (default) keeps the legacy single-client
// refresh path. The registry never exposes client secrets.
func (s *YouTubeOAuthService) SetYouTubeOAuthPool(pool *YouTubeOAuthClientRegistry) {
	s.pool = pool
}

// ClientID returns the YouTube OAuth client_id this service was
// configured with (cfg.Auth.YouTubeClientID). Used by pkg/api/handlers.go
// handleValidateAccount to compare Google's tokeninfo `aud` against
// the configured client — a Production-but-issued-for-Testing token
// carries a mismatched aud and is a hard reauth signal (the 4-step
// pipeline's STEP 2 guard). Returns "" if the service hasn't been
// fully constructed (defensive — the production wiring wires
// cfg.Auth.YouTubeClientID at NewYouTubeOAuthService time).
func (s *YouTubeOAuthService) ClientID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.Auth.YouTubeClientID
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *YouTubeOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *YouTubeOAuthService) Name() string { return models.PlatformYouTube }

// PreferredTokenTypes declares that YouTube stores the OAuth grant as a
// bearer token. Validation checks bearer first, then falls back to the
// other common token types for backwards compatibility.
func (s *YouTubeOAuthService) PreferredTokenTypes() []string {
	return []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
	}
}

// Compile-time assertion (matches the YouTubeChannelBinder /
// YouTubeCanaryUploader guard pattern below). Caught by `go vet`,
// not at runtime.
var _ error = (*ErrChannelListSafetyCap)(nil)

// Compile-time assertion: YouTubeOAuthService satisfies the
// services.YouTubeChannelBinder capability interface. Caught by
// `go vet`, not at runtime.
var _ YouTubeChannelBinder = (*YouTubeOAuthService)(nil)

var _ YouTubeCanaryUploader = (*YouTubeOAuthService)(nil)

// P1 (Blocco #1 followup) — youtube_privacy_updater.go adds the
// post-upload privacy-transition cast used by PublishWorker in
// Phase 2 (skip-reupload path). The assertion keeps the contract
// honest: a future refactor that renames UpdateVideoPrivacy or
// changes its signature would surface here at vet time instead of
// at runtime on a real publish tick.
var _ YouTubePrivacyUpdater = (*YouTubeOAuthService)(nil)

var _ OAuthGrantRevoker = (*YouTubeOAuthService)(nil)

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider          = (*YouTubeOAuthService)(nil)
	_ ContentValidator       = (*YouTubeOAuthService)(nil)
	_ Publisher              = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher         = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer      = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
