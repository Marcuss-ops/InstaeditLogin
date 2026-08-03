-- Stable identity for the PostgreSQL installation, not the application
-- process. The singleton row is created once and never overwritten by
-- migrations or startup code. gen_random_uuid() is already provided by
-- pgcrypto in the baseline migration set.
CREATE TABLE IF NOT EXISTS system_installation (
    id                SMALLINT PRIMARY KEY CHECK (id = 1),
    installation_uuid UUID        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- On first provisioning, the migration command supplies the operator's
-- EXPECTED_DATABASE_INSTALLATION_UUID through a transaction-local setting.
-- If absent (dev/test), PostgreSQL generates a UUID for convenience.
INSERT INTO system_installation (id, installation_uuid)
VALUES (
    1,
    COALESCE(
        NULLIF(current_setting('app.expected_installation_uuid', true), '')::UUID,
        gen_random_uuid()
    )
)
ON CONFLICT (id) DO NOTHING;
