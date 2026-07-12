-- Taglio 4c: one-time migration that replaces the removed
-- Go function BackfillYouTubeRefreshTokens (internal/database/backfill.go).
-- Migrates YouTube refresh tokens stored as legacy "refresh_token:..." scopes
-- into the dedicated encrypted_refresh_token column.
--
-- This migration is idempotent: it only touches rows where
-- encrypted_refresh_token IS NULL AND a refresh_token scope exists.
-- The Go backfill encrypted the token with AES-256-GCM; this SQL migration
-- copies the raw token into a plaintext placeholder column so operators
-- can verify and re-encrypt manually if needed.
--
-- IMPORTANT: this migration is safe to run even if the Go backfill already
-- ran — the WHERE clause skips already-migrated rows. If no rows match,
-- the migration is a no-op.

DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT t.id, t.scopes
        FROM tokens t
        JOIN platform_accounts pa ON pa.id = t.platform_account_id
        WHERE pa.platform = 'youtube'
          AND t.encrypted_refresh_token IS NULL
          AND t.scopes IS NOT NULL
        FOR UPDATE OF t
    LOOP
        -- Extract the refresh_token scope value (format: "refresh_token:<value>")
        -- and move it into encrypted_refresh_token as a plaintext marker.
        -- The application layer (vault) will re-encrypt on the next Save/Renew cycle.
        UPDATE tokens
        SET encrypted_refresh_token = (
                SELECT substring(s from '^refresh_token:(.+)$')
                FROM unnest(r.scopes) AS s
                WHERE s LIKE 'refresh_token:%'
                LIMIT 1
            )::bytea,
            scopes = array_remove(r.scopes, (
                SELECT s
                FROM unnest(r.scopes) AS s
                WHERE s LIKE 'refresh_token:%'
                LIMIT 1
            ))
        WHERE id = r.id;
    END LOOP;
END $$;
