# InstaEdit agent control plane

This document describes the agent-facing part of `InstaeditLogin` only. The
execution plane (PipelineGen, RenderingGen, Chronon and replaceable workers)
is configured behind the server-side Job Master client and is not exposed to
agents directly.

## Configuration

The control plane uses these server-only settings:

```text
JOB_MASTER_URL
JOB_MASTER_M2M_SECRET
JOB_MASTER_CLIENT_ID
JOB_MASTER_TIMEOUT
JOB_MASTER_POLL_INTERVAL
JOB_MASTER_POLL_TIMEOUT
```

No worker address is compiled into the application. A deployment can point the
same control plane at a pool front door, a private service, or a replacement
execution plane.

## Agent API

Agent API-key calls require the dedicated `automation` permission. This is
separate from `write`, `media`, and `publish`.

```text
GET  /api/v1/agent/tools
POST /api/v1/agent/runs
GET  /api/v1/agent/runs/{run_id}
GET  /api/v1/agent/runs/{run_id}/steps
GET  /api/v1/agent/runs/{run_id}/recovery
POST /api/v1/agent/runs/{run_id}/tools/{tool_name}
```

`/agent/tools` is the only agent catalog. It contains stable tool names such
as `content.generate_script` and `content.render_clip`; internal execution
names such as `system.cleanup` are never returned. A tool is marked available
only when the configured Job Master advertises its corresponding remote type.

Agents submit a typed tool request:

```json
{
  "project": "video-01",
  "idempotency_key": "workflow-1-script",
  "payload": {}
}
```

The control plane maps the tool to its remote job type, persists a run step,
submits the job, then persists the returned remote job id. Repeating the same
idempotency key is safe; reusing a run key with a different goal or reference
is rejected with `409 Conflict`.

## First vertical slice: script only

The smallest end-to-end workflow is deliberately only script generation:

```text
POST /api/v1/agent/runs
  → run_id, status=running

POST /api/v1/agent/runs/{run_id}/tools/content.generate_script
  → step_id, remote_job_id, status=running

GET /api/v1/agent/runs/{run_id}/recovery
  → remote status is read and persisted
  → step status becomes completed/failed
  → run status becomes completed/failed
```

The request sent to the typed tool is:

```json
{
  "project": "video-01",
  "idempotency_key": "workflow-1-script",
  "payload": {"topic": "Mike Tyson", "language": "en"}
}
```

Polling `recovery` repeatedly is safe and idempotent. Replaying the same
step request reuses its persisted step and remote id; if the API process stopped
after remote acceptance but before saving the id, the same remote idempotency
key is submitted again to recover the Master response. No voiceover, stock,
clip, render, thumbnail or publishing capability is involved in this first
certification slice.

Each recovery request persists the latest generic execution snapshot on the
step: normalized remote status, numeric progress and fields such as
`current_stage`, `current_step`, `phase`, `timeline`, `events`, `error` and
`stage_progress`. A server-side recovery worker runs the same poll path after
restart, so progress continues even when the Calendar is closed.

## Restart recovery

`agent_run_steps` stores the run id, stable tool name, remote job id and
idempotency key. After a control-plane restart, the agent reads
`GET /recovery`; the control plane loads those references from the database and
polls the configured Job Master for each non-local step. No in-memory goroutine
is the authority for workflow state.

## Complete-video contract

`internal/agentworkflow` defines a deterministic `content.create_video` plan:

```text
script → optional YouTube/stock acquisition → voiceover → clip render → assemble
```

The plan is a control-plane contract and idempotency-key generator. The
execution-plane implementations of `video.create` and `video.assemble` remain
deferred until PipelineGen exposes them through the catalog. The control plane
does not infer an assembler from script generation plus clip rendering. The
configured Master also returns 404 for its legacy `/api/v1/jobs/pre` PREPARE
endpoint. Accordingly, `content.create_video` is not advertised as available
and new scheduled video intents are rejected before a calendar card is
created. Once PipelineGen publishes a durable full-video contract in the M2M
catalog, the control plane can enable scheduling against it.

The live Master catalog observed on 2026-09-23 advertises these generic M2M
types: `script.generate` (`script.generate.v1` →
`script.generate.result.v1`), `youtube_clip.extract` (`youtube_clip.extract.v1`
→ `youtube_clip.extract.result.v1`), `media.stock` (`media.stock.v1` →
`media.stock.result.v1`), `voiceover.generate` (`voiceover.generate.v1` →
`voiceover.generate.result.v1`), `clip.render` (`clip.render.v1` →
`clip.render.result.v1`), and `image.generate.google`
(`image.generate.google.v1` → `image.generate.google.result.v1`). The BFF now
preserves these schema references, artifact kinds and resource classes in
`GET /api/v1/agent/tools`. The Master currently returns identifiers, not the
JSON Schema bodies; common `/openapi.json` and schema-registry URL candidates
returned 404 during discovery. The PipelineGen source on the compute defines
typed Go payload contracts for these job types, but the catalog itself does
not expose them as machine-readable JSON Schemas.

## Calendar to scheduled publication

The Calendar dialog contains the intended workflow, but generation is gated
until a full-video assembler is available in the live M2M catalog. After that
capability exists, InstaEdit can validate and download the final MP4, import it
into the Media Library, and create a normal queued post with selected
channel(s) and future `scheduled_at`. Calendar detail already opens a preview
and direct link from the stored artifact's `media_url`.

The input has `{pre, finalize, publish}` objects. `pre` carries scenes, clip
references, script, output profile and the required `drive-production`
delivery plan; `finalize` carries optional overlay/audio parameters; `publish`
has title, caption, language, future RFC3339 `scheduled_at`, privacy and
workspace `platform_account_id` targets.

The Calendar dialog can create `pre` from a topic through
`POST /api/v1/agent/video-plan`. The BFF uses the Master's read-only
`GET /api/v1/media/assets?source=youtube&search=…` catalog and its asset detail
route, then includes only ready Drive-backed clips with a SHA-256 reference in
the returned scene manifest. The operator can review/edit this manifest before
submitting. This discovers and reuses registered clips; it does not initiate
new YouTube downloads, write a narration script, or generate a thumbnail.

The Calendar can persist an individual scheduled generation intent as a draft
post. Its workspace-scoped idempotency key prevents duplicate cards. The
recovery worker leases due intents, creates or reuses a stable agent run and
dispatches the saved `content.create_video` payload. Users can reschedule,
start now, or cancel a not-yet-dispatched intent. Generation is scheduled for
30 minutes before publish time, or immediately when that point has passed.
The intent stores the supplied IANA timezone alongside its UTC publish instant;
the Calendar displays the generation time in that stored timezone. Bulk
30-day plan creation is not implemented.

Topic search is read-only catalog discovery; it is not a source-download
operation. The live Master catalog does
not advertise a parent `video.create`, `video.assemble`, final-audio or
thumbnail job, so those phases are not claimed as completed by this
integration.

Starting that workflow first reserves its scheduled draft post, so the Calendar
shows a card before rendering begins. The recovery worker projects the latest
remote status, numeric progress and current phase into that post's metadata;
the Calendar refreshes these active cards every five seconds. At completion,
the same post is atomically populated with the imported MP4, targets and
outbox events. Opening the card exposes the playable asset and an `Apri video
finale` link. This is one scheduled item per submitted workflow, not a durable
30-day schedule generator.

For each recovery read, the control plane merges the Job Master's nested `job`
row with its outer progress envelope, preserving `current_stage`,
`current_step`, `stage_progress`, `timeline`, `events` and error details.
Persisted event histories are bounded. The Calendar detail card shows the
available stage progress and recent timeline/events; displayed phases are
limited to phases the remote job actually emits.
