CREATE TABLE IF NOT EXISTS one_time_codes (
    code_hash BYTEA PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    username TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_one_time_codes_expires_at
    ON one_time_codes(expires_at);
