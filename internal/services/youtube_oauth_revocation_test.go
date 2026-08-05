package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestYouTubeRevokeGrant_SendsRefreshTokenAndAcceptsOK(t *testing.T) {
	const token = "test-refresh-token-never-log-this"
	var gotToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("content type: got %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request body: %v", err)
		}
		gotToken = values.Get("token")
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestYouTubeService(srv).RevokeGrant(context.Background(), token); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if gotToken != token {
		t.Fatalf("request token: got %q, want %q", gotToken, token)
	}
}

func TestYouTubeRevokeGrant_InvalidTokenIsIdempotentAndRedacted(t *testing.T) {
	const token = "secret-refresh-token-must-not-escape"
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_token","error_description":"`+token+`"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestYouTubeService(srv).RevokeGrant(context.Background(), token)
	if !errors.Is(err, OAuthGrantRevocationAlreadyCompleted) {
		t.Fatalf("want idempotent already-completed error, got %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("revocation error exposed token: %q", err)
	}
}

func TestYouTubeRevokeGrant_InvalidClientIsPermanentAndDoesNotDeleteSemantically(t *testing.T) {
	const token = "invalid-client-secret-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_client","error_description":"`+token+`"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestYouTubeService(srv).RevokeGrant(context.Background(), token)
	var revocationErr *OAuthGrantRevocationError
	if !errors.As(err, &revocationErr) || revocationErr.IsTransient() {
		t.Fatalf("want permanent OAuthGrantRevocationError, got %T: %v", err, err)
	}
	if errors.Is(err, OAuthGrantRevocationAlreadyCompleted) {
		t.Fatal("invalid_client must not be treated as already revoked")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("permanent revocation error exposed token: %q", err)
	}
}

func TestYouTubeRevokeGrant_TransientFailureIsTypedAndRetryable(t *testing.T) {
	const token = "transient-secret-refresh-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestYouTubeService(srv).RevokeGrant(context.Background(), token)
	var revocationErr *OAuthGrantRevocationError
	if !errors.As(err, &revocationErr) || !revocationErr.IsTransient() {
		t.Fatalf("want transient OAuthGrantRevocationError, got %T: %v", err, err)
	}
	if revocationErr.RetryAfter != 7*time.Second {
		t.Fatalf("retry-after: got %s, want 7s", revocationErr.RetryAfter)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transient revocation error exposed token: %q", err)
	}
}

func TestYouTubeRevokeGrant_TransportFailureIsTransientAndRedacted(t *testing.T) {
	const token = "transport-secret-refresh-token"
	svc := newTestYouTubeService(nil)

	err := svc.RevokeGrant(context.Background(), token)
	var revocationErr *OAuthGrantRevocationError
	if !errors.As(err, &revocationErr) || !revocationErr.IsTransient() {
		t.Fatalf("want transient transport revocation error, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport revocation error exposed token: %q", err)
	}
}
