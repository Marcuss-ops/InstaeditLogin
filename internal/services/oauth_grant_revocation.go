package services

import (
	"errors"
	"fmt"
	"time"
)

// OAuthGrantRevocationClass describes whether a remote revocation failure is
// safe to retry without changing the request or credentials.
type OAuthGrantRevocationClass string

const (
	OAuthGrantRevocationTransient OAuthGrantRevocationClass = "transient"
	OAuthGrantRevocationPermanent OAuthGrantRevocationClass = "permanent"

	// OAuthGrantRevocationTimeout bounds the provider call while the local
	// grant transaction holds its row locks. Transient timeout failures leave
	// the transaction untouched and are explicitly retryable by the caller.
	OAuthGrantRevocationTimeout = 15 * time.Second
)

// OAuthGrantRevocationError is a redacted provider error. It deliberately
// never stores or formats the token used by the revocation request.
type OAuthGrantRevocationError struct {
	StatusCode int
	Code       string
	Class      OAuthGrantRevocationClass
	RetryAfter time.Duration
	Cause      error
}

func (e *OAuthGrantRevocationError) Error() string {
	if e == nil {
		return "oauth grant revocation failed"
	}
	if e.StatusCode > 0 {
		if e.Code != "" {
			return fmt.Sprintf("oauth grant revocation failed (status %d, code %s)", e.StatusCode, e.Code)
		}
		return fmt.Sprintf("oauth grant revocation failed (status %d)", e.StatusCode)
	}
	return "oauth grant revocation failed (transport error)"
}

func (e *OAuthGrantRevocationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *OAuthGrantRevocationError) IsTransient() bool {
	return e != nil && e.Class == OAuthGrantRevocationTransient
}

// OAuthGrantRevocationAlreadyCompleted marks an invalid-token response as an
// idempotent success: the provider no longer accepts the grant, so local
// cleanup may safely proceed.
var OAuthGrantRevocationAlreadyCompleted = errors.New("oauth grant was already revoked")
