-- 030_drop_magic_link_tokens.sql
--
-- Magic-link authentication was removed in the invite-only beta
-- (see auth_service.go: IssueVerificationToken / verify route / etc.).
-- This migration drops the now-unused magic_link_tokens table and
-- its supporting indexes. Safe to run after the latest deploy that
-- shipped the backend cleanup; roll forward, never back.

DROP INDEX IF EXISTS idx_magic_link_tokens_email;
DROP INDEX IF EXISTS idx_magic_link_tokens_expires;
DROP TABLE IF EXISTS magic_link_tokens;
