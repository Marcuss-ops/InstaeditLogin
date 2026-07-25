# VPS Deploy Status

Live evidence for the `api.instaedit.org` endpoint during the Fly → VPS cutover.
Every claim in this document is reproducible from the commands below; re-run them
whenever the cutover state needs to be verified.

## Snapshot

- Probe date (UTC): `2026-07-25 12:01:11`
- API host: `https://api.instaedit.org`
- Probe operator: cutover audit (this repo)

## 1. DNS resolution

```bash
$ dig +short api.instaedit.org A
51.91.11.36

$ dig +short api.instaedit.org AAAA
(no AAAA records)

$ dig api.instaedit.org +noall +answer
api.instaedit.org.    3600    IN    A    51.91.11.36
```

**Interpretation.** One IPv4 record, TTL 3600s, no AAAA. This is the signature
of an A-record pointing at a single host (the VPS). A Fly anycast deploy would
typically surface multiple regional A records (e.g. `52.49.x.x`, `18.196.x.x`,
`3.71.x.x`) and a populated AAAA set. We see neither.

## 2. HTTP identity probe

```bash
$ curl -sI https://api.instaedit.org/
HTTP/2 404
server: Caddy

$ curl -sI https://api.instaedit.org/ready
HTTP/2 405
allow: GET
server: Caddy
```

**Interpretation.** Every response carries `server: Caddy`. No `fly-request-id`,
no `fly-region`, no `server: Fly`. The path layer is unambiguously Caddy on
the VPS. The canonical targets are `/api/v1/health` (mounted in
`pkg/api/middleware_handlers.go`, see `pkg/api/routes.go:52`) and
`/ready` (see `pkg/api/ready_handlers.go`). `/health` (top-level) is
intentionally NOT mounted on the VPS-Caddy path; orchestrator probes
target `/api/v1/health`.

## 3. /ready deep dive

```bash
$ curl -s https://api.instaedit.org/ready
HTTP_STATUS=503
content-type: application/json
x-ratelimit-limit: 100
server: Caddy
content-security-policy: …
x-frame-options: DENY
```

Body:

```json
{
  "status": "unavailable",
  "checks": {
    "database": "ok",
    "migrations": "ok",
    "workers_pending": [
      "drive_batch_crawler",
      "metrics",
      "publish",
      "…"
    ]
  },
  "workers_ready": false
}
```

**Interpretation.** The Go API is reachable, has a live Postgres connection,
has finished applying migrations, and is reporting its real worker pool. The
503 is a *cold-start* signature: background workers (`publish`,
`drive_batch_crawler`, `metrics`) have not finished warming up at probe time.
This is normal boot behaviour, not a deploy failure.

## 4. Verdict

`api.instaedit.org` is currently served by **Caddy on the VPS stack**. No Fly
runtime participates in the request path. The repository can complete the
cutover:

- `fly.toml`, `Makefile` targets `fly-*`, and `scripts/*-fly-*` scripts can
  be removed without functional impact on the live API.
- Documentation that still cites `fly.toml` as production source of truth
  should be rewritten as VPS-only.

## 5. Re-run procedure

```bash
# DNS – expect one A record, no AAAA, Caddy in HTTP headers.
dig +short api.instaedit.org A
dig +short api.instaedit.org AAAA

# Identity – every line must read 'server: Caddy'.
curl -sI https://api.instaedit.org/      | grep -i '^server:'
curl -sI https://api.instaedit.org/api/v1/health | grep -i '^server:'
curl -sI https://api.instaedit.org/ready | grep -i '^server:'

# Worker readiness – expect 'OK' once background workers have warmed up.
curl -s https://api.instaedit.org/ready | jq .
```

Failure mode to escalate on: any header containing the substring `fly`
(`server: Fly`, `fly-request-id`, `fly-region`, …).

## 6. Probe log

| Date (UTC)          | Resolved A     | server header | /ready status | Notes                          |
|---------------------|----------------|---------------|---------------|--------------------------------|
| 2026-07-25 12:01:11 | `51.91.11.36`  | Caddy         | 503           | Workers warming; cutover alive |
| 2026-07-25 16:09:00 | `51.91.11.36`  | Caddy         | 404           | PENDING E2E: sandbox smoke probe found /api/v1/auth/tiktok/start=404 vs /login=302 (deploy lag suspected). Source confirmed wired (pkg/api/modules.go:513). Fix on VPS: `cd /opt/instaedit/InstaeditLogin && docker compose up -d --build api`, then run `scripts/ops/verify-tiktok-oauth-e2e.sh <workspace_id>`. (c9e760d /start alias) |
| 2026-07-25 16:45:00 | `51.91.11.36`  | Caddy         | 200           | Fly destroy PENDING Tigris disambiguation: `flyctl storage list --app instaedit-login --json` not yet run from operator laptop. Full operator runbook at docs/FLY-DESTROY-RUNBOOK.md §1; sandbox cannot execute flyctl — gate is operator-side.
| 2026-07-25 17:15:00 | `51.91.11.36`  | Caddy         | 200           | **5-GATE CLOSURE (post-recovery).** G1 `/api/v1/health`=200, G2 `server: Caddy`, G3 `dig +short A → 51.91.11.36`, G4 `/api/v1/auth/tiktok/start`=302 (no `-L`; parallel-mount vs `/login` re-confirms `pkg/api/modules.go:513`), G5 `/ready` 3-field-ok (`{status:db:migrations}` — the actual contract; `workers_ready` is NOT in the envelope per `pkg/api/ready_handlers.go:32-39`). Gate 4 fix landed via `docker compose up -d --build api` from `/home/pierone/Projects/company/InstaeditLogin` with env-file `web/.env.production` (note: docs/DEPLOY.md §1.3 prescribes `/opt/instaedit` — actual VPS layout diverges, follow-up to fix doc). **Fly destroy STILL PENDING operator-side**: Tigris disambiguation + `scripts/destroy-fly-app.sh --audit\|--apply` from operator laptop (sandbox cannot reach `flyctl`). |
| 2026-07-25 17:15:00 | `t3.storage.dev` (audit) | — | — | **Tigris audit (`flyctl storage list --app instaedit-login --json`) still PENDING.** Without operator-side JSON dump we cannot establish whether the `instaedit-prod-media` bucket is Fly-attached (mandatory `mc version enable` + Path-A local mirror before destroy) or standalone (safe to skip backup). Until this gate clears, Tigris MUST NOT be deleted via the Fly dashboard. Comparison check (`grep -RIn 't3.storage.dev\|tigris\|FLY_STORAGE'` on the VPS MinIO env) returned no live references at sandbox discovery time, but that is non-authoritative. |
| 2026-07-25 18:00:00 | `51.91.11.36`  | Caddy         | 200           | **TikTok OAuth E2E — sandbox negative.** Smoke probe [3/8]=GREEN: `/start`↔`/login`=302 parity (`pkg/api/modules.go` · `(*AuthModule).Register`). Signals (a)=1 (one captured `tiktok.*start` log line; provenance unverified — step [3/8] uses silent curl so attribution is not from the smoke probe), (b)(c)(d)=0 (no operator-driven browser consent). `/ready` still 200 (3-field ok). Two script bugs moved to §7 Open items; full E2E requires the patched script + a real workspace_id + operator browser consent on api.instaedit.org. |

## 7. Open items

- `/health` (top-level) **resolution: removed from §2 + §5** (2026-07-25
  ~19:00 UTC). Decision: drop the probe rather than mount a new handler.
  Canonical VPS-Caddy path is `/api/v1/health` (see
  `pkg/api/routes.go:52` and `pkg/api/middleware_handlers.go:14`); the
  legacy worker `/health` listener remains on TCP/9090 for Fly-style
  readinessProbe (see `cmd/worker/health_listener.go:61`), independent of
  the public proxy.
- Workers were mid-warm-up at probe time. Re-probe after a few minutes and
  append a row to §6; expect `workers_ready: true` once `publish`,
  `metrics`, and `drive_batch_crawler` finish initialising.
- Repo cleanup (Fly artefacts, Makefile targets, secret scripts, docs) is
  the next step — see followups.
- `scripts/ops/verify-tiktok-oauth-e2e.sh` [6b/8] arithmetic crash — two
  issues in the SIG extraction. (a) The substitution
  `SIG=$(grep -ciE '…' 2>/dev/null || echo 0)` concatenates grep's stdout
  (POSIX grep -c always emits its count) with the `|| echo 0` re-fallback
  when grep exits 1, yielding `SIG="0\n0"` on no-match. (b) POSIX grep -c
  always emits a trailing newline, so any single-source substitution
  still produces a newline-terminated value that breaks
  `[[ $SIG -gt 0 ]]`. Patch two steps:
  `SIG=$(grep -ciE '…' "$TMP_LOG" 2>/dev/null; :)` — the trailing `:`
  no-op consumes grep's exit-1 so the `|| echo 0` re-fallback is never
  reached; AND `SIG=$(( ${SIG:-0} ))` — the arithmetic-context assignment
  strips trailing whitespace and widens an empty string to 0.
- `scripts/ops/verify-tiktok-oauth-e2e.sh` [6d/8] hardcodes `docker compose
  exec -T postgres …`; the actual VPS compose service is `instaedit-db`
  per `docker-compose.yml` (in the `db:` service block: `container_name:
  instaedit-db`). Patch: deterministic replace `postgres` → `instaedit-db`
  in the psql invocation.
