package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AuthEmailStore is the subset of AuthService methods consumed by the
// email/password handlers. The interface is local to the api package so
// test fakes can implement it without importing internal/services.
type AuthEmailStore interface {
	Register(email, password, name string) (userID int64, jwtToken string, err error)
	Login(email, password string) (userID int64, jwtToken string, err error)
	IssueVerificationToken(userID int64, email string) (string, error)
	VerifyEmail(token string) (int64, error)
	IssueResetToken(email string) (string, error)
	ResetPassword(token, newPassword string) error
}

// AuthEmailServiceAdapter adapts *services.AuthService to the local
// AuthEmailStore interface used by the API handlers. Exported via
// NewAuthEmailServiceAdapter so cmd/server/main.go can wrap the
// concrete service without exporting every private helper.
type AuthEmailServiceAdapter struct {
	svc *services.AuthService
}

// NewAuthEmailServiceAdapter wraps a *services.AuthService as an
// AuthEmailStore. Required since AuthService has a richer API
// (Register returns *models.User, the adapter projects user.ID).
func NewAuthEmailServiceAdapter(svc *services.AuthService) AuthEmailStore {
	return &AuthEmailServiceAdapter{svc: svc}
}

func (a *AuthEmailServiceAdapter) Register(email, password, name string) (int64, string, error) {
	user, jwt, err := a.svc.Register(email, password, name)
	if err != nil {
		return 0, "", err
	}
	return user.ID, jwt, nil
}

func (a *AuthEmailServiceAdapter) Login(email, password string) (int64, string, error) {
	user, jwt, err := a.svc.Login(email, password)
	if err != nil {
		return 0, "", err
	}
	return user.ID, jwt, nil
}

func (a *AuthEmailServiceAdapter) IssueVerificationToken(userID int64, email string) (string, error) {
	return a.svc.IssueVerificationToken(userID, email)
}

func (a *AuthEmailServiceAdapter) VerifyEmail(token string) (int64, error) {
	return a.svc.VerifyEmail(token)
}

func (a *AuthEmailServiceAdapter) IssueResetToken(email string) (string, error) {
	return a.svc.IssueResetToken(email)
}

func (a *AuthEmailServiceAdapter) ResetPassword(token, newPassword string) error {
	return a.svc.ResetPassword(token, newPassword)
}

// -----------------------------------------------------------------------
//  Handler registration
// -----------------------------------------------------------------------

// registerAuthEmailRoutes adds email/password auth endpoints to the chi mux.
// Called from Router.Setup() when authEmailSvc is configured.
func (r *Router) registerAuthEmailRoutes() {
	r.mux.Method(http.MethodPost, "/api/v1/auth/register", http.HandlerFunc(r.handleRegister))
	r.mux.Method(http.MethodPost, "/api/v1/auth/verify", http.HandlerFunc(r.handleVerifyEmail))
	r.mux.Method(http.MethodPost, "/api/v1/auth/login", http.HandlerFunc(r.handleLoginEmail))
	r.mux.Method(http.MethodPost, "/api/v1/auth/forgot-password", http.HandlerFunc(r.handleForgotPassword))
	r.mux.Method(http.MethodPost, "/api/v1/auth/reset-password", http.HandlerFunc(r.handleResetPassword))
}

// -----------------------------------------------------------------------
//  Handlers
// -----------------------------------------------------------------------

// handleRegister creates a new SaaS user with email + password.
// POST /api/v1/auth/register
func (r *Router) handleRegister(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" || body.Password == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "email, password, and name are required")
		return
	}

	userID, jwtToken, err := r.authEmailSvc.Register(body.Email, body.Password, body.Name)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrPasswordTooShort) || errors.Is(err, services.ErrPasswordNoDigit):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, services.ErrEmailAlreadyTaken):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "registration failed")
		}
		return
	}

	// TODO(FASE 2.2): Send verification token via email (Mailgun/SES).
	// For dev/test the token is returned in the response body.
	verifyToken, err := r.authEmailSvc.IssueVerificationToken(userID, body.Email)
	if err != nil {
		slog.Warn("verification token generation failed", "email", body.Email, "error", err)
	}

	setSessionCookie(w, jwtToken)

	resp := map[string]interface{}{
		"user_id": userID,
		"email":   body.Email,
	}
	if verifyToken != "" {
		resp["verification_token"] = verifyToken
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleVerifyEmail marks a user's email as verified.
// POST /api/v1/auth/verify
func (r *Router) handleVerifyEmail(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
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
		writeError(w, http.StatusBadRequest, "verification token is required")
		return
	}

	userID, err := r.authEmailSvc.VerifyEmail(body.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired verification token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "verified",
		"user_id": userID,
	})
}

// handleLoginEmail authenticates a SaaS user with email + password.
// POST /api/v1/auth/login
func (r *Router) handleLoginEmail(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	userID, jwtToken, err := r.authEmailSvc.Login(body.Email, body.Password)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidPassword):
			writeError(w, http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, services.ErrEmailNotVerified):
			writeError(w, http.StatusForbidden, "email not verified")
		default:
			writeError(w, http.StatusInternalServerError, "login failed")
		}
		return
	}

	setSessionCookie(w, jwtToken)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": userID,
		"email":   body.Email,
	})
}

// handleForgotPassword initiates the password reset flow.
// POST /api/v1/auth/forgot-password
func (r *Router) handleForgotPassword(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	token, err := r.authEmailSvc.IssueResetToken(body.Email)
	if err != nil {
		// Always return 200 to avoid email enumeration. The reset token
		// is returned in the response body so the caller can use it for
		// testing; in production it would be sent via email only.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message": "if the email is registered, a reset link has been sent",
		})
		return
	}

	// TODO(FASE 2.2): Send reset token via email (Mailgun/SES).
	// For dev/test the token is returned in the response body.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "if the email is registered, a reset link has been sent",
		"reset_token": token,
	})
}

// handleResetPassword completes the password reset flow.
// POST /api/v1/auth/reset-password
func (r *Router) handleResetPassword(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Token == "" || body.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "token and new_password are required")
		return
	}

	if err := r.authEmailSvc.ResetPassword(body.Token, body.NewPassword); err != nil {
		switch {
		case errors.Is(err, services.ErrPasswordTooShort) || errors.Is(err, services.ErrPasswordNoDigit):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "password reset successfully",
	})
}

// -----------------------------------------------------------------------
//  Helpers
// -----------------------------------------------------------------------

// setSessionCookie writes the HttpOnly session cookie with a 7-day MaxAge
// matching the JWT TTL.
func setSessionCookie(w http.ResponseWriter, jwtToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    jwtToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   7 * 24 * 3600, // 7 days, matching JWT TTL
	})
}
