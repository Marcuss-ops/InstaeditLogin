# Integrations

Authoritative reference for partner integrations running against InstaEdit.
The doc is the single source of truth for wire-level contracts; pointer-target
for commits that touch integration code (look here first when an integration
commit message references "per the integration_summary cutover").

## Velox → InstaEdit handoff

### Scope

* Producer: Velox (master) calling InstaEdit on:
  - `POST /internal/v1/destinations/{id}/validate` — destination liveness probe
  - `POST /internal/v1/deliveries` — artifact handoff for ingest + publish
  - `GET /internal/v1/deliveries/{id}` — reconciliation/poll
* Receiver: InstaEdit at WAL=stage=worker pipeline → YouTube upload.
* Authentication: server-to-server Bearer token from `VELOX_API_TOKEN` env var.
  Constant-time compared via `crypto/subtle.ConstantTimeCompare`.
* 401 missing/malformed Authorization; 403 token mismatch; 503 token unconfigured
  at boot (operator remediation required).

### 7 documented events

When `external_deliveries.status` transitions, InstaEdit POSTs an HMAC-signed
callback to Velox's `callback_url`. The seven event types are:

| Event              | Fires when status moves to          |
|--------------------|-------------------------------------|
| `artifact_verified`| sha-256 + size + mime all match     |
| `queued`           | publish_pool claimed the row        |
| `publishing`       | platform publish call in flight     |
| `published`        | 2xx returned with media id + url    |
| `blocked_auth`     | platform_account reauth_required   |
| `failed`           | terminal non-recoverable error      |
| `dead_letter`      | retry budget exhausted              |

The signature scheme is `sha256=<hex>` of `HMAC-SHA256(VELOX_WEBHOOK_SECRET,
"<unix_ts>.<raw_body>")`. The same scheme is verified on both the
InstaEdit→Velox callback server AND the worker-side HMAC test.

### Cross-handler 404 body constant

`pkg/api/internal_velox.go` exports the constant
`veloxDestinationNotFoundBody = "destination not found"` and uses it
verbatim in every 404 path so a probe hitting any of the three internal
endpoints gets an indistinguishable body. This closes a status-code
oracle / existence-leak surface that would otherwise let an attacker
enumerate valid resource ids.

### Failure-path observability

The download-job channel between the producer (handler) and the worker
(downloader) has a 64-slot buffer. When the buffer is saturated, the
producer's `select/default` fires and emits:

* `metrics.RecordVeloxDownloadJobDrop("post_deliveries")` — increments
  the Prometheus counter `velox_download_job_drops_total{source="post_deliveries"}`.
* `slog.Error("velox deliver: download job queue full; reaper will pick up",
   "social_delivery_id", inserted.ID, "source", "post_deliveries")`

The pair is fail-loud-by-design (Warn→Error, never silent): operators
wire the counter to a Grafana panel AND grep the log for the
`social_delivery_id` field. The metric is registered in
`pkg/metrics/metrics.go::init()`.

### Auth + 429 + 503 mapping

| Response | Code | When                                              |
|----------|------|---------------------------------------------------|
| 204      | OK   | Destination valid (Velox consumes status only)    |
| 202      | OK   | Delivery accepted (with or without already_exists)|
| 400      | BAD  | Malformed JSON / oversized body                    |
| 401      | UNA  | Missing or malformed Authorization header          |
| 403      | FOR  | Bearer token mismatch (constant-time compare)     |
| 404      | NFD  | Destination / delivery / workspace not found      |
| 409      | CON  | Idempotency conflict (same key + different SHA)    |
| 413      | T-L  | Body exceeds the 8 MB cap                          |
| 422      | UNC  | Producer-side validation chain failed             |
| 429      | T-M  | Per-destination rate limit exceeded (60/min default)|
| 500      | ERR  | Transient lookup / persist failure (operator-pageable)|
| 503      | UNA  | VELOX_API_TOKEN empty at boot / transient workspace |

### See also

* `pkg/api/openapi.yaml` — OpenAPI spec for the three internal endpoints
* `pkg/api/internal_velox.go` — handler source
* `internal/repository/external_delivery_repo.go` — 3-way idempotency INSERT
* `internal/worker/ingest_fsm.go` — state-machine transitions + persistence
* `internal/worker/velox_artifact_downloader.go` — channel consumer + sha verify
* `pkg/api/internal_velox_callback_dispatcher.go` — HMAC dispatch + retry
