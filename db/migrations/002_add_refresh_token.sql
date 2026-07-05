-- Migration 002: Add encrypted_refresh_token column to tokens
-- Run on Supabase if you paste migrations manually (otherwise applied automatically by db.Migrate)
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS encrypted_refresh_token BYTEA;
