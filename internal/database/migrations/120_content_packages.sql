-- Content Package domain.
--
-- A content package is the user-editable aggregate. Posts/upload_jobs remain
-- the execution engine; these tables hold the durable product-level intent
-- that is frozen only when preparation begins.

CREATE TABLE IF NOT EXISTS content_packages (
    id                          BIGSERIAL PRIMARY KEY,
    workspace_id                BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_by                  BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source_type                 TEXT NOT NULL DEFAULT 'google_drive',
    drive_account_id            BIGINT REFERENCES platform_accounts(id) ON DELETE RESTRICT,
    drive_file_id               TEXT NOT NULL,
    source_filename              TEXT NOT NULL DEFAULT '',
    source_fingerprint          TEXT NOT NULL DEFAULT '',
    velox_project_id            TEXT,
    source_language              TEXT NOT NULL DEFAULT 'it',
    current_metadata_revision_id BIGINT,
    current_cover_media_id      TEXT,
    state                       TEXT NOT NULL DEFAULT 'draft',
    version                     BIGINT NOT NULL DEFAULT 1,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT content_packages_source_type_ck
        CHECK (btrim(source_type) <> ''),
    CONSTRAINT content_packages_drive_file_ck
        CHECK (btrim(drive_file_id) <> ''),
    CONSTRAINT content_packages_language_ck
        CHECK (btrim(source_language) <> ''),
    CONSTRAINT content_packages_state_ck
        CHECK (state IN ('draft','ready','scheduled','preparing','ready_to_publish','publishing','partially_published','published','blocked')),
    CONSTRAINT content_packages_version_ck CHECK (version > 0),
    CONSTRAINT content_packages_drive_account_uq
        UNIQUE (workspace_id, source_type, drive_account_id, drive_file_id)
);

CREATE INDEX IF NOT EXISTS content_packages_workspace_updated_idx
    ON content_packages (workspace_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS content_metadata_revisions (
    id                  BIGSERIAL PRIMARY KEY,
    content_package_id  BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    revision_number     BIGINT NOT NULL,
    source_language     TEXT NOT NULL,
    title               TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    tags                JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by          BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT content_metadata_revisions_number_ck CHECK (revision_number > 0),
    CONSTRAINT content_metadata_revisions_uq UNIQUE (content_package_id, revision_number)
);

CREATE INDEX IF NOT EXISTS content_metadata_revisions_package_idx
    ON content_metadata_revisions (content_package_id, revision_number DESC);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conname = 'content_packages_current_revision_fk'
           AND conrelid = 'content_packages'::regclass
    ) THEN
        ALTER TABLE content_packages
            ADD CONSTRAINT content_packages_current_revision_fk
            FOREIGN KEY (current_metadata_revision_id)
            REFERENCES content_metadata_revisions(id) ON DELETE RESTRICT;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS content_package_targets (
    id                  BIGSERIAL PRIMARY KEY,
    content_package_id  BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE RESTRICT,
    language            TEXT NOT NULL,
    privacy_status      TEXT NOT NULL DEFAULT 'private',
    playlist_id         TEXT,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT content_package_targets_language_ck CHECK (btrim(language) <> ''),
    CONSTRAINT content_package_targets_privacy_ck CHECK (privacy_status IN ('public','unlisted','private')),
    CONSTRAINT content_package_targets_uq UNIQUE (content_package_id, platform_account_id)
);

CREATE INDEX IF NOT EXISTS content_package_targets_package_idx
    ON content_package_targets (content_package_id, enabled, id);

CREATE TABLE IF NOT EXISTS translation_bundles (
    id                       BIGSERIAL PRIMARY KEY,
    content_package_id       BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    source_metadata_revision_id BIGINT NOT NULL REFERENCES content_metadata_revisions(id) ON DELETE RESTRICT,
    provider                 TEXT NOT NULL DEFAULT 'nvidia',
    status                   TEXT NOT NULL DEFAULT 'pending',
    requested_languages      TEXT[] NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at             TIMESTAMPTZ,
    CONSTRAINT translation_bundles_provider_ck CHECK (btrim(provider) <> ''),
    CONSTRAINT translation_bundles_status_ck CHECK (status IN ('pending','processing','completed','stale','failed'))
);

CREATE INDEX IF NOT EXISTS translation_bundles_lookup_idx
    ON translation_bundles (content_package_id, source_metadata_revision_id, status, id DESC);

CREATE TABLE IF NOT EXISTS translation_entries (
    id                  BIGSERIAL PRIMARY KEY,
    bundle_id           BIGINT NOT NULL REFERENCES translation_bundles(id) ON DELETE CASCADE,
    language            TEXT NOT NULL,
    title               TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    tags                JSONB NOT NULL DEFAULT '[]'::jsonb,
    origin              TEXT NOT NULL DEFAULT 'nvidia',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT translation_entries_language_ck CHECK (btrim(language) <> ''),
    CONSTRAINT translation_entries_origin_ck CHECK (origin IN ('source','nvidia','manual')),
    CONSTRAINT translation_entries_uq UNIQUE (bundle_id, language)
);

CREATE TABLE IF NOT EXISTS content_schedules (
    id                  BIGSERIAL PRIMARY KEY,
    content_package_id  BIGINT NOT NULL UNIQUE REFERENCES content_packages(id) ON DELETE CASCADE,
    scheduled_at        TIMESTAMPTZ NOT NULL,
    prepare_at          TIMESTAMPTZ NOT NULL,
    timezone            TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'scheduled',
    package_version     BIGINT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT content_schedules_window_ck CHECK (prepare_at < scheduled_at),
    CONSTRAINT content_schedules_status_ck CHECK (status IN ('scheduled','preparing','ready_to_publish','publishing','published','cancelled','blocked','failed'))
);

CREATE INDEX IF NOT EXISTS content_schedules_prepare_idx
    ON content_schedules (prepare_at, status, id);

CREATE TABLE IF NOT EXISTS publish_snapshots (
    id                       BIGSERIAL PRIMARY KEY,
    content_schedule_id      BIGINT NOT NULL REFERENCES content_schedules(id) ON DELETE CASCADE,
    content_package_id       BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    package_version          BIGINT NOT NULL,
    target_account_id        BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE RESTRICT,
    language                 TEXT NOT NULL,
    metadata_revision_id     BIGINT NOT NULL REFERENCES content_metadata_revisions(id) ON DELETE RESTRICT,
    translation_bundle_id    BIGINT REFERENCES translation_bundles(id) ON DELETE RESTRICT,
    cover_media_id           TEXT,
    source_media_asset_id    TEXT,
    title                    TEXT NOT NULL DEFAULT '',
    description              TEXT NOT NULL DEFAULT '',
    tags                     JSONB NOT NULL DEFAULT '[]'::jsonb,
    privacy_status           TEXT NOT NULL,
    publish_at               TIMESTAMPTZ NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT publish_snapshots_privacy_ck CHECK (privacy_status IN ('public','unlisted','private')),
    CONSTRAINT publish_snapshots_uq UNIQUE (content_schedule_id, target_account_id)
);

CREATE INDEX IF NOT EXISTS publish_snapshots_package_idx
    ON publish_snapshots (content_package_id, content_schedule_id, target_account_id);

CREATE TABLE IF NOT EXISTS publication_events (
    id                       BIGSERIAL PRIMARY KEY,
    content_package_id       BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    content_schedule_id      BIGINT REFERENCES content_schedules(id) ON DELETE CASCADE,
    target_publication_id    BIGINT,
    stage                    TEXT NOT NULL,
    event_type               TEXT NOT NULL,
    attempt_no               INTEGER NOT NULL DEFAULT 0,
    error_code               TEXT,
    message                  TEXT,
    occurred_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS publication_events_package_idx
    ON publication_events (content_package_id, occurred_at DESC, id DESC);

-- Drive Inbox discovery is intentionally metadata-only. It never owns a
-- download lease and never creates an UploadJob until the user claims an item.
CREATE TABLE IF NOT EXISTS drive_inboxes (
    id                  BIGSERIAL PRIMARY KEY,
    workspace_id        BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    drive_account_id    BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE RESTRICT,
    folder_id           TEXT NOT NULL,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    last_scan_at        TIMESTAMPTZ,
    cursor              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT drive_inboxes_folder_ck CHECK (btrim(folder_id) <> ''),
    CONSTRAINT drive_inboxes_uq UNIQUE (workspace_id, drive_account_id, folder_id)
);

CREATE TABLE IF NOT EXISTS drive_inbox_items (
    id                  BIGSERIAL PRIMARY KEY,
    inbox_id            BIGINT NOT NULL REFERENCES drive_inboxes(id) ON DELETE CASCADE,
    drive_file_id       TEXT NOT NULL,
    filename            TEXT NOT NULL DEFAULT '',
    mime_type           TEXT NOT NULL DEFAULT '',
    size_bytes          BIGINT,
    modified_time       TIMESTAMPTZ,
    fingerprint         TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'detected',
    content_package_id  BIGINT REFERENCES content_packages(id) ON DELETE SET NULL,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT drive_inbox_items_status_ck CHECK (status IN ('detected','ready_for_review','claimed','ignored','missing','error')),
    CONSTRAINT drive_inbox_items_uq UNIQUE (inbox_id, drive_file_id)
);

CREATE INDEX IF NOT EXISTS drive_inbox_items_review_idx
    ON drive_inbox_items (inbox_id, status, last_seen_at DESC, id DESC);
