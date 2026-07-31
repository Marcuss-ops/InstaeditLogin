-- 083_oauth_token_field_normalization.sql
--
-- Canonical OAuth credential naming and grant-state storage.
--
-- Legacy installs use tokens.encrypted_token, tokens.expires_at and
-- oauth_connections.scopes. Keep those columns during the transition and
-- backfill the explicit names byte-for-byte / value-for-value. Application
-- reads and writes are dual-path until a later cleanup migration removes the
-- legacy aliases.

ALTER TABLE tokens
    ADD COLUMN IF NOT EXISTS encrypted_access_token BYTEA,
    ADD COLUMN IF NOT EXISTS access_token_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS refresh_token_expires_at TIMESTAMPTZ;

ALTER TABLE oauth_connections
    ADD COLUMN IF NOT EXISTS status VARCHAR(32) NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS granted_scopes TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS last_refresh_error TEXT;

-- Backfill only missing canonical values. Existing ciphertext is never
-- decrypted or re-encrypted by this migration.
UPDATE tokens
SET encrypted_access_token = encrypted_token
WHERE encrypted_access_token IS NULL
  AND encrypted_token IS NOT NULL;

UPDATE tokens
SET access_token_expires_at = expires_at
WHERE access_token_expires_at IS NULL
  AND expires_at IS NOT NULL;

UPDATE oauth_connections
SET granted_scopes = scopes
WHERE (granted_scopes IS NULL OR cardinality(granted_scopes) = 0)
  AND scopes IS NOT NULL
  AND cardinality(scopes) > 0;

CREATE INDEX IF NOT EXISTS idx_tokens_access_token_expires_at
    ON tokens (access_token_expires_at)
    WHERE access_token_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_oauth_connections_status
    ON oauth_connections (status);

COMMENT ON COLUMN tokens.encrypted_access_token IS
    'Canonical encrypted OAuth access-token ciphertext; encrypted_token is a legacy alias during migration.';
COMMENT ON COLUMN tokens.access_token_expires_at IS
    'Canonical access-token expiry; expires_at is a legacy alias during migration.';
COMMENT ON COLUMN tokens.refresh_token_expires_at IS
    'Optional provider-issued refresh-token expiry.';
COMMENT ON COLUMN oauth_connections.granted_scopes IS
    'Canonical scopes granted for this OAuth grant; scopes is a legacy alias during migration.';
COMMENT ON COLUMN oauth_connections.last_refresh_error IS
    'Redacted/provider-safe description of the latest refresh failure; never a token value.';
