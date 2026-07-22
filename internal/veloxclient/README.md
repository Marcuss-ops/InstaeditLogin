# internal/veloxclient

Go client for the InstaEdit → Velox internal control plane.

## Overview

`veloxclient` signs short-lived HS256 JWTs with `VELOX_CONTROL_JWT_SECRET`
and calls the Velox master at `VELOX_CONTROL_URL` on behalf of a logged-in
InstaEdit user.

The JWT carries:

```json
{
  "iss": "instaedit",
  "aud": "velox",
  "sub": "<userID>",
  "workspace_id": <workspaceID>,
  "scopes": [
    "velox:jobs:read",
    "velox:jobs:write",
    "velox:workers:read",
    "velox:assets:read"
  ],
  "exp": <now + 3min>,
  "jti": "<random 16-byte hex>"
}
```

- Token lifetime is **3 minutes** (within the spec's 2–5 minute window).
- `user_id` and `workspace_id` are **never** sent in the request body; they are
  signed into the JWT so Velox can trust them.
- The underlying HTTP client is plain (no automatic retries) so Velox status
  codes such as 404 and 403 map cleanly to typed sentinel errors.

## Files

- `client.go` – `Client` implementation of `pkg/api/velox.Client`.
- `auth.go` – JWT signing (`signControlToken`).
- `types.go` – Internal wire types mapping Velox JSON responses to BFF types.
- `jobs.go` – Job operations (list, create, get, cancel, deliveries).
- `workers.go` – Worker operations (list, get).
- `assets.go` – Asset operations (get metadata).
- `client_test.go` – Unit tests covering JWT claims, request paths,
  error mapping, and 204/404/5xx handling.

### Methods

`Client` implements the `pkg/api/velox.Client` interface:

- `ListJobs(ctx, workspaceID, filter) []Job`
- `CreateJob(ctx, workspaceID, userID, req) *Job`
- `GetJob(ctx, workspaceID, jobID) *JobDetail`
- `CancelJob(ctx, workspaceID, jobID) error`
- `ListJobDeliveries(ctx, workspaceID, jobID) []Delivery`
- `ListWorkers(ctx, workspaceID) []Worker`
- `GetWorker(ctx, workspaceID, workerID) *Worker`
- `GetAsset(ctx, workspaceID, assetID) *Asset`

## Construction

```go
import "github.com/Marcuss-ops/InstaeditLogin/internal/veloxclient"

vc := veloxclient.New(
    os.Getenv("VELOX_CONTROL_URL"),
    os.Getenv("VELOX_CONTROL_JWT_SECRET"),
)
```

`New` returns `nil` when either argument is empty; the BFF router treats a
nil client as “Velox BFF routes not mounted.”

## Usage in BFF handlers

Handlers in `pkg/api/velox` call the client through the `velox.Client`
interface. The workspace and user IDs come from the session identity, not
from the request body:

```go
id := auth.IdentityFromContext(ctx)
job, err := c.CreateJob(ctx, id.WorkspaceID(), id.UserID(), req)
```

## Environment variables

| Variable | Purpose |
|----------|---------|
| `VELOX_CONTROL_URL` | Base URL of the Velox master (e.g. `http://velox-master:8080`). |
| `VELOX_CONTROL_JWT_SECRET` | Shared HS256 secret for the InstaEdit → Velox control JWT. |

## Testing

```bash
go test ./internal/veloxclient/...
```
