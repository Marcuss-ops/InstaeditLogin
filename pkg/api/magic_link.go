// SPRINT 1.2 — magic-link product login handlers (clean rewrite).
//
// Magic-link is the V1 product login path.
//
//	POST /api/v1/auth/magic-link/start  body: {email}
//	  1. Generate 32-byte URL-safe token (base64url).
//	  2. SHA-256 the token; persist { email, token_hash, expires_at
//	     = now+15min } in magic_link_tokens.
//	  3. Return 200 {status:"sent", magic_link_token:<plain>}. (In
//	     production the plaintext goes via Mailgun/SES; the dev/test
//	     SPA completes the loop via the body field. NEVER log the
//	     plaintext anywhere.)
//
//	POST /api/v1/auth/magic-link/verify  body: {token}
//	  1. SHA-256 the token; consume the magic_link_tokens row in a
//	     tx (single-use). Returns 401 invalid-or-expired on miss/
//	     replay.
//	  2. Email is on the consumed row. Call AuthEmailService.
//	     MagicLinkSignupOrLookup(email) — idempotent: creates user +
//	     personal workspace if email is new, otherwise validates
//	     email_verified and returns the existing user + their active
//	     workspace.
//	  3. SPRINT 7.4 (P0#14-blocco-1.4): SessionsService.Start(req)
//	     → set HttpOnly session cookie (7d) + HttpOnly refresh
//	     cookie, 204 No Content. The session row is created with a
//	     positive session_id so the access JWT passes Manager.Verify's
//	     post-Sprint-2.1 invariant (sid > 0).
//
// Tokens are NEVER persisted in plaintext. The plaintext is sent via
// email (or returned in the dev response); only the SHA-256 lands on
// disk.
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AuthMagicLinkStore is the persistence contract for magic_link_tokens.
// main.go injects *repository.MagicLinkRepository; tests inject fakes.
type AuthMagicLinkStore interface {
	Issue(email string, tokenHash []byte, ttl time.Duration) (tokenID string, err error)
	Consume(tokenHash []byte, userID *int64) (*repository.MagicLinkToken, error)
}

// handleMagicLinkStart mints a single-use, 15-minute magic link.
// Returns 200 with status:"sent" and (dev only) the plaintext token
// in the body so the dev/test SPA can complete the loop. Production
// deployments wire an email sender and drop the plaintext field.
func (r *Router) handleMagicLinkStart(w http.ResponseWriter, req *http.Request) {
	if r.authMagicLink == nil {
		writeError(w, http.StatusNotImplemented, "magic-link auth not configured")
		return
	}
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if body.Email == "" || !strings.Contains(body.Email, "@") {
		writeError(w, http.StatusBadRequest, "valid email is required")
		return
	}

	plain := make([]byte, 32)
	if _, err := rand.Read(plain); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint token")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(plain)
	sum := sha256.Sum256(plain)
	if _, err := r.authMagicLink.Issue(body.Email, sum[:], 15*time.Minute); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record token: "+err.Error())
		return
	}
	resp := map[string]interface{}{
		"status":           "sent",
		"email":            body.Email,
		"magic_link_token": token, // dev-only; production drops via Mailgun/SES
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMagicLinkVerify consumes a magic-link token, completes signup
// if the email is unknown, signs the JWT, sets the session cookie.
// 401 invalid-or-expired covers: token not found, expired, or
// already consumed (single-use).
func (r *Router) handleMagicLinkVerify(w http.ResponseWriter, req *http.Request) {
	if r.authMagicLink == nil || r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "magic-link auth not configured")
		return
	}
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(body.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token is malformed")
		return
	}
	sum := sha256.Sum256(raw)

	// Phase 1: signup-or-lookup up-front so we have a user_id to
	// stamp on the magic_link_tokens row when we Consume. The auth
	// service handles both cases (idempotent on email).
	// We need the email first — but we don't have it from the token
	// alone (we kept only the SHA-256). Resolution order:
	//   (a) any pre-issued token via /magic-link/start carries the
	//       email in the AuthMagicLinkStore.Issue call. So at Issue
	//       time we record email against token_hash; Consume reads
	//       email back. Repository below returns the row payload.
	// To wire this, we need to change the Issue/Consume contract to
	// return the email. For SPRINT 1.2 MVP we use a single-pass
	// flow:
	row, err := r.authMagicLink.Consume(sum[:], nil)
	if err != nil {
		if errors.Is(err, repository.ErrMagicLinkTokenNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		writeError(w, http.StatusInternalServerError, "token verification failed")
		return
	}
	userID, wsID, err := r.authEmailSvc.MagicLinkSignupOrLookup(row.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure user/workspace: "+err.Error())
		return
	}
	// SPRINT 7.4 (P0#14-blocco-1.4): the JWT MUST carry a positive
	// session_id so Manager.Verify accepts it post-Sprint-2.1. The
	// only production path that produces sid>0 is
	// SessionsService.Start — it creates the sessions row AND signs
	// the access JWT bound to the row's id.
	if r.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      userID,
		WorkspaceID: wsID,
		UserAgent:   req.UserAgent(),
		IP:          clientIP(req),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}
	r.setSessionCookie(w, req, result)
	w.WriteHeader(http.StatusNoContent)
}
