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
| GET | `/api/v1/accounts` | List connected platform accounts |

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
- Production (Fly): var is a secret; set via `flyctl secrets set VELOX_API_TOKEN=...` (see `scripts/verify-fly-secrets.sh`)
