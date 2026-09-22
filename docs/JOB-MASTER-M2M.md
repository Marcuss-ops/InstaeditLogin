# Remote job master M2M connection

InstaEdit is the control plane: it owns users, channels, scheduling and the
calendar. A separate job master and one or more worker machines own execution
of script, stock, clips, voiceover/TTS, overlays and rendering. The browser
never calls a worker or receives the M2M secret.

## Runtime configuration

Configure these values only in the API runtime secret/config store:

```env
JOB_MASTER_URL=https://job-master.example.internal
JOB_MASTER_M2M_SECRET=<server-only-secret>
JOB_MASTER_CLIENT_ID=instaedit-control-plane
JOB_MASTER_HTTP_TIMEOUT_SECONDS=30
JOB_MASTER_POLL_INTERVAL_SECONDS=3
JOB_MASTER_POLL_TIMEOUT_SECONDS=1800
```

`JOB_MASTER_URL` is the only endpoint location used by the connector. No IP,
hostname, worker name or process is compiled into the application. During the
current Velox transition, `VELOX_M2M_URL`, `VELOX_M2M_SECRET` and
`VELOX_M2M_CLIENT_ID` are accepted as aliases; `JOB_MASTER_*` is the canonical
provider-neutral configuration.

The API calls the remote master at:

```text
GET  ${JOB_MASTER_URL}/api/v1/jobs/types
POST ${JOB_MASTER_URL}/api/v1/jobs
GET  ${JOB_MASTER_URL}/api/v1/jobs/{job_id}
```

The M2M secret is sent only as `Authorization: Bearer ...` from the backend.
The optional client ID is sent as `X-Client-ID` and is useful for auditing and
credential rotation when several control planes share a master.

## Browser-facing BFF boundary

When configured, the authenticated API exposes:

```text
GET  /api/v1/automation/jobs/types
POST /api/v1/automation/jobs
GET  /api/v1/automation/jobs/{job_id}
```

These routes are deliberately separate from the existing Velox control-JWT
routes. The browser sends a generic envelope such as:

```json
{
  "type": "script.generate",
  "project": "video-01",
  "idempotency_key": "video-01-script-001",
  "payload": {}
}
```

The `payload` is opaque to InstaEdit. This lets the master add stock, clip,
TTS, overlay or render capabilities without a new InstaEdit release. A retry
with the same idempotency key is safe; a retry with a different payload is
resolved by the master contract.

The frontend should poll the BFF `GET /api/v1/automation/jobs/{job_id}` using
the configured interval and stop on the terminal status returned by the
master. The response is passed through so `result`, `error`, `progress` and
`timeline` remain available for the calendar timeline UI.

## Connectivity checks

Run these from the API host after configuring the secret (never print the
secret):

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' "$JOB_MASTER_URL/health"
curl -fsS \
  -H "Authorization: Bearer ${JOB_MASTER_M2M_SECRET}" \
  "$JOB_MASTER_URL/api/v1/jobs/types"
```

The repository does not commit a real URL or credential. Firewall rules should
allow the API host to reach the configured master port; worker-to-master
topology and worker IPs stay outside the InstaEdit configuration.
