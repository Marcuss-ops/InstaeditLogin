// Package providers is the single home for the OAuth/OAuth-publishing
// provider registry. main.go calls BuildRegistry at startup and
// hands the resulting *CapabilityRegistry to whatever consumer needs
// per-capability lookups (the HTTP router, the publish worker, the
// S3 storage wiring, future CLIs).
//
// Taglio 2.5 motivation: before this package existed, the platform
// wiring was inlined in main.go as a chain of "if cfg.X != nil
// register X" blocks, each with its own slog.Info line and a bespoke
// error policy (Facebook aborts on error, TikTok warns and skips,
// etc.). Extracting into BuildRegistry achieves three things:
//
//  1. **Single source of truth for platform wiring** — adding a new
//     platform touches one file (registry.go) and the corresponding
//     service constructor; main.go stays unchanged.
//
//  2. **Normalized error policy** — every per-platform failure is
//     warn-and-skip. No more "Facebook aborts the server but TikTok
//     silently disappears" asymmetry. A future caller that wants
//     fail-fast on missing platforms can check registry.Names() after
//     BuildRegistry and abort itself.
//
//  3. **No per-platform log spam** — the repeated "X OAuth provider
//     registered / skipped" slog lines that used to fill the startup
//     banner are gone. The single summary line ("platforms: [...]")
//     that api.NewRouter already logs is enough for operators.
//
// The Dependency variadic on BuildRegistry is a forward-compat
// extension point. Today no Dependency is required (BuildRegistry
// uses slog.Default() and the raw *config.Config). When a future
// provider needs an injected *http.Client, a clock, or a mock logger
// for testing, a WithXxx Dependency is added without changing the
// BuildRegistry signature.
package providers

import (
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// CapabilityRegistry is the single handle consumers use to look up
// per-platform capabilities (OAuth, Publisher, AccountDiscoverer,
// ContentValidator, PublishReconciler). It is a type alias for
// *services.CapabilityRouter so existing callers
// (api.NewRouter, worker.NewPublishWorker) accept it without
// any import change — the alias makes the two types
// interchangeable at the call site.
type CapabilityRegistry = *services.CapabilityRouter

// PlatformRegistry is the canonical Zernio-like name for the central
// platform registry. Per the Platform Registry contract:
//
//	registry.Register("instagram",  instagramProvider)
//	registry.Register("tiktok",     tiktokProvider)
//	registry.Register("twitter",    twitterProvider)
//	registry.Register("youtube",    youtubeProvider)
//	registry.Register("linkedin",   linkedinProvider)
//	registry.Register("facebook",   facebookProvider)
//	registry.Register("threads",    threadsProvider)
//
// Type-aliased to CapabilityRegistry / *services.CapabilityRouter
// so existing call sites compile unchanged. Handler/worker/SDK
// consumers can use either name interchangeably.
type PlatformRegistry = *services.CapabilityRouter

// Dependency is a forward-compat extension point for BuildRegistry.
// Each Dependency mutates the internal builder before platforms are
// registered. Today the only exposed Dependency is WithLogger; more
// (WithHTTPClient, WithClock) will be added as a future provider
// needs them.
type Dependency func(*registryBuilder)

// registryBuilder is the internal state BuildRegistry's Dependency
// closures mutate. Fields are exported through the closure, not
// directly, so the public surface stays narrow.
type registryBuilder struct {
	cfg    *config.Config
	logger *slog.Logger
}

// WithLogger overrides the default slog.Default() used for the
// "Skipped X provider" warn lines. Tests inject a bytes-buffer-backed
// logger to assert on the skip messages; production code can ignore
// this and let the default apply.
func WithLogger(l *slog.Logger) Dependency {
	return func(b *registryBuilder) { b.logger = l }
}

// BuildRegistry constructs the CapabilityRegistry from cfg, wiring
// every per-platform service whose required env vars are set. The
// returned error is currently always nil — it exists for future
// extension (e.g. a future provider whose constructor can fail in a
// way that should abort startup, with the existing per-platform
// services remaining warn-and-skip).
//
// Per-platform failure policy: warn-and-skip. If a platform's
// constructor returns an error or its required config is missing,
// BuildRegistry logs a warn line and continues to the next platform.
// A deployment with zero platforms configured is technically valid
// (the server boots, /api/v1/auth/{anything} returns 404) but the
// caller should check registry.Names() if it wants to enforce
// "at least one platform must be configured".
func BuildRegistry(cfg *config.Config, deps ...Dependency) (CapabilityRegistry, error) {
	b := &registryBuilder{cfg: cfg, logger: slog.Default()}
	for _, d := range deps {
		d(b)
	}

	router := services.NewCapabilityRouter()

	// Facebook (shared Meta OAuth credentials). Register when the
	// Meta-family Facebook redirect URI is set AND the shared Meta
	// credentials are both present. Taglio 2.4: each provider is
	// fully independent — a deployment can run with only YouTube /
	// only LinkedIn / etc. with zero Meta config. The constructor
	// itself only checks the redirect URI; the registry is the
	// single place that knows "META_APP_ID + META_APP_SECRET are
	// required for Facebook to work" and warns-and-skips
	// accordingly.
	if cfg.FacebookRedirectURI != "" {
		if cfg.MetaAppID == "" || cfg.MetaAppSecret == "" {
			b.logger.Warn("Skipped Facebook provider: META_APP_ID and META_APP_SECRET are required (or unset FACEBOOK_REDIRECT_URI to disable)")
			// Do not call the constructor — it would build a service
			// with an empty client_id, which would fail noisily on
			// the first /auth/facebook/login hit.
		} else {
			fb, err := services.NewFacebookOAuthService(cfg)
			if err != nil {
				b.logger.Warn("Skipped Facebook provider (constructor failed)", "error", err)
			} else if fb != nil {
				router.Register(fb.Name(), fb)
			}
		}
	}

	if cfg.TikTokClientKey != "" {
		tik, err := services.NewTikTokOAuthService(cfg)
		if err != nil {
			b.logger.Warn("Skipped TikTok provider (constructor failed)", "error", err)
		} else if tik != nil {
			router.Register(tik.Name(), tik)
		}
	}

	if cfg.TwitterClientID != "" {
		tw, err := services.NewTwitterOAuthService(cfg)
		if err != nil {
			b.logger.Warn("Skipped Twitter/X provider (constructor failed)", "error", err)
		} else if tw != nil {
			router.Register(tw.Name(), tw)
		}
	}

	if cfg.YouTubeClientID != "" {
		yt, err := services.NewYouTubeOAuthService(cfg)
		if err != nil {
			b.logger.Warn("Skipped YouTube provider (constructor failed)", "error", err)
		} else if yt != nil {
			router.Register(yt.Name(), yt)
		}
	}

	if cfg.LinkedInClientID != "" {
		li, err := services.NewLinkedInOAuthService(cfg)
		if err != nil {
			b.logger.Warn("Skipped LinkedIn provider (constructor failed)", "error", err)
		} else if li != nil {
			router.Register(li.Name(), li)
		}
	}

	// Threads (Zernio 2.1): Meta-family async publishing.
	if cfg.ThreadsRedirectURI != "" {
		th, err := services.NewThreadsOAuthService(cfg)
		if err != nil {
			b.logger.Warn("Skipped Threads provider (constructor failed)", "error", err)
		} else if th != nil {
			router.Register(th.Name(), th)
		}
	}

	// Instagram (Taglio 4.4): Meta-family media-only. Independent
	// registration — a deployment can enable only Instagram without
	// the rest of Meta-family. Same shared-credentials check as
	// Facebook: cfg.MetaAppID + cfg.MetaAppSecret are required.
	if cfg.InstagramRedirectURI != "" {
		if cfg.MetaAppID == "" || cfg.MetaAppSecret == "" {
			b.logger.Warn("Skipped Instagram provider: META_APP_ID and META_APP_SECRET are required (or unset INSTAGRAM_REDIRECT_URI to disable)")
		} else {
			ig, err := services.NewInstagramOAuthService(cfg)
			if err != nil {
				b.logger.Warn("Skipped Instagram provider (constructor failed)", "error", err)
			} else if ig != nil {
				router.Register(ig.Name(), ig)
			}
		}
	}

	return router, nil
}
