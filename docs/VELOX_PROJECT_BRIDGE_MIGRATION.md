# Velox project bridge migration

This is a one-time, relationship-only backfill. It connects an existing Velox
handle (`velox_project_id`) to an already-existing InstaEdit
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
- Migration 114 must be applied before using this command; it adds the
  persistent `migration_run_id` marker used to scope rollback safely.

Mapping format:

```json
[
  {
    "velox_project_id": "ve_abc123",
    "project_id": "thumbproj_01JABC",
    "channel_id": "UCxxxxxxxx",
    "video_id": "AbCd1234",
    "language": "en"
  }
]
```

Only `velox_project_id` and `project_id` are required. The optional assertions
are checked against authoritative database values; they are never used to
search for a project. The channel is resolved from
`workspace_channels → platform_accounts.platform_user_id`, and the video from
`youtube_video_edits.youtube_video_id`.

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
it requires the report `run_id`, the persisted `migration_run_id`, and the
bridge's complete workspace, channel, video, language, project, Velox context,
and creation timestamp to be unchanged. If an operator or later migration
changed a bridge, rollback aborts without deleting it. `already_linked` entries
are never removed. Rollback affects only `velox_project_bridges`; it never deletes an
InstaEdit project or any Velox editor data.

## Verification

```sh
go test ./internal/veloxmigration
# CLI help does not connect to the database:
go run ./cmd/migrate-velox-bridges --help
```

The command intentionally refuses to run when `DATABASE_URL` is unset. Never
run `--apply` against production without first saving and reviewing the
report. Keep both reports as migration evidence.
