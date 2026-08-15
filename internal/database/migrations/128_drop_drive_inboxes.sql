-- 128_drop_drive_inboxes.sql
--
-- The Drive Inbox feature (metadata-only Google Drive folder discovery)
-- has been removed end-to-end: frontend page/route/sidebar entry, the
-- /api/v1/drive-inboxes/* HTTP handlers, the DriveInboxRepository, the
-- drive_inbox_scanner background worker and the DriveInbox/DriveInboxItem
-- models are all gone. Migration 120 created drive_inboxes and
-- drive_inbox_items; with no remaining reader or writer they are now
-- orphaned, so this migration drops them.
--
-- Order matters: drive_inbox_items holds the FK to drive_inboxes, so it
-- is dropped first. The standalone partial review index is dropped
-- explicitly for parity with the drop-table convention used elsewhere
-- (see 030_drop_magic_link_tokens.sql); DROP TABLE would remove it too,
-- so the explicit DROP INDEX is defensive, not required.
--
-- Roll forward, never back. Guarded with IF EXISTS so a database that
-- never created the tables (or already dropped them) stays a no-op.

DROP INDEX IF EXISTS drive_inbox_items_review_idx;

DROP TABLE IF EXISTS drive_inbox_items;
DROP TABLE IF EXISTS drive_inboxes;
