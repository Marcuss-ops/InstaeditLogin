package services

import "github.com/Marcuss-ops/InstaeditLogin/internal/config"

// OAuthConfig is the configuration contract required by OAuth provider
// constructors. Each platform's getters are added to this interface
// on a per-provider basis as the corresponding *_oauth.go file is
// refactored away from concrete *config.Config.
//
// The interface and its concrete adapter live in this single file so
// bootstrap wiring is a one-import change. New methods are added here
// in the SAME atomic commit as the per-platform extraction that uses
// them (see pkg/api/contracts/doc.go for the parallel "lock-step
// rule" that governs contracts/imports updates).
//
// Why an interface instead of *config.Config directly: the providers'
// primary collaborators are HTTP clients and clocks (already
// abstracted via ProviderDependencies). Coupling the providers to
// *config.Config forces every test to construct a full Config struct
// even when only three fields matter. OAuthConfig gives each provider
// only the few getters it actually consumes, and lets tests fake
// them with a five-line struct.
type OAuthConfig interface {
	// -------------------------------------------------------------------------
	// LinkedIn — added when linkedin_oauth.go was decoupled (smallest
	// platform, refactored first). Each subsequent platform extraction
	// adds its getters in lock-step here.
	// -------------------------------------------------------------------------
	LinkedInClientID() string
	LinkedInClientSecret() string
	LinkedInRedirectURI() string
}

// ConfigAdapter wraps the concrete *config.Config to satisfy
// OAuthConfig. Bootstrap constructs ONE instance per process (in
// providers.BuildRegistry) and passes it to every provider
// constructor; a future swap of the configuration backend (Viper,
// Vault, etcd, …) only touches this file.
//
// Tests can use NewConfigAdapter against a stub *config.Config, or
// implement OAuthConfig on a tiny in-test struct when only a few
// getters matter.
type ConfigAdapter struct {
	cfg *config.Config
}

// NewConfigAdapter returns a ConfigAdapter that proxies the
// configured getters to the wrapped *config.Config. The constructor is
// the single hand-off point callers should use to keep the concrete
//→adapter conversion localized.
func NewConfigAdapter(cfg *config.Config) *ConfigAdapter {
	return &ConfigAdapter{cfg: cfg}
}

func (a *ConfigAdapter) LinkedInClientID() string     { return a.cfg.LinkedInClientID }
func (a *ConfigAdapter) LinkedInClientSecret() string { return a.cfg.LinkedInClientSecret }
func (a *ConfigAdapter) LinkedInRedirectURI() string  { return a.cfg.LinkedInRedirectURI }

// Compile-time assertion: any future drift between the interface
// and the adapter surfaces as a build error here.
var _ OAuthConfig = (*ConfigAdapter)(nil)
