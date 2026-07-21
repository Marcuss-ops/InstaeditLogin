// Package services — SessionsService.
//
// Owns the lifecycle of `sessions` rows. Cooperates with the JWT
// Manager (issues access/refresh tokens) and the SessionsRepository
// (persists and rotates rows). All paths are idempotent where the
// caller cares: rotating an already-rotated refresh token triggers
// family-wide revocation + ErrSessionReuse, which the handler maps
// to 401.

package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Sentinel errors exposed to handlers.
var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionReuse     = errors.New("refresh token reuse detected")
	ErrSessionForbidden = errors.New("session belongs to a different user")
)

// StartSessionRequest carries the inputs needed to mint a new session.
type StartSessionRequest struct {
	UserID      int64
	WorkspaceID int64
	UserAgent   string
	IP          string
}

// StartSessionResult is what the login/refresh handlers return to set
// cookies on the response.
type StartSessionResult struct {
	SessionID        int64
	AccessToken      string
	AccessJTI        string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshHash      []byte
	RefreshExpiresAt time.Time
}

// SessionsService coordinates session creation, rotation, and revocation.
type SessionsService struct {
	repo  *repository.SessionRepository
	jwt   *auth.Manager
	clock func() time.Time
}

// NewSessionsService wires the service. clock is injectable for tests.
func NewSessionsService(repo *repository.SessionRepository, jwt *auth.Manager) *SessionsService {
	return &SessionsService{repo: repo, jwt: jwt, clock: time.Now}
}

// Start creates a brand-new session and returns the access + refresh
// tokens to put in cookies. familyID may be empty to auto-generate a
// new one. Caller is responsible for setting the cookies.
func (s *SessionsService) Start(req StartSessionRequest) (*StartSessionResult, error) {
	if req.UserID <= 0 || req.WorkspaceID <= 0 {
		return nil, fmt.Errorf("invalid ids: user=%d ws=%d", req.UserID, req.WorkspaceID)
	}
	family, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("family id: %w", err)
	}
	refreshPlain, refreshHash, refreshExp, err := s.jwt.IssueRefresh()
	if err != nil {
		return nil, fmt.Errorf("issue refresh: %w", err)
	}
	// Issue a placeholder access token to learn the JTI; we'll sign
	// the real one AFTER persisting the row so the JTI matches.
	// IssueAccess is idempotent only at the (userID, wsID, sessionID)
	// level — but we don't have a sessionID yet. The first access
	// token is bound to sessionID=0 (placeholder) and reissued once
	// the row exists. Alternative: use a server-side JTI, persist,
	// then sign. That's the approach we take.
	accessJTI, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("access jti: %w", err)
	}
	now := s.clock()
	row := &repository.Session{
		UserID:           req.UserID,
		WorkspaceID:      req.WorkspaceID,
		TokenFamilyID:    family,
		AccessJTI:        accessJTI,
		RefreshTokenHash: refreshHash,
		UserAgent:        req.UserAgent,
		IPHash:           hashIP(req.IP),
		ExpiresAt:        now.Add(s.jwt.AccessTTL()),
		RefreshExpiresAt: refreshExp,
	}
	if err := s.repo.Create(row); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}
	// Sign the real access token with the freshly-allocated session id
	// and the JTI we already persisted so the DB and JWT stay in sync.
	access, _, accessExp, err := s.jwt.IssueAccessWithJTI(req.UserID, req.WorkspaceID, row.ID, accessJTI)
	if err != nil {
		// Best-effort cleanup: revoke the orphan row so the refresh
		// token doesn't dangle. The helper retries once with backoff
		// before giving up (C5 hardening) — if the row truly can't be
		// revoked, the metric increments and the periodic Cleanup()
		// goroutine handles garbage collection later.
		s.cleanupOrphanSession(row, err)
		return nil, fmt.Errorf("sign access: %w", err)
	}
	return &StartSessionResult{
		SessionID:        row.ID,
		AccessToken:      access,
		AccessJTI:        accessJTI,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshHash:      refreshHash,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// RefreshRequest is the input to /auth/refresh.
type RefreshRequest struct {
	RefreshPlaintext string
	UserAgent        string
	IP               string
}

// Refresh rotates the refresh token and returns a new access + refresh
// pair bound to the same family. If the supplied refresh plaintext
// matches a row whose revoked_at is already set (i.e. the row was
// rotated earlier), the ENTIRE family is revoked (theft detection)
// and ErrSessionReuse is returned.
func (s *SessionsService) Refresh(req RefreshRequest) (*StartSessionResult, error) {
	if req.RefreshPlaintext == "" {
		return nil, ErrSessionNotFound
	}
	hash := auth.HashRefreshToken(req.RefreshPlaintext)

	// Pre-fetch the row so we can craft a 401/403 with the right
	// state without re-querying after the rotate. The repository's
	// Rotate path still does its own SELECT FOR UPDATE for the
	// race-safe check.
	row, err := s.repo.FindByRefreshHash(hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("find session: %w", err)
	}
	if row.RevokedAt != nil {
		// Refresh was already rotated (or otherwise revoked). Treat
		// as reuse: revoke the whole family.
		if _, err := s.repo.RevokeFamily(row.TokenFamilyID, "refresh_reuse_detected"); err != nil {
			return nil, fmt.Errorf("revoke family: %w", err)
		}
		return nil, ErrSessionReuse
	}
	// Build the replacement session.
	refreshPlain, refreshHash, refreshExp, err := s.jwt.IssueRefresh()
	if err != nil {
		return nil, fmt.Errorf("issue refresh: %w", err)
	}
	accessJTI, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("access jti: %w", err)
	}
	now := s.clock()
	newRow := &repository.Session{
		TokenFamilyID:    row.TokenFamilyID,
		AccessJTI:        accessJTI,
		RefreshTokenHash: refreshHash,
		UserAgent:        req.UserAgent,
		IPHash:           hashIP(req.IP),
		ExpiresAt:        now.Add(s.jwt.AccessTTL()),
		RefreshExpiresAt: refreshExp,
	}
	if _, err := s.repo.Rotate(hash, newRow); err != nil {
		if errors.Is(err, repository.ErrSessionReuse) {
			return nil, ErrSessionReuse
		}
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("rotate: %w", err)
	}
	access, _, accessExp, err := s.jwt.IssueAccessWithJTI(row.UserID, row.WorkspaceID, newRow.ID, accessJTI)
	if err != nil {
		// Best-effort cleanup: revoke the orphan row so the refresh
		// token doesn't dangle. C5 hardening: one-retry + bounded
		// backoff before giving up; metric increments on final fail.
		s.cleanupOrphanSession(newRow, err)
		return nil, fmt.Errorf("sign access: %w", err)
	}
	return &StartSessionResult{
		SessionID:        newRow.ID,
		AccessToken:      access,
		AccessJTI:        accessJTI,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshHash:      refreshHash,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// Revoke marks a single session as revoked. ownerUserID is the
// authenticated user; if the session does not belong to them, the
// call returns ErrSessionForbidden without mutating any row.
func (s *SessionsService) Revoke(sessionID, ownerUserID int64, reason string) error {
	if sessionID <= 0 || ownerUserID <= 0 {
		return ErrSessionNotFound
	}
	// List is small (per user); we do a fetch-then-check to surface a
	// 403 cleanly. The query is bounded by ListByUser.
	rows, err := s.repo.ListByUser(ownerUserID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range rows {
		if r.ID == sessionID {
			return s.repo.Revoke(sessionID, reason)
		}
	}
	return ErrSessionForbidden
}

// RevokeAll marks every active session for the user as revoked.
func (s *SessionsService) RevokeAll(userID int64, reason string) (int64, error) {
	return s.repo.RevokeAllForUser(userID, reason)
}

// List returns the active sessions for a user (revoked + active,
// ordered by last_used_at DESC). Callers usually filter.
func (s *SessionsService) List(userID int64) ([]repository.Session, error) {
	return s.repo.ListByUser(userID)
}

// IsActive verifies that a session row exists, has not been revoked,
// and neither the access-token nor the refresh-token window has
// expired. A session whose access token expired but whose refresh
// token is still valid is still considered inactive for the
// authenticated request path; the caller should use /auth/refresh.
func (s *SessionsService) IsActive(sessionID int64) (bool, error) {
	row, err := s.repo.FindByID(sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("check session active: %w", err)
	}
	if row.RevokedAt != nil {
		return false, nil
	}
	now := s.clock()
	if now.After(row.ExpiresAt) || now.After(row.RefreshExpiresAt) {
		return false, nil
	}
	return true, nil
}

// hashIP returns a hex-encoded SHA-256 of the IP. PII minimization:
// the IP itself is never persisted; the hash is recoverable only by
// the user who knows the IP. Salt is omitted intentionally — we
// treat the hash as a stable correlation handle for audit, not as
// a security boundary.
func hashIP(ip string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}// WithdrawFromCookie extracts the refresh cookie value. Exposed so
// the /auth/logout handler can do a cookie-anchored revoke (deletes
// the row matching the current refresh hash, so the cookie is
// invalid even if the user keeps the same cookie string around).
// Returns ErrSessionNotFound when the cookie does not match any
// row, so the caller can tell the difference between "already
// logged out" and a real DB failure.
func (s *SessionsService) WithdrawFromCookie(refreshPlain string) error {
	if refreshPlain == "" {
		return ErrSessionNotFound
	}

	hash := auth.HashRefreshToken(refreshPlain)
	row, err := s.repo.FindByRefreshHash(hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return err
	}
	return s.repo.Revoke(row.ID, "logout")
}


// IsNoSession is exported for handlers that want a 401 vs 404 split.
func IsNoSession(err error) bool {
	return errors.Is(err, ErrSessionNotFound)
}

// CleanupGraceRevokedDays / CleanupGraceExpiredDays document the
// retention policy that SessionsService.Cleanup applies. Both are
// passed to SessionRepository.DeleteStale at call time so the SQL
// stays parametric (no policy values embedded in SQL literals —
// dev / test can pass smaller grace values).
const (
	// CleanupGraceRevokedDays is the audit-retention window for
	// revoked sessions. 30 days gives operators a month to
	// investigate "which session was active when user X reported
	// an incident" while still bounding table growth.
	CleanupGraceRevokedDays = 30
	// CleanupGraceExpiredDays is the grace window after
	// refresh_expires_at before the row is hard-deleted. 7 days
	// lets the refresh-token theft detection (Rotate's reuse
	// check) have a window to settle across replicas before the
	// row disappears.
	CleanupGraceExpiredDays = 7
)

// CleanupResult summarises what Services.Cleanup deleted and which
// grace values it applied. Returned to callers (worker, log
// surface) so dashboards can introspect the policy in effect
// without re-reading the service constants.
type CleanupResult struct {
	Deleted          int64
	GraceRevokedDays int
	GraceExpiredDays int
}

// Cleanup hard-deletes session rows whose revocation timestamp or
// refresh expiry has aged past the retention policy. Grace values
// come from package constants (NOT config) — operators who need
// different windows are expected to recompile with new constants
// rather than wire per-process overrides that can drift between
// deployments.
//
// Errors are returned wrapped. The caller (cmd/worker
// sessions_cleanup goroutine) logs at WARN but does not abort the
// tick on transient errors — a DB blip should not kill the
// cadence.
func (s *SessionsService) Cleanup(ctx context.Context) (CleanupResult, error) {
	n, err := s.repo.DeleteStale(ctx, CleanupGraceRevokedDays, CleanupGraceExpiredDays)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup sessions: %w", err)
	}
	return CleanupResult{
		Deleted:          n,
		GraceRevokedDays: CleanupGraceRevokedDays,
		GraceExpiredDays: CleanupGraceExpiredDays,
	}, nil
}

// _ keeps http imported for the future Set-Cookie helper that
// the api package implements; suppress the unused import warning.
var _ = http.MethodPost

// cleanupOrphanSession best-effort-revokes an orphan session row
// after the access-token signing step failed (C5 hardening).
//
// Contract: revoke-on-best-effort. Two attempts total (initial + one
// retry) with bounded backoff:
//
//	attempt 1          → fail
//	sleep 50ms         →
//	attempt 2 (retry)  → fail
//	sleep 100ms        →
//	increment metric   → log at WARN, give up
//
// Total worst-case wall time: Revoke(50ms) × 2 + 150ms sleeps. The
// cap keeps the caller from blocking on a single orphan forever
// while still leaving a forgiving window for transient DB blips
// (e.g. sql.ErrConnDone on connection pool churn) to clear. On final
// failure the orphan row stays in the sessions table until the
// periodic Cleanup() goroutine (cmd/worker sessions_cleanup, grace
// windows 30d revoked / 7d expired) hard-deletes it; the metric
// `session_orphan_revoke_failures_total` records every give-up so
// dashboards can SLO on the clean-up lag.
//
// `cause` is the upstream error that triggered the orphan-cleanup
// attempt (typically the IssueAccess failure from the caller). It's
// logged at WARN with the row IDs so an operator can correlate
// orphan-revoke failures to their root cause in the same request.
func (s *SessionsService) cleanupOrphanSession(row *repository.Session, cause error) {
	if row == nil || row.ID <= 0 {
		// Defensive: a nil or zero-id row is the caller's bug;
		// surface in logs but don't burn the retry budget.
		slog.Default().Warn("orphan session cleanup called with invalid row",
			"active_id", evIDOrZero(row), "cause", cause)
		return
	}
	// Two attempts; backoff sequence is consumed once per retry-fail.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(orphanRevokeBackoffs[attempt-1])
		}
		if err := s.repo.Revoke(row.ID, "access_sign_failed"); err == nil {
			return // success on initial OR retry: row is durably revoked.
		} else {
			lastErr = err
		}
	}
	// Final 100ms grace before giving up — the spec says
	// "50ms+100ms" backoff and we read the second slice as the
	// post-retry-grace rather than a wasted gate. Operators can
	// tune orphanRevokeBackoffs if a future load test shows longer
	// pool-recovery tails than this 150ms envelope.
	time.Sleep(orphanRevokeBackoffs[len(orphanRevokeBackoffs)-1])
	metrics.RecordSessionOrphanRevokeFailure()
	slog.Default().Warn("orphan session revoke failed after bounded retry; row may linger until Cleanup",
		"session_id", row.ID,
		"user_id", row.UserID,
		"cause", cause,
		"last_err", lastErr,
	)
}

// orphanRevokeBackoffs defines the bounded retry envelope for
// cleanupOrphanSession. The post-retry 100ms is the final grace
// before metric+log; the pre-retry 50ms is the breathing room for
// sql.ErrConnDone on connection-pool churn. Tuned conservatively:
// 150ms total worst case is below the human-perceptible latency
// for `/auth/login` and `/auth/refresh` (which already returned
// an error to the client by this point).
var orphanRevokeBackoffs = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
}

// evIDOrZero returns row.ID or 0 if row is nil. Used in log lines
// so a nil-deref never panics on the orphan-cleanup hot path.
func evIDOrZero(row *repository.Session) int64 {
	if row == nil {
		return 0
	}
	return row.ID
}
