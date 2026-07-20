// Package services — RateLimitService.
//
// Owns the application-level multi-tier rate-limit policy. Each
// tier is identified by a scope string and a per-minute limit:
//
//	"ws_post:<workspaceID>"     POST /api/v1/posts           60/min  (Postgres)
//	"apikey_read:<apiKeyID>"   GET  /api/v1/api-keys/*      600/min (Postgres)
//	"media_presign"             POST /api/v1/media/presign  30/min  (in-memory)
//	"oauth_ip:<ip>"            GET  /api/v1/auth/*/login   20/min  (in-memory)
//
// The Postgres tiers MUST be shared across replicas (per-workspace,
// per-API-key) — the user explicitly forbade in-memory limiters
// for these. The in-memory tiers are per-replica coarse backstops;
// the real per-IP gate is the edge tier (Cloudflare/reverse proxy),
// documented in docs/OPERATIONS.md.
//
// Failure mode: on Postgres error the service fails OPEN (the
// request is allowed). Rationale: a 5xx blip should not take
// down the publish/create flows. The edge tier still applies the
// per-IP rate limit, so the request is throttled upstream.
package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// RateLimitRepo is the subset of *repository.RateLimitRepository
// the service depends on. Defined inline so tests can inject a
// fake without importing internal/repository.
type RateLimitRepo interface {
	Increment(ctx context.Context, scope string, window time.Duration) (count int, resetAt time.Time, err error)
}

// RateLimitService is the application-level rate-limit coordinator.
// It owns the Postgres-backed counter repository and a small
// in-memory token-bucket for the per-replica tiers.
type RateLimitService struct {
	repo  RateLimitRepo
	mem   *MemoryLimiter
	clock func() time.Time
}

// NewRateLimitService wires the service. repo is required (the
// Postgres tiers are the only ones shared across replicas and
// must not be skipped in production). mem is constructed
// internally with sensible defaults; tests can use
// NewRateLimitServiceWithMemory to inject a pre-seeded limiter.
func NewRateLimitService(repo RateLimitRepo) *RateLimitService {
	return &RateLimitService{
		repo:  repo,
		mem:   NewMemoryLimiter(),
		clock: time.Now,
	}
}

// NewRateLimitServiceWithMemory is a test constructor that lets
// the caller pre-seed the in-memory limiter. Production wiring
// should use NewRateLimitService.
func NewRateLimitServiceWithMemory(repo RateLimitRepo, mem *MemoryLimiter) *RateLimitService {
	return &RateLimitService{
		repo:  repo,
		mem:   mem,
		clock: time.Now,
	}
}

// Shutdown stops the in-memory limiter's background reaper. Idempotent.
// Production wiring should call this in the graceful-shutdown path
// (cmd/server/main.go) so the reaper goroutine does not leak when
// the process exits. Tests use it via `defer svc.Shutdown()` to
// keep the reaper contained within the test process.
func (s *RateLimitService) Shutdown() {
	if s == nil || s.mem == nil {
		return
	}
	s.mem.Shutdown()
}

// Check is the tier-agnostic entry point. limit is per minute
// (the spec'd unit for all tiers). Returns:
//
//	allowed   — true if the request is under budget
//	remaining — tokens left in the current window (0 when over)
//	resetAt   — when the window refills (UTC)
//	err       — non-nil on backend failure (caller decides fail-open vs fail-closed)
//
// On Postgres error the service returns (true, limit, now+window, err)
// so the middleware fails open. Callers that want fail-closed should
// inspect err themselves.
func (s *RateLimitService) Check(ctx context.Context, tier Tier, limit int) (allowed bool, remaining int, resetAt time.Time, err error) {
	if limit <= 0 {
		// 0 / negative limit means "no limit". Always allowed.
		return true, 0, s.clock().Add(time.Minute), nil
	}
	switch tier.Storage {
	case StoragePostgres:
		if s.repo == nil {
			return true, limit, s.clock().Add(time.Minute), fmt.Errorf("rate-limit: postgres repo not configured")
		}
		count, ra, ierr := s.repo.Increment(ctx, tier.Scope, time.Minute)
		if ierr != nil {
			slog.Warn("rate-limit postgres error, failing open", "scope", tier.Scope, "err", ierr)
			return true, limit, s.clock().Add(time.Minute), ierr
		}
		rem := limit - count
		if rem < 0 {
			rem = 0
		}
		return count <= limit, rem, ra, nil
	case StorageMemory:
		if s.mem == nil {
			return true, limit, s.clock().Add(time.Minute), fmt.Errorf("rate-limit: memory limiter not configured")
		}
		ok, rem, ra := s.mem.Allow(tier.Scope, limit, time.Minute)
		return ok, rem, ra, nil
	default:
		return true, limit, s.clock().Add(time.Minute), fmt.Errorf("rate-limit: unknown storage %v", tier.Storage)
	}
}

// Storage is the counter location for a tier.
type Storage int

const (
	// StoragePostgres backs the counter with rate_limit_counters
	// (shared across replicas). Used for per-workspace, per-key.
	StoragePostgres Storage = iota + 1
	// StorageMemory backs the counter with a per-replica token
	// bucket. Used for per-IP (OAuth start) and per-endpoint
	// (media presign) coarse backstops. The edge tier is the
	// real per-IP gate.
	StorageMemory
)

// Tier identifies a rate-limit policy by scope and storage. The
// Scope is the string the counter is keyed on; builders are
// provided for the canonical tiers.
type Tier struct {
	Storage Storage
	Scope   string
	Limit   int // per minute
}

// Canonical tier factories.

// WorkspacePostLimit returns the per-workspace /api/v1/posts
// tier: 60 POSTs/min/workspace, Postgres-backed.
func WorkspacePostLimit(workspaceID int64) Tier {
	return Tier{
		Storage: StoragePostgres,
		Scope:   fmt.Sprintf("ws_post:%d", workspaceID),
		Limit:   60,
	}
}

// APIKeyReadLimit returns the per-API-key /api/v1/api-keys/*
// tier: 600 reads/min/key, Postgres-backed.
func APIKeyReadLimit(apiKeyID int64) Tier {
	return Tier{
		Storage: StoragePostgres,
		Scope:   fmt.Sprintf("apikey_read:%d", apiKeyID),
		Limit:   600,
	}
}

// MediaPresignLimit returns the per-endpoint /api/v1/media/presign
// tier: 30/min, in-memory coarse backstop. The "endpoint" scope is
// shared across all callers — this is a per-replica coarse gate.
func MediaPresignLimit() Tier {
	return Tier{
		Storage: StorageMemory,
		Scope:   "endpoint:media_presign",
		Limit:   30,
	}
}

// OAuthStartLimit returns the per-IP OAuth-start tier:
// 20/min/IP, in-memory coarse backstop. The edge tier is the
// real per-IP gate.
func OAuthStartLimit(ip string) Tier {
	return Tier{
		Storage: StorageMemory,
		Scope:   "oauth_ip:" + ip,
		Limit:   20,
	}
}

// APIKeyReadLimitFromKeyID is a convenience for handlers that
// hold the apiKeyID directly (avoids building the scope by hand).
func APIKeyReadLimitFromKeyID(keyID int64) Tier { return APIKeyReadLimit(keyID) }
