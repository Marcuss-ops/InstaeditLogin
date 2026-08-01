# scripts/ops/PASTE-BACK-FLY-DESTROY.md

> Operator-side paste-back checklist for the irreversible Fly destroy step
> at the end of the VPS-first cutover. **Cannot be run from sandbox** —
> the actual SSH + flyctl + mc execution is operator-only.

Mirrors `docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md` §1-§6 one-for-one. Use this file
side-by-side with the runbook on the operator's laptop. Paste back the
filled-in sections (everything between the `==>` arrows) into the
operator prompt after each section completes.

> **Replacement rule** — every `⇒ [ paste ]` marker is a *placeholder*,
> not a literal. Replace it with the actual command output from your
> terminal: raw stdout/stderr, the single-line reply written to stdout,
> or the JSON contents you cat'd. Don't paste back the literal text
> `[ paste ]`. The `||>` arrows are visual aids only — the real
> paste unit is the text immediately to the right of `⇒`.

---

## Pre-status

| Field | Value |
| --- | --- |
| Operator laptop | `<hostname>` (`uname -a`) |
| VPS reachable | `<yes / no>` (`ssh root@51.91.11.36 'whoami'`) |
| Sandbox last commit | `<commit SHA>` (`git log -1 --format=%h`) |
| VPS Caddy verified | `<yes / no>` (`curl -sI https://api.instaedit.org/ \| grep -i '^server:'` → `Caddy`) |

---

## §1 Pre-conditions (all 5 must be green)

```text
# Command                          # Expected pass criterion          # Paste here
command -v flyctl                  → exits 0                         ⇒ [ paste ]  # §1.1 flyctl installed
flyctl auth whoami                 → prints your email                ⇒ [ paste ]  # §1.2 flyctl authed
command -v python3                 → exits 0                         ⇒ [ paste ]  # §1.3 python3 available
command -v jq                      → exits 0                         ⇒ [ paste ]  # §1.4 jq available
command -v mc                      → exits 0 (if §3 planned)         ⇒ [ paste ]  # §1.5 mc available
dig +short api.instaedit.org A     → "51.91.11.36"                    ⇒ [ paste ]  # §1.6 VPS DNS
curl -fsS https://api.instaedit.org/api/v1/health
                                  → JSON with "status":"ok"          ⇒ [ paste ]  # §1.7 VPS health
```

**GATE.** If any line is red, STOP. Do not proceed to §2.

---

## §2 Tigris disambiguation

```text
flyctl auth login                      # one-time per laptop
flyctl storage list --app instaedit-login --json > /tmp/fly-storage.json
cat /tmp/fly-storage.json             # raw JSON paste                   ⇒ [ paste ]

# Defensive jq schema probe (Fly CLI wraps differently across releases).
ATTACHED=$(jq -r '.[]? | (.attached_to // .AttachedTo // .app_id) // ""' \
              /tmp/fly-storage.json 2>/dev/null | head -1)
# Last-resort jq recursive descent if the top-level-array probe returns empty
# (e.g. flyctl wrapped the result in an envelope / a different schema).
# Walks every object in the tree, picks the first key matching any of
# the three Fly CLI field-name variants, and emits ONLY the bare value
# (no field name, no surrounding quotes) — unlike the prior grep wart
# that returned "attached_to":"instaedit-login".
if [[ -z "$ATTACHED" && -s /tmp/fly-storage.json ]]; then
  ATTACHED=$(jq -r '.. | objects | to_entries[] | select(.key|test("^(attached_to|AttachedTo|app_id)$")) | .value' \
              /tmp/fly-storage.json 2>/dev/null | head -1)
fi
echo "ATTACHED='$ATTACHED'"          # ⇒ [ paste the echo line ]
```

**Decision rule** (paste the chosen branch):

| Condition | Branch | Paste |
| --- | --- | --- |
| `ATTACHED` contains `instaedit-login` *or* matches `attached_to.*instaedit-login` | **Fly-attached** → §3 backup mandatory | `[ ]` |
| `ATTACHED` is empty / standalone | **Standalone Tigris** → skip §3, go straight to §4 | `[ ]` |
| Output is genuinely ambiguous (neither standalone nor contains the app) | STOP. Paste the raw JSON in chat. Do not run §4. | `[ ]` |

---

## §3 Fly-attached pre-destroy backup (only if §2 says attached)

```text
# 3.1 Configure mc aliases.
mc alias set minio http://localhost:9000 $S3_ACCESS_KEY $S3_SECRET_KEY
                                     ⇒ [ paste ]
mc alias set tigris https://t3.storage.dev $TIGRIS_KEY $TIGRIS_SECRET
                                     ⇒ [ paste ]

# 3.2 Enable versioning (idempotent).
mc version enable tigris/instaedit-prod-media
                                     ⇒ [ paste ]
mc version info tigris/instaedit-prod-media
                                     ⇒ [ paste ]  # expect "Status: ENABLED"

# 3.3 Snapshot — Path A local mirror (always works).
SNAP=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p /tmp/tigris-snapshot-$SNAP
mc cp --recursive tigris/instaedit-prod-media/ /tmp/tigris-snapshot-$SNAP/
du -sh /tmp/tigris-snapshot-$SNAP/   ⇒ [ paste ]  # expect >0 size

# 3.4 Path B — sibling bucket (only on standalone Tigris; Fly-attached WILL REJECT).
if mc mb tigris/instaedit-prod-media-snapshot-$SNAP 2>/dev/null; then
  mc cp --recursive tigris/instaedit-prod-media/ \
    tigris/instaedit-prod-media-snapshot-$SNAP/
  echo "Path B: sibling bucket created"   ⇒ [ paste ]
else
  echo "Path B: rejected (Fly-attached); Path A is the snapshot"
                                         ⇒ [ paste ]
fi

# 3.5 Forensic versioned listing (always capture, regardless of branch).
mc ls --versions tigris/instaedit-prod-media > /tmp/tigris-versions-pre-destroy.txt
wc -l /tmp/tigris-versions-pre-destroy.txt
                                         ⇒ [ paste ]
```

---

## §4 scripts/destroy-fly-app.sh — the canonical destruction

```text
# 4.0 TTY required for --apply (exit 6 if non-interactive).
test -t 0 || { echo "no TTY — re-run from a real shell"; exit 1; }
echo "TTY=$?"                       ⇒ [ paste ]  # expect 0

# 4.1 Audit: inventory + safety-gate verdict; no mutations.
scripts/destroy-fly-app.sh --audit
                                     ⇒ [ paste ]  # expect "Audit gate: PASS"

# 4.2 Apply: real destruction; TTY confirmation gate.
scripts/destroy-fly-app.sh --apply
                                     ⇒ [ paste ]  # expect confirmation prompt + per-step progress

# 4.3 Audit log path (chain-of-custody).
ls -la /tmp/fly_destroy_*.log
sha256sum /tmp/fly_destroy_*.log    ⇒ [ paste ]
```

**GATE.** If `--apply` returns non-zero (exit 5): the script writes partial
results + the audit log nonetheless. Run `cat /tmp/fly_destroy_*.log`,
evaluate the per-step failures (`failed_steps[]` line), and re-run with
`--apply` to retry the missing steps. Idempotent on partial state.

---

## §5 Post-destroy verification (all 5 must be green)

```text
curl -fsS https://api.instaedit.org/api/v1/health
                                          ⇒ [ paste ]  # expect `"status":"ok"`
curl -sI https://api.instaedit.org/api/v1/health \
  | grep -i '^server:'
                                          ⇒ [ paste ]  # expect `server: Caddy`
dig +short api.instaedit.org A            ⇒ [ paste ]  # expect exactly 1 line; 51.91.11.36
curl -sSI "https://api.instaedit.org/api/v1/auth/tiktok/start?workspace_id=$TEST_WS" \
  | head -1
                                          ⇒ [ paste ]  # expect "HTTP/2 302"
curl -fsS https://api.instaedit.org/ready | jq .
workers_ready:
                                          ⇒ [ paste ]  # expect `true`
```

**Workers footnote**: if `workers_ready` stays `false` for more than 5
minutes post-destroy, ssh the VPS and `docker compose restart worker`
per `docs/VPS-DEPLOY-STATUS.md` §3. The worker pool is independent of
the Fly runtime; warming is normal cold-boot behavior.

---

## §6 Probe-log row (ready to commit to docs/VPS-DEPLOY-STATUS.md §6)

Fill in the placeholder based on the §5 verification + the §2 disambiguation
result. Commit on main as `docs(vps-deploy-status): fly destroy
complete (post-applied @ <UTC>)`.

```text
| <UTC timestamp> | `51.91.11.36` | Caddy | 200 | Fly destroy complete; 6 audit-gate assets cleared; VPS canonical; Tigris was <standalone | fly-attached-with-Path-A-backup> (snapshot at /tmp/tigris-snapshot-<UTC>/). |
```

Replace the `<…>` placeholders with your actuals:

```text
| YYYY-MM-DD HH:MM:SS | `51.91.11.36` | Caddy | 200 | Fly destroy complete; 6 audit-gate assets cleared; VPS canonical; Tigris was <…> (snapshot at /tmp/tigris-snapshot-<…>/). |
```

The final version (paste-back as one line):

```
[ single-line row ready to copy into docs/VPS-DEPLOY-STATUS.md §6 ]
```

**Copy the `| YYYY-MM-DD | …` line itself, NOT the surrounding `[ ]`
brackets or the code-block fences**, into `docs/VPS-DEPLOY-STATUS.md`
§6 at the bottom of the existing probe-log table.

---

## §7 Post-cutover hygiene (one-shot after §6 lands)

Once the green probe-log row is committed on main:

| # | Action | Paste-back |
| --- | --- | --- |
| 1 | `git log --diff-filter=D --name-only --since="1 day ago"` — confirm no accidental Fly file deletions | `[ ]` |
| 2 | `git rm scripts/destroy-fly-app.sh` — the operator-side destroy orchestrator is no longer needed; the canonical VPS stack ships in docker-compose | `[ ]` |
| 3 | Update §3 of `docs/GITHUB-SECRETS-AUDIT.md` to mark `scripts/destroy-fly-app.sh` as `deleted` | `[ ]` |
| 4 | Mark §6 of `docs/VPS-DEPLOY-STATUS.md` FINAL row as the cutover-closing stamp (commit message adds `closes-the-cutover` tag) | `[ ]` |
| 5 | (Optional, deferred) Update §7 of `docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md` to redirect to VPS-commit `git log --grep=fly-destroy` | `[ ]` |

Once §7 is complete, the cutover record is closed.

---

## Reference: pre-destroy page references

While running the chain, keep these pages open:

1. `docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md` — operator protocol
2. `https://fly.io/apps/instaedit-login` — Fly dashboard for the live app
3. `https://fly.io/apps/instaedit-production` — Fly dashboard for the postgres cluster
4. `https://console.storage.dev/` — Tigris bucket dashboard (only if §3 runs)
5. `docs/VPS-DEPLOY-STATUS.md` — target for §6 row append
6. `docs/GITHUB-SECRETS-AUDIT.md` — already on main; cross-reference
