# Weekly PostgreSQL restore verification

The weekly drill verifies that the newest valid custom-format PostgreSQL dump
can be restored without touching the live `instaedit_login` database.

## What it does

`/usr/local/sbin/instaedit-db-restore-check`:

1. Takes the shared `/run/lock/instaedit-db-backup.lock` used by the daily
   backup, so it cannot race `pg_dump`.
2. Selects the newest dump with a matching `SHA256SUMS_<timestamp>` manifest.
3. Verifies the dump checksum and that `pg_restore` can read its archive.
4. Captures anonymous counts for `users`, `workspaces`,
   `platform_accounts`, `oauth_connections`, `tokens`, `posts`, and
   `media_assets` from the live database.
5. Creates a generated temporary database named
   `instaedit_restore_check_<UTC timestamp>_<pid suffix>` inside the existing
   `instaedit-db` PostgreSQL container.
6. Restores with `pg_restore --exit-on-error --no-owner --no-acl`.
7. Compares restored counts with the live counts using snapshot-safe
   invariants: a restored table must not exceed the live count, and a live
   non-empty table must not restore to zero. Exact equality is intentionally
   not required because the dump is older than the current live database.
8. Checks the `system_installation` singleton row when present.
9. Drops the temporary database in an `EXIT` trap on success, failure, or
   termination and verifies that it no longer exists. The live database is
   never a restore target.

The journal contains timestamps, dump filenames, table names and counts only;
no credentials, DSNs, token data, email addresses, UUID values, or row contents
are logged.

## Install on the VPS

Copy the tracked files from the repository checkout, then install them as
root. The script and units must not be writable by the application user:

```bash
sudo install -o root -g root -m 0700 \
  scripts/db/instaedit-db-restore-check \
  /usr/local/sbin/instaedit-db-restore-check
sudo install -o root -g root -m 0644 \
  ops/systemd/instaedit-db-restore-check.service \
  /etc/systemd/system/instaedit-db-restore-check.service
sudo install -o root -g root -m 0644 \
  ops/systemd/instaedit-db-restore-check.timer \
  /etc/systemd/system/instaedit-db-restore-check.timer
sudo systemctl daemon-reload
sudo systemctl enable --now instaedit-db-restore-check.timer
```

The timer runs on Sundays at `05:15 UTC` with up to 15 minutes of jitter and
`Persistent=true`, so a missed run is attempted after the host returns. The
45-minute service timeout prevents a stuck restore from running forever.

## Manual run and verification

Run as root, preferably after confirming the daily backup succeeded:

```bash
sudo systemctl start --wait instaedit-db-restore-check.service
sudo systemctl show instaedit-db-restore-check.service \
  -p Result -p ExecMainStatus --value
sudo journalctl -u instaedit-db-restore-check.service -n 100 --no-pager
sudo systemctl list-timers instaedit-db-restore-check.timer
```

A successful run ends with `PASS: restore verification succeeded` and the
temporary database cleanup message. The service returns non-zero when no
verified dump exists, the database/container is unavailable, `pg_restore`
fails, or a restored count exceeds the current live count.

## Failure handling

Do not manually delete the live database. First inspect the journal for the
failure class and confirm cleanup:

```bash
sudo docker exec -u postgres instaedit-db psql -X -d postgres -At \
  -c "SELECT datname FROM pg_database WHERE datname LIKE 'instaedit_restore_check_%'"
```

An empty result means cleanup completed. If a temporary database remains,
remove only the generated `instaedit_restore_check_*` database after confirming
no restore service is running; never use a wildcard directly in `DROP DATABASE`.

A count invariant failure means the restore is inconsistent with the current
live state: a restored count exceeded live. Normal inserts after the dump
are allowed, so exact equality is not required. Correlate the dump timestamp, backup journal, and live writes, then
re-run after a fresh successful daily backup. A `pg_restore` error must be
investigated before treating the backup as recoverable. A cleanup failure is
also a failed drill and must be resolved before the next run.
