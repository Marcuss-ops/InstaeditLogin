CREATE TABLE IF NOT EXISTS connect_link_nonces (
    nonce VARCHAR(32) PRIMARY KEY,
    expected_channel_id TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_connect_link_nonces_expires_at
    ON connect_link_nonces(expires_at);
