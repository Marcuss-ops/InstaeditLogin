# docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md

## 0. Purpose

Operator-side protocol for the irreversible Fly.io destruction step at the
end of the VPS-first cutover. Two paths depending on whether the Tigris
bucket is **Fly-attached** or **standalone Tigris**. The disambiguation
command is `flyctl storage list --app instaedit-login`.

**Scope.** This runbook owns ONLY the Fly side of the cutover:
`instaedit-login` app + its machines + `instaedit-production` Postgres +
Fly-managed cert (`api.instaedit.org`) + Fly Vault secrets + Fly volumes.
Tigris bucket lifecycle is a separate code path (VPS MinIO is the canonical
store post-cutover); `scripts/destroy-fly-app.sh` declares Tigris
**explicitly OUT-OF-SCOPE** (lines 27, 69, 148-149, 312).

The sandbox **cannot** run `flyctl`, `ssh root@51.91.11.36`, or interact
with Tigris accounts — so this file is a copy-pasteable protocol, not an
executed test. The actual destruction is gated on the operator's laptop
plus the VPS-side gate verifications.

---

## 1. Pre-conditions (all 5 must be green before any `--apply`)

| Gate | Command | Pass criterion |
| --- | --- | --- |
| `flyctl` installed    | `command -v flyctl`                              | exits 0 |
| `flyctl` authed       | `flyctl auth whoami`                             | prints your email |
| `python3` available   | `command -v python3`                             | exits 0 (only needed for `--audit` JSON parsing) |
| `jq` available        | `command -v jq`                                  | exits 0 (used by §2 disambiguation + §5 sequence) |
| `mc` available        | `command -v mc`                                  | exits 0 (only required if §3 path is taken; `brew install minio/stable/mc` or `apt install mc`) |
| VPS DNS for `api.instaedit.org` | `dig +short api.instaedit.org A`   | returns VPS IP `51.91.11.36` (NOT Fly) |
| VPS health            | `curl -fsS https://api.instaedit.org/api/v1/health` | JSON with `status: ok` |

If any of these fail, **do not proceed**. Fix the underlying infrastructure
first (`docs/OPERATIONS.md` for booking flow, `docs/DEPLOY.md` VPS sections).

---

## 2. Tigris disambiguation

### 2.1 The disambiguation command

```bash
flyctl auth login                                    # one-time per laptop
flyctl storage list --app instaedit-login --json > /tmp/fly-storage.json
```

The JSON response describes Fly Storage volumes attached to
`instaedit-login`. Decoding:

| Field value                                                                | Meaning                                                                | Pre-destroy action                                                                 |
| -------------------------------------------------------------------------- | ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `attached_to` is the app ID / contains `"instaedit-login"`                 | **Fly-attached**: Tigris bucket is a Fly Storage volume; deleting the app cascades (without permission to detach / versioning enabled, the bucket contents are wiped). | **§3 mandatory** before `--apply`                                                  |
| `attached_to` is `null` / empty / a different app / not present at all     | **Standalone Tigris**: the bucket is in a separate Tigris account; Fly destruction does not touch it. | Skip to §4 (no pre-empt backup needed)                                            |
| JSON is empty / bucket absent                                                | Bucket does not exist                                                | Skip to §4 (no pre-empt backup needed)                                            |

### 2.2 Disambiguation edge cases

| Ambiguous case | Disambiguate by |
| --- | --- |
| `flyctl storage list` returns a 401 / 403 error            | `flyctl auth whoami` may have lost the token; re-run `flyctl auth login`              |
| Output has multiple entries                                | Each entry is a separate volume; treat each independently — though `instaedit-login` will only ever have at most 1 in our case. |
| The JSON includes a `name` field but no `attached_to`      | This is **standalone**; only the `attached_to` field distinguishes Fly Storage from standalone Tigris |
| Bucket is `instaedit-prod-media` (matches TOMORROW.md §6)  | **Likely Fly-attached**; confirm with `name` + `attached_to` vs the historical default |

If output is genuinely ambiguous: STOP. Surface the JSON in a paste-back
to the operator prompt before proceeding; do not run `--apply`.

---

## 3. Fly-attached pre-destroy backup (only if §2 says attached)

Caveat: Fly Storage is a managed volume and the canonical snapshot path
is `mc cp --recursive` to a separately-named bucket OR enabling
versioning then listing the version IDs for forensic-restore. The exact
sequence below uses MinIO-compatible `mc` semantics; if your Tigris
endpoint is the standalone `t3.storage.dev`, follow the same `mc`
syntax (compile-shell-out to `mc` from the Tigris CORS examples).

### 3.1. The `mc` alias (mandatory, run once per laptop)

Per `[6, 9, 10]` canon, every `mc ... <bucket>` call requires a configured
alias. Set up `minio` as the VPS-side alias and `tigris` as the
Fly-attached bucket alias.

```bash
# VPS-side MinIO (already serves the live bucket in production; the
# `mc alias` here is the OPERATOR-LAPTOP alias, pointing at the VPS
# endpoint).
mc alias set minio http://localhost:9000 $S3_ACCESS_KEY $S3_SECRET_KEY

# Tigris Fly-attached bucket (the one we're snapshotting).
mc alias set tigris https://t3.storage.dev $TIGRIS_KEY $TIGRIS_SECRET
# (or `https://fly.storage.tigris.dev` for Fly-managed storage; pick
#  the endpoint Fly reported.)
```

### 3.2. Version enable + version-list

```bash
# 3.2.1 Enable versioning (idempotent; safe to re-run on already-enabled).
mc version enable tigris/instaedit-prod-media   # (or whatever name §2 returned)

# 3.2.2 Verify versioning is ON.
mc version info tigris/instaedit-prod-media     # expect "Status: ENABLED"

# 3.2.3 Capture a full version list before destroy.
mc ls --versions tigris/instaedit-prod-media > /tmp/tigris-versions-pre-destroy.txt
echo "captured $(wc -l < /tmp/tigris-versions-pre-destroy.txt) version records"
```

### 3.3. Snapshot — copy current state (Path A local; Path B optional)

`mc cp` needs the destination to exist. Two paths:

```bash
SNAP=$(date -u +%Y%m%dT%H%M%SZ)

# Path A — local mirror (always works; lifeboat-quality snapshot).
mkdir -p /tmp/tigris-snapshot-$SNAP
mc cp --recursive tigris/instaedit-prod-media/ /tmp/tigris-snapshot-$SNAP/
du -sh /tmp/tigris-snapshot-$SNAP/        # confirm non-zero

# Path B — sibling bucket on the SAME alias (only on standalone Tigris;
# Fly-attached WILL REJECT `mc mb` on new sibling buckets; the
# conditional handles rejection gracefully).
if mc mb tigris/instaedit-prod-media-snapshot-$SNAP 2>/dev/null; then
  mc cp --recursive tigris/instaedit-prod-media/ \
    tigris/instaedit-prod-media-snapshot-$SNAP/
fi

# On Fly-attached rejection (Path B failed silently), the §3.2.3
# versioned-listing IS the forensic snapshot. Path A (/tmp/tigris-snapshot-$SNAP/)
# remains as the lifeboat.
```

After §3 completes, **leave the snapshot where it is** (Tigris-stored,
separate from Fly's app-scoped storage). When VPS MinIO parity is confirmed
(see `docs/DEPLOY.md` §10 / `TOMORROW.md` §6 cutover audit), the
snapshot can be `mc mirror`d back into VPS MinIO if needed.

---

## 4. scripts/destroy-fly-app.sh — the canonical destruction path

The repo's `scripts/destroy-fly-app.sh` already implements the full
6-step destruction pipeline + safety gating + audit logging. **Read its
header (lines 1-50) before first use** — that's the authoritative
reference. This § is a digest with the operational primitives.

### 4.1 Modes & exit codes (verbatim from script)

| Mode              | Effect                                                                        |
| ----------------- | ----------------------------------------------------------------------------- |
| (no-arg, default) | Identical to `--audit` — operator-friendly default                           |
| `--dry-run`       | Prints destruction plan ONLY (no flyctl calls)                                |
| `--audit`         | Calls flyctl list --json × 6 — **network-dependent** (5-10s); prints inventory + safety-gate verdict; no mutations           |
| `--dry-run`       | Prints destruction plan ONLY — **fully local, no flyctl calls**                                            |
| `--apply`         | Real destruction; **requires interactive TTY**; one `yes`-typed confirmation   |
| `--ui-fallback`   | Manual Fly dashboard URLs (works without flyctl/python3 installed)            |

Exit codes:

| Code | Meaning |
| ---- | ------- |
| 0    | clean (dry-run / audit finished / apply succeeded)     |
| 1    | safety-gate fail (api.instaedit.org not 200)         |
| 2    | flyctl missing / not authed / python3 missing        |
| 3    | resource detection failed                            |
| 4    | operator aborted at master confirm                   |
| 5    | one or more destructive steps failed                 |
| 6    | `--apply` invoked without interactive TTY            |

### 4.2 Safety gate

```bash
# curl https://api.instaedit.org/api/v1/health must return 200 BEFORE
# any destruction. --apply refuses unless this is green.
```

`--audit` reports the safety-gate verdict. `--apply` exits 1 if non-green.
Reproduce manually with: `curl -sL -m 5 -o /dev/null -w "%{http_code}" \
"https://api.instaedit.org/api/v1/health"`.

### 4.3 Audit log (chain-of-custody)

`--apply` writes `/tmp/fly_destroy_<UTC-timestamp>.log` and prints its
SHA-256 hash at completion. Partial-failure runs still leave a verifiable
log on disk. The hash is bindable to this repo via:

```bash
sha256sum /tmp/fly_destroy_*.log > /tmp/fly-destroy-evidence.txt
```

### 4.4 Out-of-scope explicitly

The script prints (at line 148-149 / 312):

```
Note: Tigris (t3.storage.dev) is OUT-OF-SCOPE — external S3 service,
untouched by Fly app destruction.
```

This means `scripts/destroy-fly-app.sh --apply` will NOT touch Tigris
buckets. If §2 reports Fly-attached, §3 backup is REQUIRED before the
`--apply` step. Without §3 first, an attached bucket's objects will be
lost when the app-level destroy cascades.

---

## 5. Full sequence (copy-paste executed on operator laptop)

```bash
set -euo pipefail

# === pre-conditions ===
command -v flyctl >/dev/null || { echo "no flyctl"; exit 1; }
command -v python3 >/dev/null || { echo "no python3"; exit 1; }
flyctl auth whoami >/dev/null  || { echo "flyctl not authed"; exit 1; }

curl -fsSL -m 5 https://api.instaedit.org/api/v1/health >/dev/null \
  || { echo "VPS health gate failed"; exit 1; }

# === §2 Tigris disambiguation ===
flyctl storage list --app instaedit-login --json > /tmp/fly-storage.json
# Defensive schema probe: Fly CLI versions wrap the result differently
# across releases (`.[].attached_to`, `..|.attached_to?`, `data[]`,
# etc.). Try the common top-level array form first; fall back to
# recursive descent; last-resort jq on raw JSON (recursive object walk
# emits the BARE value of any matching key — no field name, no quotes).
ATTACHED=$(jq -r '.[]? | (.attached_to // .AttachedTo // .app_id) // ""' /tmp/fly-storage.json 2>/dev/null | head -1 || true)
if [[ -z "$ATTACHED" && -s /tmp/fly-storage.json ]]; then
  ATTACHED=$(jq -r '.. | objects | to_entries[] | select(.key|test("^(attached_to|AttachedTo|app_id)$")) | .value' /tmp/fly-storage.json 2>/dev/null | head -1 || true)
fi

# === §3 conditional Fly-attached backup ===
if [[ "$ATTACHED" == *"instaedit-login"* ]]; then
  echo "BUCKET FLY-ATTACHED — running §3 backup first"
  mc alias set tigris https://t3.storage.dev $TIGRIS_KEY $TIGRIS_SECRET
  mc version enable tigris/instaedit-prod-media
  mc ls --versions tigris/instaedit-prod-media \
    > /tmp/tigris-versions-pre-destroy.txt
  echo "captured $(wc -l < /tmp/tigris-versions-pre-destroy.txt) version records"
elif [[ -z "$ATTACHED" ]]; then
  echo "BUCKET STANDALONE — no backup needed"
else
  echo "AMBIGUOUS ATTACHED_TO=$ATTACHED; STOP"; exit 1
fi

# === §4 destroy ===
scripts/destroy-fly-app.sh --audit
# (Operator: read the audit output; if any of the 6 steps look wrong,
#  do NOT proceed with --apply.)

# TTY required by --apply (exit 6 if non-interactive).
[[ -t 0 ]] || { echo "no TTY — re-run from a real shell"; exit 1; }
scripts/destroy-fly-app.sh --apply
# (--apply will: detect TTY; if absent, exit 6 → re-run from a real
#  shell; otherwise print detected inventory + ask "Confirm: destroy
#  the above on Fly? Type 'yes' to continue:")

# === §5 post-destroy verification ===
curl -fsSL -m 5 https://api.instaedit.org/api/v1/health \
  | jq -r '"status=\(.status)"'
curl -sI https://api.instaedit.org/api/v1/health | grep -i '^server:'
dig +short api.instaedit.org A
```

### 5.1 UI fallback path (laptop without flyctl)

If `flyctl` is not installable on the operator's laptop:

```bash
scripts/destroy-fly-app.sh --ui-fallback
# (Prints: https://fly.io/apps/instaedit-login → Settings → Delete App
#          https://fly.io/apps/instaedit-production → Destroy Cluster
#          https://fly.io/apps/instaedit-login/certificates etc.)
```

The `--ui-fallback` works entirely from the script's hard-coded step list
(nothing dynamic) so it's safe to read the URL list aloud if needed.

---

## 6. Post-destroy verification

After `--apply` returns 0, ALL of the following must be green:

| Gate | Command | Expected output |
| --- | --- | --- |
| API still 200 on VPS                                       | `curl -fsS https://api.instaedit.org/api/v1/health`             | `status: ok`                                              |
| Server header                                              | `curl -sI https://api.instaedit.org/... \| grep -i '^server:'`| `server: Caddy` (always, never `Fly`)                      |
| No Fly DNS                                                 | `dig +short api.instaedit.org A \| wc -l`                      | `1` (VPS-only, was 4+ pre-cutover)                          |
| `/api/v1/auth/tiktok/start` 302                            | `curl -sI https://api.instaedit.org/api/v1/auth/tiktok/start?workspace_id=<tests> \| head -1` | `HTTP/2 302` (not 404)                       |
| Worker readiness                                           | `curl -fsS https://api.instaedit.org/ready \| jq .workers_ready` | `true`                                                  |

Once all 5 are green, append a `Date (UTC)` row to
`docs/VPS-DEPLOY-STATUS.md` §6 (probe-log table) with:
- `Resolved A`: `51.91.11.36`
- `server header`: `Caddy`
- `/ready status`: `200`
- `Notes`: a one-line summary (e.g. *"Fly destroy complete; VPS canonical; Tigris bucket was standalone (or `tigris-snapshot-…` archived)"*).

That row's stamp closes the cutover record.

**Workers footnote**: if `workers_ready` stays `false` for more than
5 minutes post-destroy, ssh the VPS and `docker compose restart worker`
per `docs/VPS-DEPLOY-STATUS.md` §3 (the worker pool is independent of
the Fly runtime; warming is normal cold-boot behaviour). Don't
post-destroy-debug beyond this without first checking the probe-log
row at §6 — the 503/workers_pending signature is benign on first boot.

**Worked example** (paste-back format ready to copy to VPS-DEPLOY-STATUS.md):

```
| 2026-07-25 HH:MM:SS | `51.91.11.36` | Caddy | 200 | Fly destroy complete; 6 audit-gate assets cleared; VPS canonical; Tigris was standalone (no §3 backup needed). |
```

Replace `HH:MM:SS` with the operator's commit-push timestamp; tune the
`Notes` field to reflect the actual disambiguation (standalone vs
Fly-attached vs Fly-attached-with-§3-backup).

---

## 7. Out of scope (explicit)

| Topic | Owner | Reference |
| --- | --- | --- |
| Tigris bucket deletion (post-destroy hygiene)            | Separate follow-up; requires VPS-MinIO parity first | `docs/DEPLOY.md` §10 |
| VPS-side MinIO bucket configuration                    | Compose + MinIO init; not Fly-side                | `docker-compose.yml` + `ops/vps/Caddyfile` |
| GitHub Actions secrets removal (FLY_*, INSTAEDIT_*_PATH) | Already on `docs/GITHUB-SECRETS-AUDIT.md`; one-shot | `docs/GITHUB-SECRETS-AUDIT.md` |
| API token rotation, OAuth client credentials rotation   | Operator's password manager (1Password); TOMORROW.md §1 | `TOMORROW.md` §1             |

---

## 8. Companion artifacts in this repo

- `docs/VPS-DEPLOY-STATUS.md` §6 — probe-log table; append the post-destroy row here
- `docs/GITHUB-SECRETS-AUDIT.md` — GitHub-side cleanup checklist
- `TOMORROW.md` §6 — Tigris-vs-MinIO parity audit (run BEFORE this destroy)
- `scripts/destroy-fly-app.sh` — the canonical destruction path
- `scripts/s3/provision-tigris.sh` — Tigris provisioning (post-cutover; may be redundant after destroy)
