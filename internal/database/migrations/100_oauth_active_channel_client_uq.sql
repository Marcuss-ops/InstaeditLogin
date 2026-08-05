-- =============================================================================
-- 100_oauth_active_channel_client_uq.sql
--
-- YouTube OAuth Client Pool — anti-duplicate active-grant invariant.
--
-- Enforces "one ACTIVE oauth_connections row per (provider, channel,
-- pool client)": a channel must never hold two live grants on the same
-- pool client. Each live grant owns its own refresh token, so a
-- duplicate would both (a) count twice against Google's 100-refresh-
-- token cap per (account, client) pair and (b) recreate the "one
-- channel → five old connections → five active refresh tokens"
-- failure mode from the pool plan.
--
-- The oauth_client_key column itself was added by migration 099
-- (TEXT NOT NULL DEFAULT 'youtube_pool_a'); this migration only adds
-- the constraint on top of it.
--
-- Scope: the partial index applies ONLY to rows in status='active', so
-- the reconnect flow can move a grant through non-active states
-- (pending_authorization, reauth_required, disconnected) without
-- tripping the constraint — only the final active grant wins. A
-- reconnect that re-activates the same (channel, client) pair updates
-- the existing active row instead of creating a second one.
--
-- NOTE (multi-tenant): the key deliberately omits user_id. Two
-- different users connecting the SAME channel via two different Google
-- accounts (both managing the channel) would collide here; that case
-- is a deliberate no-go for the pool (the channel belongs to one grant
-- per client) — reconcile by disconnecting the stale side first.
-- =============================================================================

CREATE UNIQUE INDEX IF NOT EXISTS oauth_active_channel_client_uq
    ON oauth_connections (provider, provider_resource_id, oauth_client_key)
    WHERE status = 'active';

COMMENT ON INDEX oauth_active_channel_client_uq IS
    'One active grant per (provider, channel/resource, pool client). Duplicate active grants for the same channel+client would double-count refresh tokens against Google''s 100-token cap and recreate the reconnect-duplication failure mode.';
