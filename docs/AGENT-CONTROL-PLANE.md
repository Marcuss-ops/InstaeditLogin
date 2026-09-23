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
deferred until PipelineGen exposes them through the catalog.

## Calendar to scheduled publication

The Calendar's **Crea e programma video** dialog creates a durable run and
invokes the specialized `content.create_video` PREPARE/FINALIZE workflow. On
success, InstaEdit validates and downloads the final MP4, imports it into the
Media Library, and creates a normal queued post with the selected channel(s)
and future `scheduled_at`. Existing publisher workers publish that post at its
scheduled time. Clicking its Calendar card opens the final video preview and a
direct link to the stored artifact.

The input has `{pre, finalize, publish}` objects. `pre` carries scenes, clip
references, script, output profile and the required `drive-production`
delivery plan; `finalize` carries optional overlay/audio parameters; `publish`
has title, caption, language, future RFC3339 `scheduled_at`, privacy and
workspace `platform_account_id` targets.

The Calendar workflow is currently a single scheduled video run. The separate
30-day intent/dispatcher model and natural-language media discovery still need
dedicated PipelineGen capabilities and are not claimed as completed by this
integration.
