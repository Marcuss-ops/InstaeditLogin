-- =============================================================================
-- Migration 111: keep group memberships usable by workspace-scoped flows
-- =============================================================================
-- group_accounts is the group projection, while editor/livestream flows
-- authorize the same account through workspace_channels. Older repair scripts
-- populated only group_accounts, leaving those accounts apparently unlinked.
-- Backfill the binding for every existing group membership. The statement is
-- idempotent and preserves an operator's explicit disabled flag on existing
-- bindings.

INSERT INTO workspace_channels (workspace_id, platform_account_id, group_name, enabled)
SELECT g.workspace_id, ga.account_id, g.name, TRUE
  FROM groups g
  JOIN group_accounts ga ON ga.group_id = g.id
 WHERE g.workspace_id IS NOT NULL
ON CONFLICT (workspace_id, platform_account_id)
DO UPDATE SET group_name = EXCLUDED.group_name;
