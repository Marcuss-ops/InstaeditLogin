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

Request body:
```json
{
  "workspace_id": 1,
  "title": "My post",
  "caption": "Hello world",
  "media_url": "https://...",
  "scheduled_at": "2026-07-15T10:00:00Z",
  "targets": [{"platform_account_id": 1}]
}
```

### POST /posts/publish

Publish content to a single platform account.

Request body:
```json
{
  "platform": "meta",
  "media_url": "https://...",
  "caption": "Hello",
  "content_type": "video"
}
```
