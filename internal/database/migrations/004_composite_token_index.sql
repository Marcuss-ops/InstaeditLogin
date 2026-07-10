-- 004_composite_token_index.sql
-- Composite index on tokens(platform_account_id, token_type).
--
-- Use cases:
--   - Cleanup pruning during SaveToken (delete expired rows where
--     platform_account_id = X AND token_type = 'access')
--   - Refresh-token rotation lookups (find the refresh row for account X)
--
-- Postgres B-Tree left-prefix rule means this index also satisfies queries
-- that filter only on platform_account_id (so the older single-column
-- idx_tokens_platform_account_id from 001 becomes redundant for reads — but
-- we keep that existing index for now to avoid a boot flip-flop on every
-- re-run; dropping it is a future cleanup task).
CREATE INDEX IF NOT EXISTS idx_tokens_account_type
    ON tokens(platform_account_id, token_type);
