package bootstrap

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/providers"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

func buildProviderWiring(s *wireState) error {
	var err error
	s.capRouter, err = providers.BuildRegistry(s.cfg)
	if err != nil {
		return fmt.Errorf("build provider registry: %w", err)
	}

	// channelAuthorizer (Task 1/10) — atomic OAuth finalize gate.
	// Pulls the YouTubeChannelBinder off the capability router so
	// AuthorizeChannel can run the channels.list(mine=true)
	// pre-tx guard. YouTube MUST satisfy YouTubeChannelBinder in
	// production — if the assertion fails, fail Wire() fast rather
	// than silently no-op'ing the most important safety net from
	// Task 1/10 (a misconfigured refactor would otherwise let a
	// publish target the wrong channel and only surface the bug
	// at the first upload time).
	var ytBinder services.YouTubeChannelBinder
	if ytp, ok := s.capRouter.Get(models.PlatformYouTube); ok {
		b, typeOK := ytp.(services.YouTubeChannelBinder)
		if !typeOK {
			return fmt.Errorf("youtube provider registered but does not implement YouTubeChannelBinder; channels.list(mine=true) guard would be a silent no-op (Task 1/10 invariant violated)")
		}
		ytBinder = b
	}
	s.channelAuthorizer = services.NewChannelAuthorizationService(s.db, s.enc, s.tokenRepo, ytBinder)

	// YouTubeCredentialResolver is the shared pre-action credential
	// boundary for livestream and future YouTube workers. It reuses the
	// existing vault, account/workspace repositories, OAuth capability,
	// and channel binder; it never persists the returned access token.
	if ytp, ok := s.capRouter.Get(models.PlatformYouTube); ok {
		if ytOAuth, typeOK := ytp.(*services.YouTubeOAuthService); typeOK {
			s.youtubeCredentialResolver = services.NewYouTubeCredentialResolver(services.YouTubeCredentialResolverDeps{
				Accounts:    s.userRepo,
				Workspaces:  s.workspaceRepo,
				Memberships: s.teamRepo,
				Grants:      s.userRepo,
				Vault:       s.vault,
				OAuth:       ytOAuth,
				Binder:      ytBinder,
				Logger:      s.logger,
			})
		}
	}

	s.authMgr = auth.NewManager(
		s.cfg.Auth.JWTSecret,
		time.Duration(s.cfg.Auth.JWTAccessTTLMinutes)*time.Minute,
		time.Duration(s.cfg.Auth.JWTRefreshTTLDays)*24*time.Hour,
	).WithEnv(s.cfg.HTTP.AppEnv)
	s.oneTimeCodes = api.NewOneTimeCodePostgresStore(s.db, 60*time.Second)
	// oneTimeCodes sweeper is gracefully stopped by RunWorkers. cmd/api
	// (HTTP-only binary) does not run RunWorkers, so the sweeper is
	// collected at process termination there. Exposing the
	// store on App avoids re-constructing it in RunWorkers —
	// the same instance is shared across api + worker processes
	// when cmd/server bundles both.

	return nil
}
