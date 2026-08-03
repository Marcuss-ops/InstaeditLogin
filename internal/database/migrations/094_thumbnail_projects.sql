-- 094_thumbnail_projects.sql
-- ThumbnailProjectModule — autonomous, workspace-scoped thumbnail editing.
--
-- Domain separation:
--   thumbnail_projects          editable graphic projects
--   thumbnail_project_revisions immutable canvas snapshots
--   thumbnail_project_assets     project-to-media references
--   thumbnail_exports            rendered, verifiable files
--   thumbnail_assignments        optional YouTube destinations
--
-- A project deliberately contains no platform_account_id,
-- youtube_video_id, oauth_connection_id, token, or provider state. YouTube
-- enters only through thumbnail_assignments after an export exists.
--
-- Migration contract:
--   * This repository has forward-only migrations. There is no executable
--     DOWN file: the migration runner wraps this entire body and its
--     schema_migrations INSERT in one transaction. Any statement failure
--     rolls back every table, constraint, index, and row created here.
--   * Every additive DDL statement is replay-safe so the SQL remains safe
--     for operator verification and test fixtures that execute a body twice.
--   * A production rollback means restore the database snapshot taken before
--     deployment, or run an explicitly reviewed manual DROP in a maintenance
--     window. Do not edit an applied migration: the runner verifies SHA-256
--     checksums and will reject modified history.
--
-- IDs are TEXT because project/revision/export IDs are opaque application
-- identifiers. media_assets.id is UUID in the existing schema, so media
-- references use UUID to retain a real foreign key and prevent orphaned
-- project assets/exports.

-- -------------------------------------------------------------------------
-- Projects
-- -------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS thumbnail_projects (
    id                  TEXT PRIMARY KEY,
    workspace_id        BIGINT NOT NULL
                        REFERENCES workspaces(id) ON DELETE CASCADE,
    created_by          BIGINT NOT NULL
                        REFERENCES users(id) ON DELETE RESTRICT,

    name                TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    canvas_width        INTEGER NOT NULL,
    canvas_height       INTEGER NOT NULL,

    status              TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'ready', 'archived', 'deleted')),

    current_revision_id TEXT,
    preview_media_id    UUID
                        REFERENCES media_assets(id) ON DELETE SET NULL,
    latest_export_id    TEXT,

    -- Optimistic concurrency token. The API increments this when it
    -- successfully appends a new revision; it is never allowed to be 0.
    version             BIGINT NOT NULL DEFAULT 1
                        CHECK (version >= 1),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT thumbnail_projects_id_nonempty_ck
        CHECK (btrim(id) <> ''),
    CONSTRAINT thumbnail_projects_name_nonempty_ck
        CHECK (btrim(name) <> ''),
    CONSTRAINT thumbnail_projects_canvas_width_ck
        CHECK (canvas_width > 0 AND canvas_width <= 16384),
    CONSTRAINT thumbnail_projects_canvas_height_ck
        CHECK (canvas_height > 0 AND canvas_height <= 16384),
    CONSTRAINT thumbnail_projects_workspace_id_id_uq
        UNIQUE (workspace_id, id)
);

CREATE INDEX IF NOT EXISTS thumbnail_projects_workspace_status_idx
    ON thumbnail_projects (workspace_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_projects_workspace_updated_idx
    ON thumbnail_projects (workspace_id, updated_at DESC);

-- -------------------------------------------------------------------------
-- Immutable project revisions
-- -------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS thumbnail_project_revisions (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL
                     REFERENCES thumbnail_projects(id) ON DELETE CASCADE,
    revision_number  BIGINT NOT NULL CHECK (revision_number >= 1),
    schema_version   INTEGER NOT NULL CHECK (schema_version >= 1),
    snapshot_json    JSONB NOT NULL
                     CHECK (jsonb_typeof(snapshot_json) = 'object'),
    snapshot_sha256  BYTEA NOT NULL
                     CHECK (octet_length(snapshot_sha256) = 32),
    renderer_version TEXT NOT NULL,
    created_by       BIGINT NOT NULL
                     REFERENCES users(id) ON DELETE RESTRICT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT thumbnail_project_revisions_id_nonempty_ck
        CHECK (btrim(id) <> ''),
    CONSTRAINT thumbnail_project_revisions_renderer_nonempty_ck
        CHECK (btrim(renderer_version) <> ''),
    CONSTRAINT thumbnail_project_revisions_project_revision_uq
        UNIQUE (project_id, revision_number),
    CONSTRAINT thumbnail_project_revisions_project_hash_uq
        UNIQUE (project_id, snapshot_sha256),
    CONSTRAINT thumbnail_project_revisions_project_id_id_uq
        UNIQUE (project_id, id)
);

CREATE INDEX IF NOT EXISTS thumbnail_project_revisions_project_created_idx
    ON thumbnail_project_revisions (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_project_revisions_project_revision_idx
    ON thumbnail_project_revisions (project_id, revision_number DESC);

-- -------------------------------------------------------------------------
-- Project asset references
-- -------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS thumbnail_project_assets (
    project_id  TEXT NOT NULL
                REFERENCES thumbnail_projects(id) ON DELETE CASCADE,
    media_id    UUID NOT NULL
                REFERENCES media_assets(id) ON DELETE RESTRICT,
    role        TEXT NOT NULL,
    object_id   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT thumbnail_project_assets_role_ck
        CHECK (role IN ('background', 'foreground', 'logo', 'overlay', 'reference', 'font')),
    CONSTRAINT thumbnail_project_assets_project_media_role_pk
        PRIMARY KEY (project_id, media_id, role)
);

CREATE INDEX IF NOT EXISTS thumbnail_project_assets_project_role_idx
    ON thumbnail_project_assets (project_id, role, created_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_project_assets_media_idx
    ON thumbnail_project_assets (media_id);

-- -------------------------------------------------------------------------
-- Rendered exports
-- -------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS thumbnail_exports (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL
                     REFERENCES thumbnail_projects(id) ON DELETE CASCADE,
    revision_id      TEXT NOT NULL
                     REFERENCES thumbnail_project_revisions(id) ON DELETE CASCADE,
    media_id         UUID NOT NULL
                     REFERENCES media_assets(id) ON DELETE RESTRICT,
    content_type     TEXT NOT NULL
                     CHECK (content_type IN ('image/png', 'image/jpeg')),
    width            INTEGER NOT NULL CHECK (width > 0 AND width <= 16384),
    height           INTEGER NOT NULL CHECK (height > 0 AND height <= 16384),
    file_size        BIGINT NOT NULL CHECK (file_size >= 0),
    sha256           BYTEA NOT NULL
                     CHECK (octet_length(sha256) = 32),
    renderer_version TEXT NOT NULL,
    status           TEXT NOT NULL
                     CHECK (status IN ('rendering', 'ready', 'failed')),
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT thumbnail_exports_id_nonempty_ck
        CHECK (btrim(id) <> ''),
    CONSTRAINT thumbnail_exports_renderer_nonempty_ck
        CHECK (btrim(renderer_version) <> ''),
    CONSTRAINT thumbnail_exports_project_id_id_uq
        UNIQUE (project_id, id)
);

CREATE INDEX IF NOT EXISTS thumbnail_exports_project_created_idx
    ON thumbnail_exports (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_exports_project_status_idx
    ON thumbnail_exports (project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_exports_revision_idx
    ON thumbnail_exports (revision_id);

-- -------------------------------------------------------------------------
-- Ensure the assignment's platform discriminator can participate in a
-- composite FK. platform_accounts.id is already a primary key, but PostgreSQL
-- requires a matching unique key for the (id, platform) reference below.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'platform_accounts_id_platform_uq'
           AND conrelid = 'platform_accounts'::regclass
    ) THEN
        ALTER TABLE platform_accounts
            ADD CONSTRAINT platform_accounts_id_platform_uq
            UNIQUE (id, platform);
    END IF;
END $$;

-- Optional destination assignments
-- -------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS thumbnail_assignments (
    id                  TEXT PRIMARY KEY,
    workspace_id        BIGINT NOT NULL
                        REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id          TEXT NOT NULL,
    export_id           TEXT NOT NULL,

    platform_account_id BIGINT NOT NULL,
    -- This module currently supports YouTube assignments only. Keeping the
    -- discriminator in the row lets the database enforce that the referenced
    -- account is actually a YouTube account through the composite FK below.
    platform            TEXT NOT NULL DEFAULT 'youtube'
                        CHECK (platform = 'youtube'),
    youtube_video_id    TEXT NOT NULL,
    target_language     TEXT,
    status              TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'pending', 'applied', 'failed', 'cancelled')),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT thumbnail_assignments_id_nonempty_ck
        CHECK (btrim(id) <> ''),
    CONSTRAINT thumbnail_assignments_video_nonempty_ck
        CHECK (btrim(youtube_video_id) <> ''),
    CONSTRAINT thumbnail_assignments_workspace_project_fk
        FOREIGN KEY (workspace_id, project_id)
        REFERENCES thumbnail_projects (workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT thumbnail_assignments_project_export_fk
        FOREIGN KEY (project_id, export_id)
        REFERENCES thumbnail_exports (project_id, id)
        ON DELETE CASCADE,
    CONSTRAINT thumbnail_assignments_workspace_account_fk
        FOREIGN KEY (workspace_id, platform_account_id)
        REFERENCES workspace_channels (workspace_id, platform_account_id)
        ON DELETE CASCADE,
    CONSTRAINT thumbnail_assignments_workspace_account_platform_fk
        FOREIGN KEY (platform_account_id, platform)
        REFERENCES platform_accounts (id, platform)
        ON DELETE CASCADE,
    CONSTRAINT thumbnail_assignments_export_account_video_uq
        UNIQUE (export_id, platform_account_id, youtube_video_id)
);

CREATE INDEX IF NOT EXISTS thumbnail_assignments_workspace_updated_idx
    ON thumbnail_assignments (workspace_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_assignments_project_idx
    ON thumbnail_assignments (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS thumbnail_assignments_account_video_idx
    ON thumbnail_assignments (platform_account_id, youtube_video_id);

-- -------------------------------------------------------------------------
-- Cross-reference FKs that depend on tables declared above
-- -------------------------------------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'thumbnail_projects_current_revision_fk'
           AND conrelid = 'thumbnail_projects'::regclass
    ) THEN
        ALTER TABLE thumbnail_projects
            ADD CONSTRAINT thumbnail_projects_current_revision_fk
            FOREIGN KEY (current_revision_id)
            REFERENCES thumbnail_project_revisions(id)
            ON DELETE SET NULL;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'thumbnail_projects_current_revision_same_project_fk'
           AND conrelid = 'thumbnail_projects'::regclass
    ) THEN
        ALTER TABLE thumbnail_projects
            ADD CONSTRAINT thumbnail_projects_current_revision_same_project_fk
            FOREIGN KEY (id, current_revision_id)
            REFERENCES thumbnail_project_revisions(project_id, id);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'thumbnail_projects_latest_export_fk'
           AND conrelid = 'thumbnail_projects'::regclass
    ) THEN
        ALTER TABLE thumbnail_projects
            ADD CONSTRAINT thumbnail_projects_latest_export_fk
            FOREIGN KEY (latest_export_id)
            REFERENCES thumbnail_exports(id)
            ON DELETE SET NULL;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'thumbnail_projects_latest_export_same_project_fk'
           AND conrelid = 'thumbnail_projects'::regclass
    ) THEN
        ALTER TABLE thumbnail_projects
            ADD CONSTRAINT thumbnail_projects_latest_export_same_project_fk
            FOREIGN KEY (id, latest_export_id)
            REFERENCES thumbnail_exports(project_id, id);
    END IF;
END $$;

-- Composite FKs enforce that pointers and assignments stay within the
-- same project/workspace without triggers: project/current_revision,
-- project/latest_export, assignment workspace/project, and project/export
-- must all match. The renderer invariant (an export's revision belongs to
-- that export's project) is represented by the composite FK below.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'thumbnail_exports_project_revision_fk'
           AND conrelid = 'thumbnail_exports'::regclass
    ) THEN
        ALTER TABLE thumbnail_exports
            ADD CONSTRAINT thumbnail_exports_project_revision_fk
            FOREIGN KEY (project_id, revision_id)
            REFERENCES thumbnail_project_revisions (project_id, id)
            ON DELETE CASCADE;
    END IF;
END $$;
