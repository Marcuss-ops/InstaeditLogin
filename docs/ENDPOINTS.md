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
