# InstaEditLogin — API Endpoints

Base path: `/api/v1`

## Health & Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Health check and registered platforms |
| GET | `/api/v1/metrics` | Prometheus metrics (optional basic auth) |

## Authentication

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/auth/{provider}/login` | Start OAuth flow for provider |
| GET | `/api/v1/auth/{provider}/callback` | OAuth callback and JWT issuance |

Providers: `meta`, `tiktok`, `twitter`, `youtube`, `linkedin`.

## Accounts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/accounts` | List connected platform accounts. Deleted-state accounts (`account_state=deleted`, i.e. disconnected/revoked) are hidden by default; pass `?include_deleted=true` to include them |
| POST | `/api/v1/accounts/{id}/disconnect` | Explicit channel disconnect: `status=disconnected` (row kept for audit), removal from all groups and publishable destinations, future jobs cancelled (parent aggregates recomputed), shared Google grant/token preserved while an active sibling channel remains; 204 on success |
| DELETE | `/api/v1/accounts/{id}` | **Deprecated** — answered with `410 Gone` and guidance; use `POST /api/v1/accounts/{id}/disconnect` (soft) or `DELETE /api/v1/accounts/{id}/data` (permanent, P1) |
| DELETE | `/api/v1/accounts/{id}/oauth-grant` | Revoke a Google account and every channel sharing its grant (YouTube only); 204 on success |
| DELETE | `/api/v1/accounts/{id}/data` | Permanent account deletion / tombstone: `status=deleted`, `username=[deleted]`, `metadata={}` (row kept for FK integrity of historical publications), removal from groups/workspace channels/snapshots, future jobs cancelled, Google grant revoked + tokens/`oauth_connections` removed only when this is the last active channel of the grant; 204 on success |

## Workspaces

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/workspaces` | Create workspace |
| GET | `/api/v1/workspaces` | List workspaces |
| GET | `/api/v1/workspaces/{id}` | Get workspace |
| DELETE | `/api/v1/workspaces/{id}` | Delete workspace |
| POST | `/api/v1/workspaces/{id}/channels` | Attach a platform_account to the workspace (P0#4, idempotent UPSERT) |
| GET | `/api/v1/workspaces/{id}/channels` | List channels bound to the workspace |
| PATCH | `/api/v1/workspaces/{id}/channels/{accountId}` | Update a binding's `group_name` / `enabled` flag |
| DELETE | `/api/v1/workspaces/{id}/channels/{accountId}` | Detach a platform_account from the workspace |

## Posts

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/posts` | Create post |
| GET | `/api/v1/posts/{id}` | Get post |
| GET | `/api/v1/posts/workspace/{wid}` | List posts by workspace |
| POST | `/api/v1/posts/{id}/targets` | Add target to post |
| POST | `/api/v1/posts/{id}/schedule` | Schedule post |
| POST | `/api/v1/posts/publish` | Publish to single platform |
| POST | `/api/v1/posts/publish-all` | Publish to all connected accounts |

## Storage

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/storage/upload-url` | Request presigned upload URL |

## Internal /internal/v1 contract

Service-to-service endpoints for Velox (and future external integrations). NOT mounted under `/api/v1` because they use **Bearer-token auth**, NOT JWT/CSRF — Velox is trusted, no browser involvement, no user session. Reverse proxy must block public access to this prefix.

Base path: `/internal/v1`

| Method | Path | Description |
|--------|------|-------------|
| POST | `/internal/v1/destinations/{id}/validate` | Validate that a Velox-resolved destination is publishable (enabled, workspace active, platform_account not reauth_required) |
| POST | `/internal/v1/deliveries` | Accept a Velox delivery; idempotent by `Idempotency-Key` header + payload SHA (currently TBD; planned for Phase 2) |
| GET | `/internal/v1/deliveries/{id}` | Fetch delivery state (planned for Phase 2) |

### Authentication

Static shared secret loaded from `VELOX_API_TOKEN` env var on boot.

```
Authorization: Bearer <32-char-random-hex>
```

Constant-time compare (`crypto/subtle.ConstantTimeCompare`) on byte slices prevents timing-based token recovery.

### Error codes (Velox-specific — deviates from convention)

| Status | When |
|--------|------|
| `401 Unauthorized` | Authorization header missing OR malformed (not `Bearer <token>`, case-insensitive) |
| `403 Forbidden` | Authorization header well-formed but token mismatches |
| `503 Service Unavailable` | `VELOX_API_TOKEN` empty at process start (boot-time misconfiguration; operators should fix the env var) |

**Forward-compat note:** the 401-missing / 403-mismatch split is **Velox-specific**. Conventional API providers (GitHub, Stripe, AWS, Slack) return `401` for both cases; `403` there means "authenticated but lacks permission". A future provider that drops into `/internal/v1` (Dropbox is mentioned in the architecture doc) and expects standard HTTP semantics should opt back into `401`-for-both via a per-router configuration. See `pkg/api/internal_auth.go` for the implementation rationale.

### Response envelope

All auth failures return the standard JSON error envelope so callers get a uniform content type regardless of which path fired:

```json
{ "error": "missing or malformed Authorization header" }
```

Content-Type: `application/json` (NOT `text/plain`).

### Bootstrap requirements (operators)

- `VELOX_API_TOKEN` MUST be a 32-char random hex (16-byte secret); rotate via deploy-time env reroll
- Reverse proxy (Caddy / Cloudflare / nginx) MUST refuse public access to `/internal/v1/*`
- Docker Compose local: `instaedit-api` consumes the var via `internal/config.Config.VeloxAPIToken`
- Backend runtime: `VELOX_API_TOKEN` is a server-side secret loaded from the protected Compose environment file; never expose it through Vercel or the browser.

## Admin · Dead-letter job triage (Task 10/10 — operator runbook)

- `GET /admin/upload_jobs/dead_letter` — JSON list (up to 500 rows) of `upload_jobs` rows whose retry budget has been exhausted (`status='dead_letter'`). Returns `{count, jobs[], generated_at_unix}`. Each job row carries: `job_id`, `user_id`, `workspace_id`, `source_type`, `source_id`, `title`, `status`, `attempt_count`, `error_code`, `error_message`, `dead_lettered_at`. Auth: admin bearer JWT or admin API key; 401/403 for non-admin callers; 501 if the admin store is not wired.
- `GET /admin/upload_jobs/dead_letter.csv` — CSV companion; identical row shape + a single-row header so spreadsheet imports work without a manual header pre-pend.

The operator-facing row shape mirrors the JSON table-header in the response. `error_code` is one of the canonical taxonomy (`drive_error` / `s3_error` / `youtube_error` / `auth_error` / `timeout` / `""`) — see `internal/worker/upload_worker.go::classifyUploadError`.
