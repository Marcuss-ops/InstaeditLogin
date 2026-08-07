# InstaEditLogin — OpenAPI Equivalent

This document provides the human-readable companion to the formal `api/openapi.yaml` specification. The canonical Velox job contract is documented in both places; keep endpoint and schema changes synchronized.

## Info

- Title: InstaEditLogin API
- Version: 1.0.0
- Base URL: `/api/v1`

## Servers

- Local direct binary: `http://localhost:8080/api/v1`
- Local `make dev` host binding: `http://localhost:8081/api/v1`
- Local Caddy single-origin proxy: `https://localhost:8443/api/v1`
- Staging: `https://api-staging.example.com/api/v1`
- Production: `https://api.example.com/api/v1`

## Authentication

Most endpoints require a `Authorization: Bearer <jwt>` header. The JWT is issued after OAuth callback.

## Canonical Velox jobs

### POST /jobs

With the `/api/v1` server base, this is `POST /api/v1/jobs`. It is the
single endpoint for asynchronous Velox render-job creation. The request
uses the stable `velox.job.v1` envelope:

```json
{
  "contract_version": "velox.job.v1",
  "idempotency_key": "five-boxers-it-001",
  "job_type": "scene.composite.v1",
  "template_id": "documentary.clip-stock",
  "template_version": 1,
  "video_name": "Five legendary boxers",
  "spec": {
    "scenes": [
      {
        "id": "intro",
        "text": "Cinque pugili leggendari",
        "assets": {
          "primary_clip": {"asset_id": "asset-clip-intro"}
        },
        "audio": {
          "voiceover": {"uri": "velox-asset://voiceover-intro"}
        },
        "timeline": {"duration_ms": 4000}
      }
    ]
  },
  "output": {"width": 1920, "height": 1080, "fps": 30, "format": "mp4"},
  "delivery_plan": {
    "destinations": [{"external_destination_id": "extdst_01J"}]
  }
}
```

`job_type` selects the technical registry entry and must be one of:

- `scene.composite.v1`
- `clip.stock.v1`
- `scene.image.v1`
- `slideshow.v1`

The first three use a closed `spec.scenes` schema; `slideshow.v1` uses a
closed `spec.images` schema. Unknown fields are rejected recursively with
`422 Unprocessable Entity`. `template_id` and `template_version` select the
editorial recipe; editorial templates never become URL paths or separate
endpoints. For the full recursive schema and all four examples, see
`api/openapi.yaml` (`VeloxSceneSpec`, `VeloxSlideshowSpec`, and the
`Velox*Job` examples).

The former migration-only `POST /api/v1/velox/jobs` route has been removed.
All producers must use `POST /api/v1/jobs`.

### Canonical job responses

- `202 Accepted` — job queued for asynchronous rendering
- `409 Conflict` — idempotency conflict from the same key with a different payload
- `422 Unprocessable Entity` — invalid envelope, unknown `job_type`, invalid typed spec, or unknown nested field

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

### Workspace and post validation contract

For the `/workspaces` and `/posts` handlers, keep malformed and semantic
validation distinct:

- **400 Bad Request** means the JSON body cannot be parsed or a parsed value
  is invalid, such as an unknown post status.
- **422 Unprocessable Entity** means valid JSON is missing a required semantic
  field, such as `name`, `workspace_id`, `targets`, or a target's
  `platform_account_id`.

Examples:

| Request | Response |
|---|---|
| `POST /workspaces` with `{}` | 422, `name is required` |
| `POST /workspaces` with `not json` | 400, invalid request body |
| `POST /posts` with `{"title":"x"}` | 422, `workspace_id is required` |
| `POST /posts` with `{"workspace_id":1}` | 422, at least one target is required |
| `POST /posts` with `targets:[{"platform_account_id":0}]` | 422, target platform account is required |
| `POST /posts` with an invalid `status` | 400, status must be one of the supported values |

The SPA uses 422 for form correction and 400 for an integration/payload bug;
do not collapse these response classes. The contract is locked by
`TestHandleCreateWorkspace_MissingName_422` in
`pkg/api/workspace_routes_test.go`,
`TestHandleCreatePost_MissingWorkspaceID_422`,
`TestHandleCreatePost_NoTargets_422`, and
`TestHandleCreatePost_BadTargetID_422` in `pkg/api/post_routes_test.go`, plus
`TestPostsAPI_Create_BadStatus_400` in `pkg/api/posts_test.go`.

Protected endpoints derive identity only from the authenticated JWT context
(Bearer header or HttpOnly `session` cookie). They must not accept `user_id`
from the request body or query string. The shared `requireUserID` helper is the
first authorization step for workspaces, posts, publishing, accounts, and
storage handlers.

### Create-post response compatibility

`POST /posts` returns both the flat post fields and a nested `post` object,
plus `targets`, for compatibility with the existing flat and nested decoders:

```json
{
  "id": 100,
  "workspace_id": 1,
  "title": "hello",
  "caption": "world",
  "media_url": "",
  "scheduled_at": null,
  "status": "draft",
  "post": {
    "id": 100,
    "workspace_id": 1,
    "title": "hello",
    "caption": "world",
    "media_url": "",
    "scheduled_at": null,
    "status": "draft",
    "created_at": "2024-01-01T00:00:00Z"
  },
  "targets": [
    {"id": 200, "post_id": 100, "platform_account_id": 10, "status": "scheduled"}
  ]
}
```

The flat fields and nested `post` represent the same resource. `media_url`
remains in the response for compatibility even though new create requests use
server-resolved media assets. If this shape changes, update the flat and nested
handler tests together; do not simplify it to only one shape without an
explicit API migration.

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
