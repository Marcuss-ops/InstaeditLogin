package api

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"sync"
	"time"
)

// OneTimeCodePostgresStore is the production OneTimeCodeStore backed by
// PostgreSQL. Tokens are never stored in the clear: only a SHA-256 hash
// of the raw 43-char base64url code is persisted. Consume deletes the
// row atomically with DELETE ... RETURNING, so the same code cannot be
// consumed twice, even across replicas.
type OneTimeCodePostgresStore struct {
	db  *sql.DB
	ttl time.Duration
	stop chan struct{}
	stopOnce sync.Once
}

// NewOneTimeCodePostgresStore constructs a PostgreSQL-backed store. ttl <= 0
// falls back to 60s. The returned store starts a background sweeper that
// removes expired rows once per minute.
func NewOneTimeCodePostgresStore(db *sql.DB, ttl time.Duration) *OneTimeCodePostgresStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	s := &OneTimeCodePostgresStore{
		db:   db,
		ttl:  ttl,
		stop: make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// Stop halts the background sweeper.
func (s *OneTimeCodePostgresStore) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *OneTimeCodePostgresStore) Generate(payload ExchangePayload) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	hash := hashCode(code)
	expiresAt := time.Now().Add(s.ttl)

	_, err := s.db.Exec(`
		INSERT INTO one_time_codes (code_hash, user_id, name, username, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		hash, payload.UserID, payload.Name, payload.Username, expiresAt,
	)
	if err != nil {
		return "", err
	}
	return code, nil
}

func (s *OneTimeCodePostgresStore) Consume(code string) (ExchangePayload, error) {
	var payload ExchangePayload
	err := s.db.QueryRow(`
		DELETE FROM one_time_codes
		WHERE code_hash = $1 AND expires_at > NOW()
		RETURNING user_id, name, username, expires_at`,
		hashCode(code),
	).Scan(&payload.UserID, &payload.Name, &payload.Username, &payload.ExpiresAt)

	if err == sql.ErrNoRows {
		return ExchangePayload{}, ErrCodeNotFound
	}
	return payload, err
}

func (s *OneTimeCodePostgresStore) sweepLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			_, _ = s.db.Exec(`DELETE FROM one_time_codes WHERE expires_at <= NOW()`)
		}
	}
}

func hashCode(code string) []byte {
	h := sha256.Sum256([]byte(code))
	return h[:]
}
