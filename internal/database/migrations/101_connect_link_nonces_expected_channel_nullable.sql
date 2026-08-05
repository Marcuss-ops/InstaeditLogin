-- connect_link_nonces.expected_channel_id becomes nullable.
--
-- The nonce store is shared by TWO flows:
--   1. admin connect-link  (POST /admin/channels/{id}/connect-link) — the
--      expected_channel_id is always present (the admin pins a channel);
--   2. YouTube OAuth Client Pool login  (GET /api/v1/auth/youtube/login with
--      a pool registry) — expected_channel_id is OPTIONAL. A generic
--      "add channel" flow does not know the channel id yet (it is revealed
--      after Google consent); channel disambiguation happens in the
--      callback (409 when channels.list(mine=true) is ambiguous).
-- The strict NOT NULL made the generic pool login fail with a 500
-- ("expected_channel_id is required") before the user even reached
-- Google's consent screen. Relaxing the column lets the pool flow issue
-- an unpinned nonce; the single-use / replay contract is unchanged.
ALTER TABLE connect_link_nonces
    ALTER COLUMN expected_channel_id DROP NOT NULL;
