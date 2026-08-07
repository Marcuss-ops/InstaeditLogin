# Velox project bridge migration

This is a one-time, relationship-only backfill. It connects an existing Velox
handle (`external_project_id`) to an already-existing InstaEdit
`thumbnail_projects.id`. It does **not** create shell projects and does not
move or rewrite timeline, scene, layer, object, asset, revision, export, render,
or canvas data.

## Preconditions

- Run from `InstaeditLogin`.
- `DATABASE_URL` must point to the intended InstaEdit PostgreSQL installation.
- `EXPECTED_DATABASE_INSTALLATION_UUID` is strongly recommended and is required
  by the normal production configuration.
- A human-verified mapping file is required. No match is inferred from names,
  titles, timestamps, canvas JSON, thumbnails, or render data.
- Migrations 112, 114, 115 and 116 must be applied before using this command.
  Migration 112 creates the bridge, 114 adds the persistent `migration_run_id`
  marker used to scope rollback safely, 115 adds only editor metadata, and 116
  removes legacy channel/video context from the bridge.

Mapping format:

```json
[
  {
    "external_project_id": "ve_abc123",
    "project_id": "thumbproj_01JABC"
  }
]
```

Only `external_project_id` and `project_id` are accepted. Workspace and
application-project validity are checked against InstaEdit's authoritative
records; no channel, video, platform, language, group, or membership data is
read into or written to the bridge.

## Dry-run (default)

```sh
DATABASE_URL='...' \
EXPECTED_DATABASE_INSTALLATION_UUID='...' \
go run ./cmd/migrate-velox-bridges \
  --mapping ./operator/velox-project-bridge-mapping.json \
  --report ./operator/velox-project-bridge-dry-run.json
```

Without `--apply`, the command performs only reads and writes the JSON report
file. The report classifies each mapping as `matched`, `already_linked`,
`missing`, `ambiguous`, or `conflict`. A dry-run never inserts into
`velox_project_bridges`.

A mapping is `matched` only when all of these are true:

1. exactly one `youtube_video_edits` row has the Velox handle;
2. the mapped `thumbnail_projects.id` exists, is not deleted, and has the same
   workspace;
3. the session account is a YouTube channel in that workspace; and
4. optional channel/video assertions match the authoritative rows.

An apply is refused if any entry is missing, ambiguous, or conflicting. This
prevents a partial migration.

## Apply

After reviewing the dry-run report:

```sh
DATABASE_URL='...' \
EXPECTED_DATABASE_INSTALLATION_UUID='...' \
go run ./cmd/migrate-velox-bridges \
  --mapping ./operator/velox-project-bridge-mapping.json \
  --apply \
  --report ./operator/velox-project-bridge-applied.json
```

The insert runs in one transaction. Existing identical bridges are reported
as `already_linked` and are not rewritten. New rows are reported as `created`.
Database uniqueness constraints remain the final protection against duplicate
project or Velox ownership.

## Rollback

Rollback requires the JSON report produced by a successful `--apply`:

```sh
DATABASE_URL='...' \
EXPECTED_DATABASE_INSTALLATION_UUID='...' \
go run ./cmd/migrate-velox-bridges \
  --rollback-report ./operator/velox-project-bridge-applied.json \
  --report ./operator/velox-project-bridge-rollback.json
```

Rollback deletes only entries marked `created` in that report. Before deleting,
it requires the report `run_id`, the persisted `migration_run_id`, andthe bridge's project, workspace, Velox reference, editor metadata, and creation
timestamp to be unchanged. If an operator or later migration
changed a bridge, rollback aborts without deleting it. `already_linked` entries
are never removed. Rollback affects only `velox_project_bridges`; it never deletes an
InstaEdit project or any Velox editor data.

## Verification

The destructive cleanup in migration 116 must be exercised against a disposable
PostgreSQL database before production rollout. The integration test seeds a
bridge row with populated legacy platform/account/channel/video/language values,
applies the full chain 112 → 114 → 115 → 116, reruns the chain for idempotency,
and verifies that only the project mapping and allowed editor metadata remain.
It also verifies that authoritative InstaEdit channel rows are unchanged.

```sh
go test -tags=integration -count=1 -run 'TestMigration112And114_VeloxProjectBridgeSchemaAndConstraints' ./internal/database/...
go test ./internal/veloxmigration
# CLI help does not connect to the database:
go run ./cmd/migrate-velox-bridges --help
```

After applying the migration to a real installation, run the read-only schema
checks from `VerifyMigrationReady` and confirm that `velox_project_bridges`
contains no group/channel columns, foreign keys or channel indexes. The
migration never deletes InstaEdit `groups`, `workspace_channels`, memberships,
or provider records; those remain owned by InstaEdit.

The command intentionally refuses to run when `DATABASE_URL` is unset. Never
run `--apply` against production without first saving and reviewing the
report. Keep both reports as migration evidence.
