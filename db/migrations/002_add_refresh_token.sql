-- Migration 002 — reference copy
--
-- Il sorgente autoritativo è in internal/database/migrations/002_add_refresh_token.sql
-- (embeddato nel binario Go e applicato automaticamente da db.Migrate).
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS encrypted_refresh_token BYTEA;
