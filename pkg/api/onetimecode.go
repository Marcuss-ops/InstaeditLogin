// Package api — one-time code store used by Taglio 1.2 to bridge the OAuth
// callback and the HttpOnly session cookie.
//
// Flow:
//  1. handleCallback (OAuth success) calls Store.ExchangePayload with the
//     freshly-issued user identity, gets back a 32-char base64 code, and
//     redirects the browser to /auth/callback?code=<code>&provider=....
//     The JWT never lands in the URL.
//  2. The SPA's /auth/callback page POSTs the code to
//     /api/v1/auth/exchange. The exchange handler calls Consume, which
//     atomically deletes the entry on read. On success, the handler sets
//     a `session` HttpOnly Secure SameSite=None cookie carrying the JWT
//     and returns 204.
//  3. The SPA's subsequent fetch()es carry the cookie via
//     `credentials: "include"`. The auth.Middleware (in internal/auth/jwt.go)
//     falls back to the cookie when no Authorization: Bearer header is set.
//
// Security:
//   - Codes are 32 bytes from crypto/rand → 43 base64url chars (same
//     generator as the OAuth state in handlers.go).
//   - Consume is single-use: the entry is deleted atomically. An attacker
//     who replays a captured code (e.g. via Referer leakage) gets nothing
//     on the second attempt because the entry is already gone.
//   - TTL is 60s. Combined with single-use, the window for a successful
//     replay is one request, one second, and one bot.
package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// ExchangePayload is the identity bound to a one-time code. It is consumed
// once and never returned to a second caller.
type ExchangePayload struct {
	UserID    int64
	Name      string
	Username  string
	ExpiresAt time.Time
}

// ErrCodeNotFound is returned by Consume when the code is unknown,
// already consumed, or expired. The handler maps this to 401.
var ErrCodeNotFound = errors.New("one-time code not found or expired")

// OneTimeCodeStore is the persistence contract for OAuth-callback
// exchange codes. Production wiring injects a PostgreSQL-backed
// implementation; tests inject the in-memory implementation below.
type OneTimeCodeStore interface {
	Generate(payload ExchangePayload) (string, error)
	Consume(code string) (ExchangePayload, error)
	// Stop halts any background goroutine owned by the store.
	Stop()
}

// InMemoryOneTimeCodeStore is the previous in-memory implementation,
// kept for tests and local dev. It is no longer wired automatically by
// NewRouter — callers must explicitly pass it via WithOneTimeCodeStore.
type InMemoryOneTimeCodeStore struct {
	mu       sync.Mutex
	entries  map[string]inMemoryOneTimeCodeEntry
	ttl      time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

type inMemoryOneTimeCodeEntry struct {
	payload  ExchangePayload
	expireAt time.Time
}

// NewInMemoryOneTimeCodeStore constructs an in-memory store with the given
// TTL and starts the background sweeper. ttl <= 0 falls back to 60s.
func NewInMemoryOneTimeCodeStore(ttl time.Duration) *InMemoryOneTimeCodeStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	s := &InMemoryOneTimeCodeStore{
		entries: make(map[string]inMemoryOneTimeCodeEntry),
		ttl:     ttl,
		stop:    make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// Stop halts the background sweeper. Idempotent.
func (s *InMemoryOneTimeCodeStore) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Generate stores payload under a fresh random code and returns the code.
func (s *InMemoryOneTimeCodeStore) Generate(payload ExchangePayload) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[code] = inMemoryOneTimeCodeEntry{
		payload:  payload,
		expireAt: time.Now().Add(s.ttl),
	}
	return code, nil
}

// Consume atomically reads and deletes the entry for code.
func (s *InMemoryOneTimeCodeStore) Consume(code string) (ExchangePayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[code]
	if !ok {
		return ExchangePayload{}, ErrCodeNotFound
	}
	delete(s.entries, code)
	if time.Now().After(entry.expireAt) {
		return ExchangePayload{}, ErrCodeNotFound
	}
	return entry.payload, nil
}

func (s *InMemoryOneTimeCodeStore) sweepLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for code, entry := range s.entries {
				if now.After(entry.expireAt) {
					delete(s.entries, code)
				}
			}
			s.mu.Unlock()
		}
	}
}
