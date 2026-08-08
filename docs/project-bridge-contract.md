# InstaEdit ↔ Velox Project Bridge Contract

**Status:** normative contract
**Contract version:** `instaedit.velox.project-bridge.v1`
**Owner:** InstaEdit
**Applies to:** InstaEditLogin, the separate editor application (`VeloxFrontend` or its successor), and the VeloxEditiingg render farm
**Last updated:** 2026-08-08
**Canonical API entry point:** `POST|GET|DELETE /api/v1/thumbnail-projects/{id}/velox-bridge`

This document defines the smallest durable bridge between an InstaEdit
project and an editor/render project in Velox. It is intentionally separate
from the publishing and delivery contract in
[`velox-instaedit-contract.md`](./velox-instaedit-contract.md).

The bridge is an ownership reference, not a replicated domain.

```text
InstaEdit = application source of truth
Velox     = editor/render execution system

InstaEdit project_id  ────────┐
                              │ one explicit bridge
Velox project_id              │
                              ▼
                     editor/render project
```

## 1. Ownership boundary

### InstaEdit owns

- users and authentication;
- workspaces and membership/permissions;
- groups and group membership;
- platform accounts/channels and OAuth grants;
- videos and provider metadata;
- the application project record;
- the relationship between the application project and the Velox project;
- authorization to open or operate on the project;
- editor bridge metadata only (`editor_provider`, optional `editor_status` and `last_editor_sync_at`);
- the application lifecycle of the project.

### The separate editor application owns

- the editor representation referenced by `external_project_id`;
- editor project creation/lookup and editor-native persistence.

### VeloxEditiingg owns

- render execution for the referenced editor project;
- the editor/render representation referenced by `external_project_id` only where
  that representation is explicitly delegated to the render system;
- canvas/editor state;
- scenes, layers, objects, timelines, animations and keyframes;
- editor-native assets and revisions;
- render settings, render jobs and render state;
- editor-side execution details.

The current repository split is therefore:

```text
InstaEditLogin  → application project + bridge + authorization
VeloxFrontend   → editor UI and editor-native project state
VeloxEditiingg  → headless render/job execution
```

The bridge record is deliberately narrow. `editor_provider` declares the
editor backend, `editor_status` is an optional coarse application-level
state, and `last_editor_sync_at` records when InstaEdit last synchronized
operational metadata. None of these make the bridge a Velox replica.

The trusted editor application that creates or resolves `external_project_id`
MUST be the editor-project owner. The InstaEdit bridge route only persists or
resolves that opaque reference. VeloxEditiingg MUST NOT invent a second
editor-project identity or make InstaEdit depend on a Velox-owned catalog.

Velox MUST NOT become the owner of an InstaEdit user, workspace, group,
channel, video, OAuth grant or application permission.

## 2. Canonical bridge record

The bridge record is persisted and authorized by InstaEdit. The minimum
canonical representation is:

```text
bridge(project_id, external_project_id, workspace_id)
UNIQUE(project_id)
UNIQUE(external_project_id)
```

### 2.0 Persistence allowlist and explicit exclusions

The `contract_version` value is wire-only protocol metadata: it is required
in API requests/responses but is not stored as a column in
`velox_project_bridges`. The `velox_project_bridges` table may persist **only**
the following bridge fields:

- `project_id` — the InstaEdit application project reference;
- `workspace_id` — the InstaEdit tenant boundary;
- `external_project_id` — the opaque editor-project reference;
- `editor_provider` — optional editor backend metadata;
- `editor_status` — optional coarse editor status;
- `last_editor_sync_at` — optional operational timestamp;
- the bridge's technical timestamps and migration marker (`created_at`,
  `updated_at`, `migration_run_id`).

It MUST NOT persist, copy, mirror, snapshot, or administer any of the
following in the bridge:

- platform or provider identity;
- platform account identifiers or account records;
- channel identifiers, channel names, channel lists, or channel membership;
- video identifiers, video metadata, or provider video records;
- language or locale metadata;
- groups, group identifiers, group membership, or group-to-channel
  associations;
- OAuth credentials, permissions, or workspace/user copies;
- editor-internal state such as scenes, layers, timelines, revisions, assets,
  or render state.

These values may remain in their authoritative InstaEdit tables and may be
consulted to authorize or validate an editor launch. They may be sent as
short-lived, read-only launch context when strictly necessary, but they MUST
never be written to `velox_project_bridges` or used to create a Velox-owned
catalog. Migration cleanup removes legacy bridge columns only; it does not
delete the authoritative InstaEdit accounts, channels, videos, languages,
groups, or memberships.

Both uniqueness constraints are global within one deployment/environment.
A project identifier MUST NOT be rebound to another bridge, and a Velox
project identifier MUST NOT be attached to another InstaEdit project.


```json
{
  "contract_version": "instaedit.velox.project-bridge.v1",
  "project_id": "thumbproj_01JABC",
  "external_project_id": "vx_01JXYZ",
  "workspace_id": 12,
  "editor_provider": "velox",
  "editor_status": "linked",
  "last_editor_sync_at": "2026-08-07T12:00:00Z"
}
```

### 2.1 Field rules

The bridge carries only the mapping plus the three allowed operational
metadata fields: `editor_provider` (which editor backend owns
`external_project_id`; `"velox"` today), `editor_status` and
`last_editor_sync_at`. These are operational metadata, not a competing
project lifecycle: editor-internal timeline, layers, scenes, assets,
revisions and render state remain editor-owned and MUST NOT be stored here.

| Field | Required | Owner | Contract rule |
|---|---:|---|---|
| `contract_version` | yes | InstaEdit | Exact value is `instaedit.velox.project-bridge.v1`. Unknown versions MUST fail closed. |
| `project_id` | yes | InstaEdit | Opaque, stable identifier of the InstaEdit application project. It MUST NOT be a Velox identifier. For the autonomous project model this is `thumbnail_projects.id`; the YouTube compatibility model currently exposes a `youtube_video_edits` session row and MUST NOT be silently treated as the universal project model. |
| `external_project_id` (`external_project_id`) | yes after linking | Velox-issued, InstaEdit-persisted | The existing wire name `external_project_id` is the opaque `external_project_id` in the provider-neutral contract. InstaEdit stores it; only the editor service may define its internal meaning. |
| `workspace_id` | yes | InstaEdit | Tenant boundary. It is not a copied Velox workspace and does not grant access by itself. |
| `editor_provider` | no | InstaEdit | Optional editor backend identifier; defaults to `velox`. |
| `editor_status` | no | InstaEdit | Optional coarse operational status. |
| `last_editor_sync_at` | no | InstaEdit | Optional timestamp for operational metadata refresh. |

`project_id` and `external_project_id` MUST be treated as opaque strings. No
caller may infer ownership, workspace, user, channel or permissions by
parsing either identifier.

### 2.2 Context ownership (not part of the bridge)

Channel, video, platform, account, language, group and membership context
remains in InstaEdit-owned project/session/account records. It may be read for
authorization and validated before an editor launch, and may be sent as
strictly ephemeral, read-only launch context when needed. None of it is
persisted in this bridge: in particular, `platform_account_id`, channel IDs,
video IDs, language values, group IDs and membership lists MUST NOT appear in
`velox_project_bridges`.

For the current YouTube editor-session compatibility path, the minimum
validated tuple is:

```text
workspace_id
platform_account_id
youtube_video_id
```

The server MUST verify that the video belongs to the selected account before
creating or opening the editor session. A channel display name, avatar or
human-readable label is diagnostic metadata only and is never an identity or
selection key.

If a display snapshot is sent to the editor, it is read-only and disposable:

```json
{
  "channel_name": "Example Channel",
  "channel_avatar": "https://...",
  "language": "en"
}
```

The editor MUST NOT write that snapshot back as channel ownership data.

## 3. Mapping and lifecycle

### 3.1 One bridge, one editor project

The normative `project_id` is the identifier of an InstaEdit application
project, not automatically the identifier of a YouTube editor session. The
current codebase has two application models:

- `thumbnail_projects.id` for autonomous, workspace-scoped editor projects;
- `youtube_video_edits.id` for the legacy/provider-specific editing session,
  which also stores `external_project_id`.

A future implementation MUST make the bridge relation explicit for both
models, or explicitly declare the YouTube session row to be the temporary
application project for that compatibility flow. Until that decision is
implemented, `youtube_video_edits.id` is a provider-specific session ID only;
it is not interchangeable with `thumbnail_projects.id`. The code MUST NOT
infer a universal project identity from whichever row happens to contain a
`external_project_id`.


A linked InstaEdit project maps to exactly one active Velox project:

```text
one InstaEdit project_id ↔ one external_project_id
```

The editor may have many revisions and render jobs, but those revisions do
not create new bridge records. Replacing the editor technology later means
replacing the bridge target, not moving editor internals into InstaEdit.

A `external_project_id` MUST NOT be shared by unrelated InstaEdit projects.

### 3.2 Persist-or-resolve semantics

The canonical route is implemented by InstaEdit and persists or resolves an
already-created opaque `external_project_id`. It does not create, enumerate,
import, delete or synchronize Velox editor projects. The trusted editor
handoff is responsible for creating/resolving the editor project before the
InstaEdit-owned relation is written.

The operation MUST be idempotent. The bridge relation is owned by InstaEdit,
scoped at minimum by `(workspace_id, project_id)`, and protected by the
persisted uniqueness constraints. The winning `external_project_id` is stored
in the bridge; the operation does not rely on an in-memory cache.

The operation is:

1. Authenticate the caller in InstaEdit.
2. Resolve and authorize `project_id` in its `workspace_id`.
3. If the bridge already exists, return the existing `external_project_id`.
4. Otherwise persist the supplied opaque `external_project_id` as the sole
   InstaEdit bridge relation.
5. Retrying the same equivalent request MUST NOT create a second relation;
   attempting to rebind the project to a different editor ID returns `409`.

Replay outcomes are normative:

- an equivalent persisted relation → return the original bridge;
- a different editor ID or context for the same project → `409`;
- an editor project handed off before bridge persistence → retry the bridge
  write with the same opaque ID or reconcile it through an explicit
  orphan-recovery path;
- an orphan MUST NOT be silently rebound to a different InstaEdit project.

For the existing YouTube session path, the compatibility key is the
workspace/account/video tuple above. `FindOrCreateEditableSession` and the
partial unique index currently provide this behavior for non-terminal
sessions. While the editor session is non-terminal,
repeated clicks MUST converge on the same session and
`external_project_id`. After an application-defined terminal/published session,
a later re-edit MAY create a new application session and a new Velox project;
that behavior MUST be explicit and MUST NOT overwrite the old project.

### 3.3 Application lifecycle is authoritative

The application project lifecycle is decided by InstaEdit. Examples:

- archived/deleted application project → editor access is refused;
- revoked bridge → editor access is refused;
- active application project → editor access may be granted after all checks;
- editor/render failure → does not change group, channel or workspace data.

Velox editor status and render status remain Velox-owned projections. They are
not synchronized back as a competing project lifecycle. InstaEdit may read a
render/job result through the documented API and record an application-level
result, but it remains the owner of the application decision.

## 4. Authorization contract

### 4.1 Browser/user boundary

The target role matrix is:

| Operation | Required InstaEdit permission |
|---|---|
| Read project/bridge | workspace member with project-read permission |
| Edit snapshot/revision | workspace member with project-edit permission |
| Request render | workspace member with project-edit/render permission |
| Assign export to channel/video | workspace member with publish/assignment permission |
| Archive/delete project | workspace owner or workspace-admin permission |

The current YouTube compatibility helper remains owner-only until these role
checks are implemented and tested. A deployment MUST NOT claim the target
member permissions merely because `workspace_members` exists.


The browser authenticates to InstaEdit. It MUST NOT receive:

- OAuth access or refresh tokens;
- Velox control JWT signing secrets;
- Velox database credentials;
- unrestricted Velox service credentials;
- another user's or workspace's project identifiers.

The editor receives only the authorized project handle and the minimum
context needed to render its UI. Project-opening URLs MUST be generated by
InstaEdit and MUST be scoped to the authorized project.

### 4.2 InstaEdit authorization sequence

The target contract allows an authorized workspace member according to the
InstaEdit role model. The current YouTube editor-session helper is stricter:
it verifies workspace ownership (`Workspace.OwnerID == userID`) and does not yet
constitute proof that every workspace-member role can open a session. Any
implementation claiming member access MUST add and test the corresponding role
check; it must not weaken the current owner-only path implicitly.


For every bridge operation, InstaEdit MUST:

1. authenticate the user/session;
2. load `project_id` from InstaEdit;
3. verify the requested `workspace_id` matches the project;
4. verify the user owns or is an authorized member of the workspace according
   to the InstaEdit permission model;
5. when the launch originates from a provider video, verify that video and
   channel in InstaEdit-owned records;
6. only then resolve or use `external_project_id`.

A request that supplies a foreign workspace, project, account, channel or
video MUST fail without revealing whether the foreign resource exists.
Normal user-facing behavior is a generic `404` for an unknown or inaccessible
project. Authentication failures remain `401`; malformed requests are `400`
or `422` as appropriate.

### 4.3 InstaEdit-to-Velox authorization

When InstaEdit calls Velox, it uses the existing machine-to-machine boundary:

- short-lived signed control JWT or the configured equivalent;
- `iss=instaedit` and `aud=velox`;
- verified `workspace_id` claim;
- only the minimum operation scopes, such as `jobs.read`, `jobs.write`,
  `workers.read`, `assets.read` or `assets.write`;
- no OAuth material.

Velox MUST validate the token, workspace claim and route scope. Velox MUST
also verify that the referenced job, asset or render resource belongs to the
signed workspace. A client-supplied workspace header is never authoritative.

The bridge itself is authorized by InstaEdit. Velox may validate the opaque
`external_project_id` and its workspace binding, but it MUST NOT independently
look up or reconstruct InstaEdit groups, channels, users or permissions.

## 5. API shape

The canonical bridge route is implemented at
`/api/v1/thumbnail-projects/{id}/velox-bridge`. The following examples describe
its semantics; the route does not create a Velox editor project.

### 5.1 Persist or resolve the bridge

The API represents the bridge as the project mapping plus optional editor
metadata. It does not accept or return platform, channel, video, language,
group, or membership fields.

```http
POST /api/v1/thumbnail-projects/{id}/velox-bridge
Authorization: Bearer <InstaEdit session>
Content-Type: application/json
```

Request (the version is mandatory):

```json
{
  "contract_version": "instaedit.velox.project-bridge.v1",
  "workspace_id": 12,
  "external_project_id": "vx_01JXYZ"
}
```

Response:

```json
{
  "contract_version": "instaedit.velox.project-bridge.v1",
  "bridge": {
    "project_id": "thumbproj_01JABC",
    "external_project_id": "vx_01JXYZ",
    "workspace_id": 12,
    "editor_provider": "velox"
  },
  "editor_url": "https://editor.example.com/project/vx_01JXYZ"
}
```

The existing `/api/v1/youtube/editor-sessions` endpoint is a compatibility
entry point. It MUST preserve the same authorization semantics, but it is not
itself the canonical thumbnail-project bridge route.

### 5.2 Resolve by project

```http
GET /api/v1/thumbnail-projects/{id}/velox-bridge?workspace_id=12
```

The response is workspace-scoped and MUST return `404` when the project is
unknown, archived, revoked or inaccessible.

The existing compatibility endpoint:

```http
GET /api/v1/youtube/editor-sessions/by-project/{external_project_id}
```

is allowed to remain, but it MUST keep the same authorization behavior. The
opaque Velox ID is a lookup handle, not a permission credential.

### 5.3 Editor operations

Editor operations use the resolved project handle and remain scoped to the
bridge:

```text
load project/revision
save editor state
create revision
request render
read render status
read render artifact
```

They MUST NOT accept group IDs or arbitrary channel lists as a substitute for
`project_id` authorization. Destination assignment is an explicit InstaEdit
operation after a valid project/export exists.

## 6. Data flow and allowed propagation

Allowed direction:

```text
InstaEdit
  ├── project_id
  ├── workspace_id
  ├── authorization decision
  └── resolved external_project_id
          │
          ▼
Velox/editor
  ├── editor state
  ├── revisions
  ├── render jobs
  └── render results
```

Velox may echo these values for correlation:

```text
project_id
external_project_id
workspace_id
correlation_id
```

An echo is not ownership and MUST NOT be treated as an authorization grant.

Render/job results travel back through the documented API/BFF. They contain
operational status and artifact references, not a copied InstaEdit domain.

## 7. Explicit exclusions

The following are forbidden in the bridge and in Velox's project/editor
domain:

```text
velox_group_id
velox_channel_group_id
velox_workspace_copy
velox_user_groups_cache
velox_channel_catalog
velox OAuth tokens
timeline
layers
scenes
render_state
revisions
keyframes
editor_snapshot
```

The bridge stores only the mapping (`project_id ↔ external_project_id`),
`workspace_id`, and at most `editor_provider`, `editor_status` and
`last_editor_sync_at` plus technical timestamps/markers. Platform, account,
channel, video, language, group and membership data are never persisted in
this bridge; editor-internal representations are never persisted in
InstaEdit's bridge either.

Specifically:

- no duplicate groups in Velox;
- no duplicate channel management in Velox;
- no group → channel associations owned by Velox;
- no editor request that asks Velox to list all InstaEdit groups or channels;
- no bidirectional group/channel synchronization;
- no shared database between InstaEdit and Velox;
- no editor frontend embedded as the ownership layer inside InstaEdit;
- no synchronization loop where Velox edits a group/channel and InstaEdit
  tries to reconcile it back;
- no fallback to a default workspace, channel or group when a reference is
  missing or unauthorized;
- no use of display names as identifiers.

A group selection belongs entirely to InstaEdit. If a user selects a group,
InstaEdit resolves its current channel membership and creates the required
project/destination context. Velox receives only the concrete project context
needed for the editor or render operation. `group_id` MAY remain in the
separate publishing/delivery contract when InstaEdit resolves a delivery
selection, but it MUST NOT be persisted in the project bridge or interpreted
by Velox as an owned group.

The current `Covers.tsx` iframe path using `/dark_editor_v2` is a legacy
compatibility violation of this boundary. The target flow is a redirect or
new-tab navigation to the separately deployed editor SPA; embedding the
editor does not transfer ownership, and it MUST NOT be used as the long-term
bridge mechanism.


## 8. Persistence and consistency rules

The InstaEdit bridge write MUST be atomic from the application's perspective:

```text
receive the trusted Velox project reference
persist InstaEdit bridge reference
return editor URL
```

If the trusted editor handoff reports a Velox project before InstaEdit
persistence succeeds, the bridge write MUST be retryable with the same opaque
ID or handled through a reviewed orphan procedure. It MUST NOT silently
rebind that editor project to a different InstaEdit project.

If the InstaEdit project is deleted or permanently revoked, the bridge is no
longer usable. The editor data may follow the editor retention policy; it is
not imported into InstaEdit groups, channels or users.

No background synchronization job is required for groups or channels. A
fresh authorization check occurs when the user opens or mutates the project.
Provider/channel metadata may be refreshed by InstaEdit as part of its own
provider lifecycle, but Velox does not own or reconcile that metadata.

## 9. Compatibility notes for the current implementation

The current codebase already has the following compatible pieces:

- `youtube_video_edits.external_project_id` is the existing opaque editor handle;
- `FindOrCreateEditableSession` provides click-idempotent reuse for the
  `(workspace_id, platform_account_id, youtube_video_id)` tuple;
- `GET/PATCH/PUT/POST .../by-project/{external_project_id}` routes use the Velox
  project handle as a lookup key;
- `ThumbnailProject` is already a workspace-scoped autonomous project model
  that does not require a channel or video;
- Velox's current BFF contract is workspace-scoped and uses narrow job,
  worker and asset scopes;
- Velox's retired legacy InstaEditor surface (formerly called Dark Editor) and YouTube domain migrations confirm that
  editor project ownership moved out of the Velox render-farm database.

The following are compatibility gaps, not permission to weaken this contract:

- the current YouTube session model still combines session identity with
  channel/video context;
- some current UI paths still use the legacy `editor_url`/`/dark_editor_v2`
  shape;
- `ThumbnailProject` and `YouTubeVideoEdit` are separate application models;
- the existing publishing contract still contains group-target resolution for
  delivery, which is distinct from editor project ownership;
- any unresolved local merge-conflict markers must be resolved before using
  those files as an implementation source of truth.

Future implementation work MUST adapt these compatibility paths to this
contract rather than introducing Velox-owned copies of InstaEdit data.

## 10. Configuration and CI enforcement

The canonical deployment configuration is:

```env
VELOX_PROJECT_BRIDGE_CONTRACT_VERSION=instaedit.velox.project-bridge.v1
```

InstaEdit and Velox fail closed when this value is unknown. The control JWT
secret (`VELOX_CONTROL_JWT_SECRET` on InstaEdit and
`INSTAEDIT_CONTROL_JWT_SECRET` on Velox) authenticates the request but does
not grant catalog access. It is distinct from the reverse delivery token.

The contract is enforced by:

- InstaEdit bridge response/model tests for version, idempotency, ownership
  and forbidden fields;
- Velox scope and route tests requiring `project_id` for editor operations;
- InstaEditor tests for opaque project handles and `410 Gone` global catalog
  retirement;
- the vendored OpenAPI synchronization check in VeloxFrontend.

## 11. Contract acceptance checklist

An implementation conforms only when all of the following are true:

- [ ] Every linked project has exactly one InstaEdit `project_id` and one
      opaque `external_project_id`.
- [ ] `workspace_id` is mandatory and authorization is checked in InstaEdit.
- [ ] Channel/video ownership is revalidated in InstaEdit before editor launch; no such context is persisted in the bridge.
- [ ] Repeated create/open requests are idempotent.
- [ ] Cross-tenant probes return no resource existence information.
- [ ] Browser clients never receive OAuth or Velox control secrets.
- [ ] Velox receives only the minimum project context needed for its
      operation; channel/video context is never persisted in the bridge.
- [ ] Velox has no groups, group membership, channel catalog or user
      permission domain.
- [ ] There is no shared database and no bidirectional group/channel sync.
- [ ] Render/editor state remains editor-owned; application ownership remains
      InstaEdit-owned.
- [ ] The publishing/delivery contract is not silently reused as a project
      ownership contract.

## 12. Status 2026-08-08 — Definition of Done acceptance

The final Definition of Done acceptance test was executed with **Velox
completely offline** (all `velox-*` services dead, editor ports closed) while
InstaEdit stayed fully operational:

- login, Groups, Channels, videos, Drive and Posting all returned the same
  success codes with Velox ON and OFF;
- **only** "Apri InstaEditor" fails with Velox OFF (`502 upstream editor call
  failed`) — the editor access is the single operation that depends on Velox;
- Velox was restarted and verified afterwards (health 200).

The acceptance checklist above is now verified for the YouTube
editor-session compatibility path:

- `workspace_id` and ownership/permission checks are performed by InstaEdit
  before any editor launch (`c34b292c` centralizes workspace authorization;
  `caf43680` pins the permission gate before opening the editor);
- channel/video ownership is revalidated in InstaEdit and only short-lived,
  read-only context reaches Velox; nothing is persisted in the bridge beyond
  the allowlist in §2.0;
- editor configuration is explicit via `INSTAEDITOR_URL`, which fails fast and
  clearly when missing — no ambiguous fallback (`7f1b7540`);
- the launch flow is now reliably mounted: the `LaunchTokenIssuer` wiring bug
  in `modules_editor.go` (handler never registered) was fixed with a
  regression test (`3f3d8fa6`);
- the legacy `/dark_editor_v2/*` routes were reduced to a launcher/redirect
  mechanism (`312c6b8b` retires the legacy covers route);
- the stable one-to-one `InstaEdit project_id ↔ velox_project_id` bridge is
  complete (`bde2272c`);
- no bidirectional group/channel/membership sync exists in any of the three
  repos (`f66c5081`, guard `verify-no-velox-catalog-sync.sh` PASS) — there is
  a single source of truth for content→group/channel membership (InstaEdit
  DB);
- demo/stale Velox data (fake groups `amish`/`odyssey_explorers`/`rapgame`,
  test destinations, ~486 smoke/test/bench jobs) was removed from the live
  Velox DB and **not** migrated; real content jobs and the bridge
  relationships were preserved (`d8d866e2`);
- visual rebranding to InstaEditor is complete (`e2217ebf`). Full status
  matrix: `VELOX-FRONTEND-GROUPS-BOUNDARY.md` "Definition of Done — stato
  avanzamento".

Known non-blocking follow-up: `GET /api/v1/workspaces/{id}/channels` returns
404 due to chi route shadowing between the team module and the auth module.
It does not affect the Definition of Done because the Channels page uses
`/api/v1/accounts` and `/api/v1/workspaces` (verified in
`web/src/features/channels/api/channelsApi.ts`).
