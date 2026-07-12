# InstaEditLogin — OpenAPI Equivalent

This document provides an OpenAPI-style description of the public API. A formal `openapi.yaml` can be generated from this file in the future.

## Info

- Title: InstaEditLogin API
- Version: 1.0.0
- Base URL: `/api/v1`

## Servers

- Local: `http://localhost:8080/api/v1`
- Staging: `https://api-staging.example.com/api/v1`
- Production: `https://api.example.com/api/v1`

## Authentication

Most endpoints require a `Authorization: Bearer <jwt>` header. The JWT is issued after OAuth callback.

## Common Responses

- `200 OK` — success
- `201 Created` — resource created
- `204 No Content` — deletion succeeded
- `400 Bad Request` — malformed request
- `401 Unauthorized` — missing or invalid JWT
- `403 Forbidden` — resource exists but not owned by caller
- `404 Not Found` — resource not found
- `422 Unprocessable Entity` — semantic validation error
- `500 Internal Server Error` — server error

## Paths

### GET /health

Returns service status and registered platforms.

### GET /auth/{provider}/login

Starts OAuth flow. Redirects to provider authorization URL.

### GET /auth/{provider}/callback

OAuth callback. Issues JWT and redirects to `FRONTEND_URL/auth/callback`.

### GET /accounts

List connected platform accounts for the authenticated user.

### POST /posts

Create a new post within a workspace.

Request body (Taglio 3.2 — `media_url` REMOVED, use `media: [{ asset_id }]`):
```json
{
  "workspace_id": 1,
  "content": {
    "title": "My post",
    "caption": "Hello world",
    "media": [{"asset_id": "00000000-0000-4000-8000-000000000001"}]
  },
  "scheduled_at": "2026-07-15T10:00:00Z",
  "targets": [{"platform_account_id": 1}]
}
```

The server resolves each `asset_id` to a verified internal S3 URL. Only
assets in status `ready` are accepted; missing / non-owned / expired /
not-ready assets produce 422.

### POST /posts/publish

Publish content to a single platform account. The `media` field is a
list of `asset_id` references (Taglio 3.2); the server never accepts a
user-controlled URL.

Request body:
```json
{
  "platform": "meta",
  "media": [{"asset_id": "00000000-0000-4000-8000-000000000001"}],
  "caption": "Hello",
  "content_type": "video"
}
```

### POST /media/presign  (Taglio 3.2)

Mint a presigned S3 PUT URL + a server-tracked `asset_id`. The
client PUTs the file to `upload_url`, then commits via
`/media/{asset_id}/complete`. **This is step 1 of 2** in the
presigned-upload flow.

Request body:
```json
{
  "filename": "my-photo.jpg",
  "content_type": "image/jpeg",
  "size_bytes": 524288,
  "sha256": "abc123... (optional)"
}
```

Response (200):
```json
{
  "asset_id": "00000000-0000-4000-8000-000000000001",
  "upload_url": "https://bucket.s3.amazonaws.com/uploads/1/uuid_my-photo.jpg?X-Amz-Signature=...",
  "upload_method": "PUT",
  "upload_headers": {"Content-Type": "image/jpeg"},
  "expires_at": "2026-07-12T18:30:00Z",
  "content_type": "image/jpeg",
  "max_size_bytes": 209715200
}
```

The client then `PUT`s the file to `upload_url` directly to S3 (no
file body traverses our server). Allowed content types:
`image/jpeg`, `image/png`, `image/webp`, `video/mp4`, `video/quicktime`.

### POST /media/{asset_id}/complete  (Taglio 3.2)

Commit a media asset. The server HEADs the S3 object to verify
size + content-type, then transitions the asset to `ready`. **This
is step 2 of 2** in the presigned-upload flow.

Response (200):
```json
{
  "id": "00000000-0000-4000-8000-000000000001",
  "user_id": 1,
  "upload_key": "uploads/1/uuid_my-photo.jpg",
  "content_type": "image/jpeg",
  "size_bytes": 524288,
  "status": "ready",
  "expires_at": "2026-07-13T18:15:00Z",
  "created_at": "2026-07-12T18:15:00Z",
  "updated_at": "2026-07-12T18:16:00Z"
}
```

Error cases:
- 404 — asset not found OR not owned by caller (no existence leak)
- 410 — asset expired (re-upload required)
- 400 — S3 object missing (client can retry)
- 422 — size or content-type mismatch (asset transitions to `failed`)
