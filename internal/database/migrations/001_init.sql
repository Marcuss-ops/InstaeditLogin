-- Core users table (platform-agnostic)
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         VARCHAR(255),
    name          VARCHAR(255),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Platform accounts link users to their social profiles
CREATE TABLE IF NOT EXISTS platform_accounts (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform         VARCHAR(50) NOT NULL,
    platform_user_id VARCHAR(255) NOT NULL,
    username         VARCHAR(255),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(platform, platform_user_id)
);

-- Encrypted OAuth tokens, scoped to a platform account
CREATE TABLE IF NOT EXISTS tokens (
    id                  BIGSERIAL PRIMARY KEY,
    platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
    token_type          VARCHAR(50) NOT NULL,
    encrypted_token     BYTEA NOT NULL,
    expires_at          TIMESTAMPTZ,
    scopes              TEXT[],
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_tokens_platform_account_id ON tokens(platform_account_id);
CREATE INDEX IF NOT EXISTS idx_platform_accounts_user_id ON platform_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_platform_accounts_platform ON platform_accounts(platform);
