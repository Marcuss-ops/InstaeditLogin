# Velox → InstaEdit: JSON Publishing Contract

Authoritative wire-level specification for the publish flow that runs
**PipelineGen → Velox → InstaeditLogin → YouTube**, with the Thumbnail
Maker sitting between private upload and final public transition.

This document binds **three contracts in one place**:

1. The `publishing` intent block stamped on a PipelineGen job by the
   editorial pipeline.
2. The producer-side payload Velox forwards over the
   `/internal/v1/*` boundaries documented in
   [`ENDPOINTS.md`](./ENDPOINTS.md).
3. The consumer-advertised state machine Velox polls back to reconcile
   the social leg of the job.

> **Single source of truth.** Any drift between this file, the Go
> handlers under `pkg/api/internal_velox.go`, and `api/openapi.yaml`
> must be resolved by editing this file first, then propagating the
> change. The OpenAPI document is generated from this contract.

---

## 1. Scope and boundaries

### Producer responsibilities (Velox)

- Decide editorial intent: **title, description, tags, privacy,
  thumbnail requirement**.
- Resolve the source artifact post-render: `job_id`, `task_id`,
  `artifact_id`.
- Resolve the publishing target using **stable identifiers**
  (`workspace_id` + `platform_account_id`, OR `workspace_id` +
  `group_id`). Display names (channel title strings) are never the
  primary key.
- Execute the three HTTP calls against `/internal/v1/*` documented in
  [`ENDPOINTS.md`](./ENDPOINTS.md).
- Poll `GET /internal/v1/deliveries/{id}` until the social leg
  reaches a terminal state.

### Consumer responsibilities (InstaEdit)

- Hold OAuth tokens in the encrypted vault. **Never** leak tokens,
  refresh tokens, cookies, client secrets, signed URLs back to Velox.
- Perform the YouTube upload with `privacyStatus=private`.
- Provision the thumbnail-editor session.
- Apply the thumbnail via the `thumbnails.set` scope using the
  channel-bound token.
- Transition the video from `private` → `public|unlisted|scheduled`
  **only** after the thumbnail is applied and the channel binding
  re-verifies.

### Hard safety rule

> From `PRIVATE_UPLOADED` onward, **any** failure must leave the
> video in `privacy=private`. The video is never published without a
> thumbnail, and never on a channel other than the one explicitly
> requested by `platform_account_id` or the resolved `group_id`.

Both repos refuse to operate in any path that violates this invariant.
The state machine in §10 enforces it on the consumer; the validate
endpoint (§6) enforces it on the producer side before any upload is
attempted.

---

## 2. Publishing intent (PipelineGen → Velox)

PipelineGen stamps a `publishing` block on each job. Velox reads it
verbatim and forwards the relevant fields downstream to InstaEdit.

### 2.1 Single channel

```json
{
  "publishing": {
    "platform": "youtube",
    "workspace_id": 12,
    "target": {
      "type": "channel",
      "platform_account_id": 381
    },
    "initial_privacy": "private",
    "final_privacy": "public",
    "require_thumbnail": true
  }
}
```

### 2.2 Group of channels

```json
{
  "publishing": {
    "platform": "youtube",
    "workspace_id": 12,
    "target": {
      "type": "group",
      "group_id": 27
    },
    "initial_privacy": "private",
    "final_privacy": "public",
    "require_thumbnail": true
  }
}
```

A group expands deterministically into **N independent deliveries**,
one per active, enabled, reauth-clean account. Velox does **not** pick
a "best" channel; it sends all of them. InstaEdit reports `PARTIAL` if
any child fails — see §12.

### 2.3 Field reference

| Field                                     | Type                              | Required | Notes                                                                                                |
| ----------------------------------------- | --------------------------------- | -------- | ---------------------------------------------------------------------------------------------------- |
| `platform`                                | `"youtube"`                       | yes      | Reserved for future: `tiktok`, `meta`, `linkedin` are accepted but consumer handlers reject for now. |
| `workspace_id`                            | int64                             | yes      | Owns the channel + the source artifact. Cross-workspace ID is not allowed.                           |
| `target.type`                             | `"channel"` \| `"group"`          | yes      | Determines which sibling field is required.                                                          |
| `target.platform_account_id`              | int64                             | conditional | Required when `type=channel`.                                                                          |
| `target.group_id`                         | int64                             | conditional | Required when `type=group`.                                                                           |
| `initial_privacy`                         | `"private"`                       | yes      | Always `private`. Hard-coded invariant. Any other value is a producer-side bug.                      |
| `final_privacy`                           | `"public"` \| `"unlisted"` \| `"private"` | yes      | Settled-state privacy of the published video.                                                          |
| `require_thumbnail`                       | bool                              | yes      | If `true`, `READY_TO_PUBLISH → PUBLISHING → PUBLISHED` is blocked until `thumbnails.set` succeeds.  |
| `publish_at` *(optional scheduling hint)* | RFC3339 datetime                  | no       | If set, the consumer schedules `videos.update` for that instant without passing through `public`.   |

> **Display names are not allowed.** `"channel": "Wrestling Discovery"`
> was the loose way the team used to identify channels. It is rejected:
> two workspaces can both have a "Wrestling Discovery" channel, and
> channel titles can be renamed by the operator mid-flight.

---

## 3. Delivery provenance fields

These values are carried as top-level fields of the canonical flat
`VeloxDeliverArtifactRequest`; `source` is not a nested HTTP envelope.
The actual bytes travel over the artifact's `download_url`.

```json
{
  "external_delivery_id": "delivery_789",
  "idempotency_key": "delivery_789|destination_12",
  "external_destination_id": "extdst_01JABC",
  "artifact": { "artifact_id": "artifact_abc" }
}
```

| Field                       | Type   | Required | Notes                                                                            |
| --------------------------- | ------ | -------- | -------------------------------------------------------------------------------- |
| `external_delivery_id`      | string | yes      | Stable producer delivery identifier.                                             |
| `idempotency_key`           | string | yes      | Stable across retries; the request body is hashed for replay protection.         |
| `external_destination_id`   | string | yes      | Opaque destination resolved by InstaEdit.                                         |
| `artifact.artifact_id`      | string | yes      | Rendered artifact identifier used for ingest and audit correlation.               |

---

## 4. Artifact fields (artifact transport)

The canonical flat request places transport fields under `artifact`.

```json
{
  "artifact": {
    "artifact_id": "artifact_abc",
    "download_url": "https://velox.example/internal/artifacts/abc?token=...&expires=...",
    "sha256": "cc3cfb49...",
    "size_bytes": 1915469,
    "mime_type": "video/mp4"
  }
}
```

| Field             | Type        | Required | Notes                                                                                              |
| ----------------- | ----------- | -------- | -------------------------------------------------------------------------------------------------- |
| `download_url`    | HTTPS URL   | yes      | Must be HTTPS, expiring, signed, reachable only by InstaEdit. **Never** a worker-local path.        |
| `sha256`          | lowercase hex | yes   | SHA-256 of the raw bytes; lowercase hex; no `sha256:` prefix.                                       |
| `size_bytes`      | int64       | yes      | Strict equality required on ingest. Mismatch → `MEDIA_INVALID`.                                    |
| `mime_type`       | string      | yes      | Currently `video/mp4`. Consumer rejects anything else.                                              |
| `duration_seconds`| —           | no       | Not part of the canonical flat delivery DTO; duration probing is performed by the consumer.         |

### 4.1 Verification chain

1. HTTPS `GET` on `download_url`. Reject HTTP, self-signed, expired,
   or unreachable in <5 s → `MEDIA_INVALID`.
2. `len(bytes) == size_bytes`. Strict equality.
3. `sha256(bytes) == sha256`. Constant-time compare to avoid timing
   leak on the integrity check.
4. `ffprobe -show_format` → `format_name=mp4`,
   `duration ≈ duration_seconds` (within ±0.5 s).
5. Optional width/height sanity vs. publish minimums.

Any failure transitions the delivery to `MEDIA_INVALID`. The video is
**not** uploaded.

### 4.2 Producer download URL constraints

- HTTP**S** only (TLS 1.2+).
- Expiring bearer token in the query string or signed header.
- Expiry ≤ 1 hour from issue.
- Reachable only from the InstaEdit VPC / private network
  (allowlist at the Velox proxy).
- No use of query-string names that would land in a stack trace:
  the URL is logged stripped (token-bearing parts removed).
- No IAM/cookie that survives across deliveries. Each URL is
  single-use in spirit; safe-by-replay via the Idempotency-Key.

---

## 5. Publication metadata

Publication fields are carried in the flat request's `metadata` JSON
object, alongside the artifact and opaque destination identifiers.

```json
{
  "metadata": {
    "title": "Titolo del video",
    "description": "Descrizione del video",
    "tags": ["wwe", "wrestling"],
    "privacy_status": "private"
  },
  "publish_at": "2026-07-30T18:00:00Z"
}
```

| Field                   | Type                                  | Required | Notes                                                                                              |
| ----------------------- | ------------------------------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `metadata.title`        | string ≤100                           | yes      | YouTube title; producer supplies the value in the metadata object.                                |
| `metadata.description`  | string ≤5000                          | yes      | YouTube description; producer supplies the value in the metadata object.                          |
| `metadata.tags`         | string\[\]                            | no       | Joined with `,` on upload; bounded by the consumer metadata validator.                             |
| `metadata.privacy_status` | `"private"` \| `"unlisted"` \| `"public"` | yes | Current publisher privacy intent; acceptance starts from private.                              |
| `publish_at`            | RFC3339 datetime                      | no       | Optional scheduling hint forwarded to the publisher.                                               |

---

## 6. `POST /internal/v1/destinations/{id}/validate`

Dry-run probe to confirm a workspace + target is publishable **before**
Velox commits to the artifact transport. The default response code is
`204` (Velox consumes status only); JSON opt-in via
`?diagnostic=true` or `X-Velox-Diagnostic: true` returns `200` with the
shape below.

### 6.1 Channel — request

```http
POST /internal/v1/destinations/instaedit_youtube/validate
Authorization: Bearer <VELOX_API_TOKEN>
Content-Type: application/json

{
  "workspace_id": 12,
  "platform": "youtube",
  "target": { "type": "channel", "platform_account_id": 381 }
}
```

### 6.2 Group — request

```http
POST /internal/v1/destinations/instaedit_youtube/validate
Authorization: Bearer <VELOX_API_TOKEN>
Content-Type: application/json

{
  "workspace_id": 12,
  "platform": "youtube",
  "target": { "type": "group", "group_id": 27 }
}
```

### 6.3 Channel — happy response (HTTP 200, `?diagnostic=true`)

```json
{
  "valid": true,
  "destination_id": "instaedit_youtube",
  "resolved_targets": [
    {
      "platform_account_id": 381,
      "platform": "youtube",
      "channel_id": "UCxxxxxxxx",
      "channel_name": "Wrestling Discovery",
      "status": "active",
      "enabled": true
    }
  ]
}
```

### 6.4 Group — happy response (HTTP 200, `?diagnostic=true`)

```json
{
  "valid": true,
  "resolved_targets": [
    { "platform_account_id": 381, "platform": "youtube", "channel_id": "UC111" },
    { "platform_account_id": 442, "platform": "youtube", "channel_id": "UC222" },
    { "platform_account_id": 519, "platform": "youtube", "channel_id": "UC333" },
    { "platform_account_id": 605, "platform": "youtube", "channel_id": "UC444" }
  ]
}
```

### 6.5 Invalid response (HTTP 422)

```json
{
  "valid": false,
  "error_code": "TARGET_NOT_AVAILABLE",
  "message": "The requested channel is not connected or enabled"
}
```

`error_code` ∈ `TARGET_NOT_AVAILABLE`, `WORKSPACE_INACTIVE`,
`GROUP_EMPTY`, `CHANNEL_DISABLED`, `ACCOUNT_REAUTH_REQUIRED`,
`TOKEN_REVOKED`. See §11 for the catalogue.

### 6.6 Producer-side rules (Velox contract)

If `valid=false`, Velox **MUST**:

- abort the delivery for that target;
- never auto-pick a similar channel;
- never fall back to a default account;
- never silently switch group.

### 6.7 Wire-shape convention (legacy vs body-based)

Two validate-style endpoints live in the namespace and serve
different purposes; their HTTP semantics differ by design.

| Endpoint                                         | Happy path            | Failure path        | Returns body by default? |
| ------------------------------------------------ | --------------------- | ------------------- | ------------------------ |
| `POST /internal/v1/destinations/{id}/validate`   | `204 No Content`      | `404 Not Found`     | Only on `?diagnostic=true` opt-in |
| `POST /internal/v1/destinations/resolve-target`  | `200 OK` + JSON       | `422 Unprocessable` | Always                   |

The legacy `/{id}/validate` is internal-only and used by Velox as
a quick status probe (fire-and-forget; the response code is all the
producer needs). The newer `/resolve-target` is a body-based
pre-flight gate that must return diagnostics on every call
because Velox uses the response to populate the operator UI's
channel/group resolver view; requiring an opt-in flag there would
round-trip an extra HTTP call.

Both endpoints share the same `error_code` taxonomy listed in §11.

---

## 7. `POST /internal/v1/deliveries`

Producer-side accept of a rendered artifact. Idempotent on
`Idempotency-Key` (mandatory header). SLA target 500 ms p99.

### 7.1 Request

The endpoint accepts exactly one envelope: the flat artifact request
with the explicit `contract_version` discriminator. The retired
`velox-instaedit.v1` nested `source`/`media`/`destination`/`publication`
envelope and an omitted version are rejected with `422`.

```http
POST /internal/v1/deliveries
Authorization: Bearer <VELOX_API_TOKEN>
Idempotency-Key: delivery_789|destination_12
Content-Type: application/json

{
  "contract_version": "velox.delivery.v1",
  "external_delivery_id": "delivery_789",
  "idempotency_key": "delivery_789|destination_12",
  "external_destination_id": "extdst_01JABC",
  "artifact": {
    "artifact_id": "artifact_abc",
    "sha256": "cc3cfb49...",
    "size_bytes": 1915469,
    "mime_type": "video/mp4",
    "download_url": "https://velox.example/internal/artifacts/abc?token=...&expires=..."
  },
  "metadata": {
    "title": "Titolo del video",
    "description": "Descrizione del video",
    "tags": ["wwe", "wrestling"],
    "privacy_status": "private"
  },
  "publish_at": "2026-07-30T18:00:00Z"
}
```

### 7.2 Idempotency-Key format

```
velox-<job_id>-<artifact_id>-<platform>-<account|group>
```

Examples:

```
velox-job_123-artifact_abc-youtube-account_381
velox-job_123-artifact_abc-youtube-group_27
```

Producer MUST keep the key stable across retries. Consumer computes
`sha256(raw_body)` and INSERTs under a `pg_advisory_xact_lock` so
concurrent replays serialise.

- Same key + same body SHA → existing row reused; `already_exists=true`.
- Same key + different body → `409 idempotency_key_conflict`.
- Different key + same body → fresh insertion.

### 7.3 Fresh insertion — response (HTTP 202)

```json
{
  "social_delivery_id": "sdel_01JABC",
  "status": "accepted",
  "already_exists": false
}
```

### 7.4 Replay — response (HTTP 202)

```json
{
  "social_delivery_id": "sdel_01JABC",
  "status": "accepted",
  "already_exists": true
}
```

### 7.5 Scheduled — response (HTTP 202)

```json
{
  "social_delivery_id": "sdel_01JDEF",
  "status": "accepted",
  "already_exists": false
}
```

---

## 8. `GET /internal/v1/deliveries/{id}`

Polled by Velox while the social leg progresses. Render success and
publish success are reported separately — the consumer enforces this
separation on the producer side.

```json
{
  "delivery_id": "delivery_789",
  "velox_job_id": "job_123",
  "target": {
    "platform_account_id": 381,
    "channel_id": "UCxxxxxxxx",
    "channel_name": "Wrestling Discovery"
  },
  "status": "THUMBNAIL_PENDING",
  "youtube_video_id": "AbCd1234",
  "privacy": "private",
  "thumbnail_status": "pending",
  "publish_status": "waiting_thumbnail",
  "updated_at": "2026-07-29T08:58:00Z"
}
```

Field reference:

| Field              | Type                                                      | Notes                                                                  |
| ------------------ | --------------------------------------------------------- | ---------------------------------------------------------------------- |
| `delivery_id`      | string                                                    | Opaque; matches the value returned by §7.                              |
| `velox_job_id`     | string                                                    | Mirrors the producer's external delivery correlation.                  |
| `target.platform_account_id` | int64                                            | Resolved account (never a group aggregate id).                         |
| `target.channel_id` | string                                                   | YouTube channel id actually bound to the token used for upload.       |
| `target.channel_name` | string                                                 | Diagnostic only; subject to rename. Not a selection key.               |
| `status`           | enum (see §10)                                            | Canonical delivery state machine value.                                |
| `youtube_video_id` | string \| null                                            | Present from `PRIVATE_UPLOADED` onward.                                |
| `privacy`          | `"private"` \| `"unlisted"` \| `"public"` \| null          | Read-back of YouTube-side actual value.                                |
| `thumbnail_status` | `"skipped"` \| `"pending"` \| `"applied"` \| `"failed"`   | Driven by the editor-session lifecycle.                                |
| `publish_status`   | `"waiting_thumbnail"` \| `"ready_to_publish"` \| `"scheduled"` \| `"published"` \| `"failed"` \| `"cancelled"` | Substate of the publish leg. |
| `updated_at`       | RFC3339 datetime                                          | Server-side commit time.                                               |

---

## 9. Thumbnail session contract

After `PRIVATE_UPLOADED`, InstaEdit provisions a session bound to the
uploaded video. The InstaEditor thumbnail-editor SPA consumes only
the session handle. OAuth tokens never reach the browser.

### 9.1 Session payload (returned to the Thumbnail Maker)

The auto-provisioner (`POST /internal/v1/thumbnail-sessions`) hands the
Thumbnail Maker the session **handle** (`editor_session_id`,
`velox_project_id`, `editor_url`). The editor then loads the full
session document from `GET
/api/v1/youtube/editor-sessions/by-project/{velox_project_id}`
(by-id variant: `GET /api/v1/youtube/editor-sessions/{id}`). That
document is the payload below — the **extended session contract**:
besides the session identity it carries the authoritative YouTube
projection (`thumbnail_url`, `category_id`, `privacy_status`) that
InstaEditor renders as its initial canvas, so the editor never needs
its own YouTube/OAuth access.

```json
{
  "id": "ytedit_123",
  "workspace_id": 12,
  "platform_account_id": 381,
  "channel_id": "UCxxxxxxxx",
  "youtube_video_id": "AbCd1234",
  "velox_project_id": "ve_123",
  "editor_url": "https://editor.example.com/editor/ve_123",
  "source_thumbnail_url": "https://i.ytimg.com/vi/AbCd1234/hqdefault.jpg",
  "thumbnail_url": "https://i.ytimg.com/vi/AbCd1234/hqdefault.jpg",
  "category_id": "24",
  "privacy_status": "private",
  "thumbnail_media_id": null,
  "desired_privacy": "private",
  "publish_at": null,
  "status": "editing",
  "last_error": "",
  "actual_privacy": null,
  "youtube_sync_status": null,
  "draft_title": "Titolo del video",
  "draft_description": "",
  "created_at": "2026-07-29T08:58:00Z",
  "updated_at": "2026-07-29T08:58:00Z"
}
```

Field reference (extended-contract fields first):

| Field                  | Type            | Notes                                                                                                      |
| ---------------------- | --------------- | ---------------------------------------------------------------------------------------------------------- |
| `thumbnail_url`        | string          | Extended contract wire name — mirrors `source_thumbnail_url`; the editor's initial canvas.                  |
| `category_id`          | string          | YouTube video category stamped at session creation from `videos.list`; omitted when unset.                   |
| `privacy_status`       | `"public"` \| `"unlisted"` \| `"private"` | The **single** visibility value the editor renders: `actual_privacy` read-back when the publish orchestrator stamped it, `desired_privacy` fallback otherwise. |
| `id`                   | string          | Session id: `ytedit_<uuid>` auto-provisioned, bare uuid when manually created.                              |
| `workspace_id`         | int64           | Owning workspace.                                                                                          |
| `platform_account_id`  | int64           | Owning YouTube account.                                                                                    |
| `channel_id`           | string          | YouTube channel id bound to the account (diagnostic).                                                      |
| `youtube_video_id`     | string          | The uploaded video the session edits.                                                                      |
| `velox_project_id`     | string          | InstaEditor project handle — the `/editor/{velox_project_id}` URL segment.                                 |
| `editor_url`           | string          | Launcher URL; empty when the editor base is unconfigured.                                                  |
| `source_thumbnail_url` | string          | Persisted original YouTube thumbnail (fallback for un-rendered covers).                                    |
| `thumbnail_media_id`   | string \| null  | Attached thumbnail asset after apply; null before the editor exports.                                      |
| `desired_privacy`      | `"public"` \| `"unlisted"` \| `"private"` | Operator's intended visibility on the session row (from `final_privacy` / editor panel).                   |
| `actual_privacy`       | string \| null  | YouTube read-back after publish; null = not published / read-back not done yet.                             |
| `youtube_sync_status`  | string \| null  | `pending` \| `confirmed` \| `drift` \| `failed` lifecycle marker (colours the privacy badge).             |
| `publish_at`           | RFC3339 \| null | Scheduled publish instant (video stays `private` until then).                                              |
| `status`               | string          | Session lifecycle: `editing` \| `failed` \| `publishing` \| `published`.                                  |
| `draft_title` / `draft_description` | string \| null | Auto-save draft; NULL = no draft yet, empty string = intentionally cleared.             |
| `last_error`           | string          | Operator hint for the dashboard's failure copy (internal diagnostics).                                     |
| `created_at` / `updated_at` | RFC3339    | Server-side commit timestamps.                                                                             |

Semantics:

- The extended fields are **server-derived** — `thumbnail_url` and
  `category_id` come from the channel's own `videos.list` response,
  `privacy_status` from the publish read-back — never from
  client-supplied values.
- `privacy_status` is the resolved projection, distinct from both
  `desired_privacy` (intent) and `actual_privacy` (raw read-back).
- OAuth tokens never reach the browser: the Thumbnail Maker only ever
  sees this document.

### 9.2 `POST /api/v1/youtube/editor-sessions/{id}/thumbnail`

```json
{
  "thumbnail_url": "https://storage.example/thumbnails/thumb_123.jpg",
  "sha256": "92aa...",
  "width": 1280,
  "height": 720
}
```

Accepted dimensions: 1280×720 (16:9) or 1920×1080. Max weight 2 MB.
JPEG/PNG only. Consumer performs `thumbnails.set` over the YouTube
Data API v3 with the channel-bound token.

### 9.3 `POST /api/v1/youtube/editor-sessions/{id}/publish`

Immediate:

```json
{ "final_privacy": "public" }
```

Scheduled:

```json
{ "final_privacy": "private", "publish_at": "2026-07-30T18:00:00Z" }
```

Consumer MUST verify:

1. Thumbnail applied.
2. Account still active.
3. Token refreshed if needed.
4. `youtube_video_id` still resolves to a real video.
5. Channel binding re-verified (token still maps to the requested
   channel).
6. `videos.update` succeeded.
7. Read-back: actual privacy matches the requested final.

---

## 10. State matrix

Single canonical state machine across both repos. Render success on
Velox and publish state on InstaEdit are tracked separately — see §8.

### 10.1 Forward transitions

| From                   | To                       | Trigger                                                                                                   |
| ---------------------- | ------------------------ | --------------------------------------------------------------------------------------------------------- |
| `DELIVERY_QUEUED`      | `TARGET_VALIDATING`      | Velox accepts the delivery via §7.                                                                        |
| `TARGET_VALIDATING`    | `TARGET_VALIDATED`       | §6 returned `valid=true`.                                                                                 |
| `TARGET_VALIDATED`     | `MEDIA_DOWNLOADING`      | Worker claims the row.                                                                                    |
| `MEDIA_DOWNLOADING`    | `MEDIA_VERIFIED`         | `sha + size + ffprobe` chain in §4.1 matches.                                                              |
| `MEDIA_VERIFIED`       | `PRIVATE_UPLOAD_QUEUED`  | Resumable YouTube upload created.                                                                          |
| `PRIVATE_UPLOAD_QUEUED`| `PRIVATE_UPLOADING`      | First byte sent.                                                                                          |
| `PRIVATE_UPLOADING`    | `PRIVATE_UPLOADED`       | YouTube returns 2xx + `youtube_video_id`. **Invariant**: from here on, errors MUST NOT change `privacy`. |
| `PRIVATE_UPLOADED`     | `THUMBNAIL_PENDING`      | Editor session created.                                                                                    |
| `THUMBNAIL_PENDING`    | `THUMBNAIL_UPLOADING`    | Thumbnail Maker uploads bytes.                                                                             |
| `THUMBNAIL_UPLOADING`  | `THUMBNAIL_APPLIED`      | `thumbnails.set` returned 2xx.                                                                             |
| `THUMBNAIL_APPLIED`    | `READY_TO_PUBLISH`       | Channel binding + token still valid; `require_thumbnail==true` satisfied.                                 |
| `READY_TO_PUBLISH`     | `PUBLISHING`             | Publish call in flight.                                                                                    |
| `PUBLISHING`           | `PUBLISHED`              | `videos.update` 2xx, read-back matches.                                                                    |
| `PUBLISHING`           | `PUBLISHED` *(scheduled)*| `publish_at` set; video still `private` until that instant.                                                |

### 10.2 Private-status matrix (YouTube read-back)

| Delivery state       | Allowed `privacy` on YouTube              |
| -------------------- | ---------------------------------------- |
| Pre-`PRIVATE_UPLOADED` | not yet on YouTube                     |
| `PRIVATE_UPLOADED → THUMBNAIL_PENDING` | `private`                         |
| `THUMBNAIL_PENDING → THUMBNAIL_APPLIED` | `private`                         |
| `READY_TO_PUBLISH`   | `private`                                 |
| `PUBLISHING`         | `private` (transient; toggles on success) |
| `PUBLISHED` (immediate) | `final_privacy` from the publication block |
| `PUBLISHED` (scheduled) | `private` until `publish_at`, then `final_privacy` |

### 10.3 Error matrix — guarded leaves

| State at error            | Allowed terminal            | Allowed `privacy` change? |
| ------------------------- | --------------------------- | ------------------------- |
| Pre-`PRIVATE_UPLOADED`    | `BLOCKED_TARGET`, `BLOCKED_AUTH`, `MEDIA_INVALID`, `PRIVATE_UPLOAD_FAILED`, `CANCELLED` | yes (video not yet on YouTube) |
| Post-`PRIVATE_UPLOADED`   | `THUMBNAIL_FAILED`, `PUBLISH_FAILED` | **NO.** `privacy` must remain `private`. |

The consumer publishes a `cancelled` row only if the producer explicitly
requested cancellation **before** `PRIVATE_UPLOADED`. After that point
the video is private-frozen; only the InstaEdit admin console or a
reauth by the operator can move it.

---

## 11. Error catalogue (canonical taxonomy)

| `error_code`                  | HTTP | When                                                                                            |
| ----------------------------- | ---- | ----------------------------------------------------------------------------------------------- |
| `TARGET_NOT_AVAILABLE`        | 422  | Workspace/channel unknown, disabled, or not in this destination.                                |
| `WORKSPACE_INACTIVE`          | 422  | Workspace archived or suspended.                                                                 |
| `GROUP_EMPTY`                 | 422  | `group_id` resolves to zero active accounts.                                                    |
| `CHANNEL_DISABLED`            | 422  | Account present but operator disabled it (`enabled=false`).                                     |
| `ACCOUNT_REAUTH_REQUIRED`     | 422  | YouTube grant revoked since last refresh. Operator must re-link.                                |
| `TOKEN_REVOKED`               | 403  | Refresh token permanently invalid.                                                               |
| `MEDIA_INVALID`               | 422  | `sha / size / mime / duration` mismatch on download.                                           |
| `IDEMPOTENCY_BODY_CHANGED`    | 409  | Replay key reused with a different body SHA-256.                                                |
| `CHANNEL_BINDING_MISMATCH`    | 422  | OAuth token resolves to a different YouTube channel than `platform_account_id`. Treat as suspect. |
| `MISSING_AUTH`                | 401  | `Authorization` header absent or malformed.                                                     |
| `FORBIDDEN_AUTH`              | 403  | `VELOX_API_TOKEN` mismatched (constant-time compare).                                            |
| `VELOX_API_TOKEN_UNCONFIGURED`| 503  | Boot-time misconfiguration; operator action required.                                            |

---

## 12. Behavioural rules summary

| Situation                           | Behaviour                                                  |
| ----------------------------------- | ---------------------------------------------------------- |
| Channel not connected               | `BLOCKED_TARGET`; no upload attempted.                     |
| Channel disabled                    | `BLOCKED_TARGET`; no upload attempted.                     |
| Token expired but refresh succeeds  | Continue silently.                                         |
| Token revoked                       | `BLOCKED_AUTH`; no upload attempted.                       |
| Group unknown                       | `BLOCKED_TARGET`.                                          |
| Group empty                         | `BLOCKED_TARGET`.                                          |
| Two channels with same name         | Reject; never auto-pick.                                   |
| One channel in group fails          | Overall delivery `PARTIAL`.                                |
| Thumbnail fails                     | Video stays `private`.                                     |
| Publish fails                       | Video stays `private`.                                     |
| Velox timeout / replay              | Idempotent; no duplicate upload.                           |
| `videos.update` returns non-final   | Read-back mismatch; treat as `PUBLISH_FAILED`.             |
| Channel-binding mismatch            | `BLOCKED_AUTH` + alert; mark account `reauth_required`.    |

`PARTIAL` semantics: at least one child delivery reached
`PUBLISHED` and at least one reached a terminal error. `SUCCEEDED`
requires **all** children `PUBLISHED`.

---

## 13. Security

This contract is internal-only. The reverse proxy (Caddy / Cloudflare
/ nginx) **MUST** refuse public access to `/internal/v1/*`.

- `VELOX_API_TOKEN` MUST be ≥ 32 chars of hex (16 bytes secret).
  Compared constant-time via `crypto/subtle.ConstantTimeCompare`.
- Distinct tokens per environment (dev / staging / prod). Rotation is
  a deploy-time env reroll.
- `Idempotency-Key` mandatory on `POST /internal/v1/deliveries`.
  Both producer and consumer MUST persist keys in audit logs.
- Timestamp + HMAC signature: optional today, mandatory when mTLS
  lands. Server rejects requests whose timestamp diverges > ±300 s
  from server `Date` now.
- Anti-replay window enforced via `Idempotency-Key` cache with TTL
  tied to the producer's retry budget.
- **Never** log OAuth tokens, refresh tokens, signed URLs, cookies,
  client secrets. Sanitiser strips `token=`, `signature=`, `Cookie`
  headers from outgoing logs.
- Audit log shape (every validate, upload, thumbnail, publish):

  ```json
  {
    "ts": "2026-07-29T09:00:00Z",
    "actor": "velox",
    "correlation_id": "corr_01j...",
    "action": "PUBLISH|VALIDATE|UPLOAD|THUMBNAIL_APPLY|...",
    "delivery_id": "delivery_789",
    "workspace_id": 12,
    "platform_account_id": 381,
    "result": "ok|error",
    "error_code": "..."
  }
  ```

### Roadmap

- Replace static `VELOX_API_TOKEN` with short-lived JWT M2M
  (rotation via Vault) or mTLS, while keeping the same central
  middleware in `pkg/api/internal_auth.go`.
- Same key/secret swap must be possible without service restart
  (control-plane reload).
- Drop `?diagnostic=true` opt-in once publishing call-graph is
  stable enough to keep the diagnostic block on by default.

---

## 14. Cross-references

- [`InstaeditLogin/docs/ENDPOINTS.md`](./ENDPOINTS.md) — full HTTP
  surface, auth + status codes.
- [`InstaeditLogin/docs/INTEGRATIONS.md`](./INTEGRATIONS.md) —
  locked-in behaviour over time (HMAC callback, dead-letter path).
- [`InstaeditLogin/api/openapi.yaml`](../api/openapi.yaml) — wire-level
  schemas (regenerated from this contract).
- [`InstaeditLogin/docs/SECURITY_RUNBOOK.md`](./SECURITY_RUNBOOK.md) —
  auth/incident procedures.
- `VeloxEditiingg/docs/pipeline.md` — producer-side flow.

---

## 15. Changelog

- **2026-08-14** — §9.1 aligned to the extended editor-session
  contract (`b317f3ef`): the session payload is now the full
  `youTubeEditorSessionDetail` document served by
  `GET /editor-sessions/by-project/{velox_project_id}` and
  `GET /editor-sessions/{id}`, carrying `thumbnail_url`,
  `category_id` and `privacy_status` (server-derived from
  `videos.list` / the publish read-back; `privacy_status` resolves
  actual-over-desired). The legacy flattened example
  (`video_title`/`video_status`/`thumbnail_status`/`final_privacy`)
  is removed — those fields live on the auto-provisioner request
  (`POST /internal/v1/thumbnail-sessions`), not on the document the
  editor loads.
- **2026-07-29** — Initial version. Bound the wire-level contract
  for the Velox → InstaEdit publish handoff; locked state machine,
  safety invariant, idempotency format, security baseline.
- **2026-08-08** — Decoupling status update (Definition of Done). This
  document remains the publishing/delivery contract only; the editor-launch
  path is governed by [`project-bridge-contract.md`](./project-bridge-contract.md)
  and the single durable bridge `InstaEdit project_id ↔ velox_project_id`.
  Editor configuration is explicit via `INSTAEDITOR_URL` (fail-fast, no
  ambiguous fallback) and the launch handler is reliably mounted (wiring fix
  `3f3d8fa6`). The final acceptance test with Velox **offline** passed: the
  publish/upload/thumbnail flows in this contract keep working (POSTS 201,
  BRIDGE 201, GATE 200 with Velox ON and OFF) and only the editor-open
  operation fails with Velox OFF.
- **2026-08-08 (follow-up)** — DoD progress consolidated. Additional
  evidence recorded across the boundary docs:
  - **No bidirectional group/channel/membership sync** anywhere in the three
    repos (verified by search + git history + `verify-no-velox-catalog-sync.sh`
    PASS; `f66c5081`). Velox consumes the targets/groups snapshots of this
    contract transiently and never persists or mirrors them.
  - **Demo/stale Velox data removed, not migrated** (`d8d866e2`): fake
    groups (`amish`, `odyssey_explorers`, `rapgame`), test destinations,
    legacy imports and ~486 test/smoke/bench jobs deleted from the live Velox
    DB with a timestamped backup; 66 real content jobs preserved. No
    delivery/publishing row for real content was affected.
  - **Visual rebranding complete** (`e2217ebf`): zero “Dark Editor”
    references left in UI or descriptive text; only technical/historical
    matches remain (basePath `/dark_editor_v2`, env keys, migrations,
    negative tests).
  - Full status matrix: [`VELOX-FRONTEND-GROUPS-BOUNDARY.md`](./VELOX-FRONTEND-GROUPS-BOUNDARY.md)
    and [`project-bridge-contract.md`](./project-bridge-contract.md) §12.
