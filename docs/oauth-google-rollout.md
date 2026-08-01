# Google OAuth — Operator Workflow for 200-Channel Rollout

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file holds the **offline CLI workflow** that
produces the per-manager CSV files needed to execute
[Step 8](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts)
(the channel distribution across manager Google Accounts).

Related documents:

- [Console setup walkthrough](oauth-google-setup.md)
- [Limits we have to plan around](oauth-google-limits.md)
- [Monitoring refresh-token TTL](oauth-google-monitoring.md)

This is the **offline CLI workflow** that produces the per-manager
CSV files needed to execute Step 8 above without the operator
having to hand-split 200 rows by hand. The script is dependency-
free (pure stdlib), reads a flat inventory CSV, and writes one
`manager_<slug>.csv` per manager — or `_part2.csv`, `_part3.csv`,
… files when a single manager exceeds the per-file cap.

## When to use this workflow

* You have ~200 channels (or any number) but ≤ 50 per manager
  on the inventory CSV (per the Google 2026 cap discussed in
  [Limits we have to plan around](oauth-google-limits.md)).
* You have 4–5 manager Google Accounts already created (per
  Step 2 + Step 8 in the [setup walkthrough](oauth-google-setup.md)).
* You want each manager to receive a CSV they can `POST` to
  `/admin/channels/import-csv` after their OAuth dance completes
  so the per-channel import is done by the canonical
  `internal/channelimport.Parse` path — no manual re-mapping.

## How to run

The CLI ships at `scripts/distribute_channels_to_managers/`:

```bash
go run ./scripts/distribute_channels_to_managers \
    -input inventory.csv \
    -output-dir ./out/managers \
    -buckets 4 \
    -cap 50
```

Flags:

| Flag           | Default             | Purpose                                                                                                       |
| ---            | ---                 | ---                                                                                                           |
| `-input`       | *(required)*        | Input inventory CSV. Header row MUST contain at minimum `channel_id`, `channel_name`, `manager_email_hint`.   |
| `-output-dir`  | `./out/managers`    | Output directory for per-manager CSVs. Created if missing.                                                    |
| `-buckets`     | `4`                 | Target bucket count. Matches the 4–5 manager rollout in Step 8 above; the script round-robins managers into buckets when `len(managers) > buckets`. |
| `-cap`         | `50`                | Hard cap per output file. The YouTube 2026 silent-invalidation limit per `(Google Account, OAuth client)` pair (see [Limits](oauth-google-limits.md)). |

The bucket assignment is **deterministic** over SORTED manager
emails, so a re-run on the same inventory yields byte-for-byte
identical output — important when the operator wants to spot-
check a regression. Managers with more than `-cap` channels get
chunked into `manager_<slug>_part1.csv` + `_part2.csv` + …
files; the `cap` is enforced strictly per output file (a single
oversized file is never produced). The output CSVs include the
full `internal/channelimport.CSVHeaderColumns` header so a
follow-up import via `internal/channelimport.Parse` (or
`POST /admin/channels/import-csv`, or `scripts/import_channels_csv.go`)
accepts them without any column re-mapping.

### Per-manager summary line format

For each output file the script prints a summary line so the
operator can confirm the split before kicking off OAuth dances:

```
Bucket B | NN channels | manager=<email>      | UC...first .. UC...last | <output-file-name>
```

* **`Bucket B`** — the deterministic bucket index assigned by
  the round-robin over sorted manager emails. Identical across
  re-runs on the same inventory.
* **`NN channels`** — the row count in this file. NEVER exceeds
  `-cap`. A line with `NN > -cap` is a bug; report it as a
  regression on the script.
* **`manager=<email>`** — the manager Google Workspace email
  that owns this file's channels. Slugified to `<slug>` in the
  filename.
* **`UC...first .. UC...last`** — the first and last
  `channel_id` in this file (input order). Useful for spot-
  checking channel sorting.
* **`<output-file-name>`** — the file the script just wrote.
  The format is `manager_<slug>.csv` (single file) or
  `manager_<slug>_partN.csv` (overflow).

A trailing `=== Wrote N files ===` block lists every file by full
path so the operator can confirm exactly what landed on disk.

### Operator checklist (after the split)

1. **Run the script** against the master inventory CSV.
2. **Verify the summary** — confirm every line shows ≤
   `-cap` channels (50 by default). Any line over `-cap` is a
   bug; re-check the `manager_email_hint` distribution in the
   source sheet.
3. **Verify zero overlap** — open two of the per-manager CSVs
   side-by-side and confirm every `channel_id` appears exactly
   once across the fleet. The script guarantees this because
   keys are partitioned by `manager_email_hint`, but a manual
   spot-check before 50 OAuth dances is cheap insurance.
4. **Verify determinism** — a second run with the same input
   must produce identical file names + identical contents. The
   script is deterministic; a divergence is a regression.
5. **Hand each manager their CSV** along with a one-pager
   showing their steps in Step 8 above.
6. **Hand off to the import path** — once each manager completes
   their OAuth dance, the operator feeds the manager's CSV back
   through the canonical `POST /admin/channels/import-csv`
   endpoint (or `scripts/import_channels_csv.go`). The script's
   output uses the full CSVHeaderColumns shape, so the import
   path accepts it directly without any column re-mapping —
   `workspace`, `group`, `language`, `timezone`, and
   `expected_upload_frequency` columns are pre-populated with
   empty strings for the operator to fill in if relevant.

### Failure modes the script guards against

| Failure                                  | Detection                                                                |
| ---                                      | ---                                                                      |
| Missing required header column           | Hard error from `readInventoryCSV` naming the missing column.            |
| Empty `channel_id` on a data row         | Hard error from `readInventoryCSV` naming the offending row number.      |
| Single manager with > `-cap` channels    | `_partN` files emitted automatically; never a single oversized file.     |
| Identical `channel_id` across managers   | NOT currently detected — operator spot-checks (see Operator Checklist #3). Future: companion `verify-distributed` script. |
| Output directory missing                 | `os.MkdirAll` creates it; owner is the running user.                     |
| Re-run produces different output         | A regression — file an issue with the `inventory_<git-sha>.csv` checked into `out/`. |

See `scripts/distribute_channels_to_managers/main_test.go` for
the full test surface (eight unit tests cover the round-robin
assignment, the 50-channel cap, the `_partN` overflow split, the
deterministic re-run guarantee, the manager-email sort key, the
inventory header validation, the empty-`channel_id` rejection,
the slug helper, and the round-trip write back to disk).
