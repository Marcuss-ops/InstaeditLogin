-- SPRINT 5.3 (P1#11): encryption key rotation.
--
-- The encrypted_token / encrypted_refresh_token columns carry
-- self-describing envelopes (see internal/crypto/encrypt.go:
-- 1 byte envelope version + 4 byte key id + 12 byte nonce + AEAD
-- ciphertext). The new key_version column is a fast-path filter
-- for the rotation worker (internal/worker/key_rotation_worker.go)
-- — it lets the worker do
--
--   SELECT id, encrypted_token, encrypted_refresh_token, key_version
--   FROM tokens
--   WHERE key_version < $1 OR key_version IS NULL
--   ORDER BY id ASC
--   LIMIT $2
--   FOR UPDATE SKIP LOCKED
--
-- without parsing every envelope byte-by-byte. The envelope itself
-- is the source of truth (the column is a denormalised copy) — the
-- worker re-stamps the column when it re-encrypts.
--
-- Nullable for backward-compat: legacy rows (pre-Sprint 5.3) were
-- written without the envelope prefix and without key_version. The
-- rotation worker picks them up via `key_version IS NULL` and
-- re-encrypts them in-place, stamping the new key_version. Until
-- the worker processes them, the encryptor's Decrypt() transparently
-- falls back to the legacy format using LegacyKeyID (key 1).
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS key_version INT DEFAULT NULL;

-- Partial index speeds up the rotation worker's filter. The full
-- table is unindexed (only the partial IS NOT NULL entries are
-- interesting to the worker; NULL rows are only ever scanned in
-- the initial migration sweep).
CREATE INDEX IF NOT EXISTS idx_tokens_key_version
    ON tokens(key_version)
    WHERE key_version IS NOT NULL;
