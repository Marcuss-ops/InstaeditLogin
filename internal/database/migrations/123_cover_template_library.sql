-- 123_cover_template_library.sql
-- Cover Library is a read projection over ready thumbnail_exports.
-- Templates are versioned identities whose editable canvas remains in
-- InstaEditor; InstaEdit stores only opaque editor handles, slots and previews.

CREATE TABLE IF NOT EXISTS cover_templates (
    id                      BIGSERIAL PRIMARY KEY,
    workspace_id            BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_by              BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    category                TEXT NOT NULL DEFAULT '',
    language                TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','archived')),
    current_version_number  BIGINT NOT NULL DEFAULT 0 CHECK (current_version_number >= 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT cover_templates_name_ck CHECK (btrim(name) <> ''),
    CONSTRAINT cover_templates_language_ck CHECK (language = '' OR btrim(language) <> ''),
    CONSTRAINT cover_templates_workspace_name_uq UNIQUE (workspace_id, name)
);

CREATE INDEX IF NOT EXISTS cover_templates_workspace_status_idx
    ON cover_templates (workspace_id, status, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS cover_template_versions (
    id                BIGSERIAL PRIMARY KEY,
    template_id       BIGINT NOT NULL REFERENCES cover_templates(id) ON DELETE CASCADE,
    version_number    BIGINT NOT NULL CHECK (version_number > 0),
    editor_project_id TEXT NOT NULL,
    preview_media_id  UUID REFERENCES media_assets(id) ON DELETE SET NULL,
    slots             JSONB NOT NULL DEFAULT '{}'::jsonb
                      CHECK (jsonb_typeof(slots) = 'object'),
    created_by        BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT cover_template_versions_editor_project_ck CHECK (btrim(editor_project_id) <> ''),
    CONSTRAINT cover_template_versions_uq UNIQUE (template_id, version_number)
);

CREATE INDEX IF NOT EXISTS cover_template_versions_template_idx
    ON cover_template_versions (template_id, version_number DESC);

ALTER TABLE content_packages
    ADD COLUMN IF NOT EXISTS current_cover_template_version_id BIGINT;

ALTER TABLE content_package_targets
    ADD COLUMN IF NOT EXISTS cover_media_id TEXT,
    ADD COLUMN IF NOT EXISTS cover_template_version_id BIGINT;

ALTER TABLE publish_snapshots
    ADD COLUMN IF NOT EXISTS cover_template_version_id BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname='content_packages_cover_template_version_fk'
           AND conrelid='content_packages'::regclass
    ) THEN
        ALTER TABLE content_packages
            ADD CONSTRAINT content_packages_cover_template_version_fk
            FOREIGN KEY (current_cover_template_version_id)
            REFERENCES cover_template_versions(id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname='content_package_targets_cover_template_version_fk'
           AND conrelid='content_package_targets'::regclass
    ) THEN
        ALTER TABLE content_package_targets
            ADD CONSTRAINT content_package_targets_cover_template_version_fk
            FOREIGN KEY (cover_template_version_id)
            REFERENCES cover_template_versions(id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname='publish_snapshots_cover_template_version_fk'
           AND conrelid='publish_snapshots'::regclass
    ) THEN
        ALTER TABLE publish_snapshots
            ADD CONSTRAINT publish_snapshots_cover_template_version_fk
            FOREIGN KEY (cover_template_version_id)
            REFERENCES cover_template_versions(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS content_packages_cover_template_idx
    ON content_packages (current_cover_template_version_id)
    WHERE current_cover_template_version_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS content_package_targets_cover_template_idx
    ON content_package_targets (cover_template_version_id)
    WHERE cover_template_version_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS publish_snapshots_cover_template_idx
    ON publish_snapshots (cover_template_version_id)
    WHERE cover_template_version_id IS NOT NULL;
