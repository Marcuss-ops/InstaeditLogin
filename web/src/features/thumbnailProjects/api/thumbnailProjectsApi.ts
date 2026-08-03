/**
 * Canonical client for the autonomous Thumbnail Project API.
 *
 * Owns every request/response shape for:
 *
 *   POST   /api/v1/thumbnail-projects
 *   GET    /api/v1/thumbnail-projects?workspace_id=
 *   GET    /api/v1/thumbnail-projects/{id}?workspace_id=
 *   PATCH  /api/v1/thumbnail-projects/{id}?workspace_id=
 *   POST   /api/v1/thumbnail-projects/{id}/archive?workspace_id=&version=
 *   DELETE /api/v1/thumbnail-projects/{id}?workspace_id=&version=
 *   PUT    /api/v1/thumbnail-projects/{id}/snapshot?workspace_id=
 *   GET    /api/v1/thumbnail-projects/{id}/revisions?workspace_id=
 *   GET    /api/v1/thumbnail-projects/{id}/revisions/{revision_id}?workspace_id=
 *   POST   /api/v1/thumbnail-projects/{id}/restore/{revision_id}?workspace_id=
 *   POST   /api/v1/thumbnail-projects/{id}/render?workspace_id=
 *   GET    /api/v1/thumbnail-exports/{export_id}?workspace_id=
 *   POST   /api/v1/thumbnail-exports/{export_id}/assignments
 *
 * Every project-scoped call requires `workspace_id` as a query
 * parameter (the server enforces workspace isolation server-side).
 *
 * Conflicts: the server returns 409 + `{ code:
 * "PROJECT_VERSION_CONFLICT", current_version? }` when a
 * snapshot/update races another writer (e.g. a second tab). Use
 * {@link parseProjectVersionConflict} (or
 * {@link toProjectVersionConflictError}) to detect it and offer
 * "Ricarica versione recente" / "Salva come copia" — never silently
 * last-write-wins on the canvas. `current_version` is present on
 * snapshot/restore races; lifecycle CAS conflicts omit it.
 *
 * Errors: `authedFetch` throws `AuthError` (401) and `ApiError`
 * (other non-2xx, with the parsed body on `.data`).
 */

import { authedFetch, ApiError } from "../../../lib/auth";
import type {
  ProjectVersionConflict,
  ThumbnailExport,
  ThumbnailProject,
  ThumbnailProjectAssignment,
  ThumbnailProjectRevision,
  ThumbnailProjectSnapshotResult,
  ThumbnailProjectStatus,
  ThumbnailCanvasSnapshot,
} from "../types";

const PROJECTS_PATH = "/api/v1/thumbnail-projects";
const EXPORTS_PATH = "/api/v1/thumbnail-exports";

const projectPath = (projectId: string): string =>
  `${PROJECTS_PATH}/${encodeURIComponent(projectId)}`;

/** Append the mandatory workspace_id query parameter. */
function withWorkspace(path: string, workspaceId: number): string {
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}workspace_id=${encodeURIComponent(String(workspaceId))}`;
}

/** Append a numeric version query parameter to an already-workspace path. */
function withVersion(path: string, version: number): string {
  return `${path}&version=${encodeURIComponent(String(version))}`;
}

// ─── Projects ─────────────────────────────────────────────────────

export interface CreateThumbnailProjectRequest {
  workspace_id: number;
  name: string;
  description?: string;
  canvas_width: number;
  canvas_height: number;
}

export interface UpdateThumbnailProjectRequest {
  name?: string;
  description?: string;
  canvas_width?: number;
  canvas_height?: number;
  status?: ThumbnailProjectStatus;
  /** Required optimistic-concurrency token. */
  version: number;
}

/**
 * POST /api/v1/thumbnail-projects — creates the project with no
 * YouTube channel/video/account prerequisite. 201 on success.
 */
export async function createThumbnailProject(
  body: CreateThumbnailProjectRequest,
  init: RequestInit = {},
): Promise<ThumbnailProject> {
  const resp = await authedFetch(PROJECTS_PATH, {
    method: "POST",
    body: JSON.stringify(body),
    ...init,
  });
  return (await resp.json()) as ThumbnailProject;
}

/**
 * GET /api/v1/thumbnail-projects?workspace_id= — newest-first list.
 * Empty array when the workspace has no projects.
 */
export async function listThumbnailProjects(
  workspaceId: number,
  init: RequestInit = {},
): Promise<ThumbnailProject[]> {
  const resp = await authedFetch(withWorkspace(PROJECTS_PATH, workspaceId), init);
  const data = (await resp.json()) as { items?: ThumbnailProject[] };
  return data.items ?? [];
}

export async function getThumbnailProject(
  workspaceId: number,
  projectId: string,
  init: RequestInit = {},
): Promise<ThumbnailProject> {
  const resp = await authedFetch(withWorkspace(projectPath(projectId), workspaceId), init);
  return (await resp.json()) as ThumbnailProject;
}

export async function updateThumbnailProject(
  workspaceId: number,
  projectId: string,
  body: UpdateThumbnailProjectRequest,
  init: RequestInit = {},
): Promise<ThumbnailProject> {
  const resp = await authedFetch(withWorkspace(projectPath(projectId), workspaceId), {
    method: "PATCH",
    body: JSON.stringify(body),
    ...init,
  });
  return (await resp.json()) as ThumbnailProject;
}

/** POST .../archive — 204 on success. */
export async function archiveThumbnailProject(
  workspaceId: number,
  projectId: string,
  version: number,
  init: RequestInit = {},
): Promise<void> {
  await authedFetch(
    withVersion(withWorkspace(projectPath(projectId), workspaceId), version),
    { method: "POST", ...init },
  );
}

/** DELETE .../ — soft delete; 204 on success. */
export async function deleteThumbnailProject(
  workspaceId: number,
  projectId: string,
  version: number,
  init: RequestInit = {},
): Promise<void> {
  await authedFetch(
    withVersion(withWorkspace(projectPath(projectId), workspaceId), version),
    { method: "DELETE", ...init },
  );
}

// ─── Snapshot + revisions ─────────────────────────────────────────

export interface SaveThumbnailSnapshotRequest {
  schema_version: number;
  snapshot: ThumbnailCanvasSnapshot;
  renderer_version: string;
  base_version: number;
}

export interface SaveThumbnailSnapshotOptions {
  /** Send If-Match: "version-N" instead of relying on base_version. */
  ifMatchVersion?: number;
  signal?: AbortSignal;
  init?: RequestInit;
}

/**
 * PUT .../snapshot — saves a full canvas snapshot as a new immutable
 * revision. `base_version` must equal the project's current `version`
 * or the server answers 409 PROJECT_VERSION_CONFLICT (deduped snapshots
 * return the existing revision with `deduplicated: true`).
 */
export async function saveThumbnailSnapshot(
  workspaceId: number,
  projectId: string,
  body: SaveThumbnailSnapshotRequest,
  opts: SaveThumbnailSnapshotOptions = {},
): Promise<ThumbnailProjectSnapshotResult> {
  const headers = new Headers(opts.init?.headers);
  if (opts.ifMatchVersion !== undefined) {
    headers.set("If-Match", `"version-${opts.ifMatchVersion}"`);
  }
  const resp = await authedFetch(
    withWorkspace(`${projectPath(projectId)}/snapshot`, workspaceId),
    {
      method: "PUT",
      body: JSON.stringify(body),
      signal: opts.signal,
      ...opts.init,
      headers,
    },
  );
  return (await resp.json()) as ThumbnailProjectSnapshotResult;
}

/** GET .../revisions — newest-first immutable revision list. */
export async function listThumbnailRevisions(
  workspaceId: number,
  projectId: string,
  init: RequestInit = {},
): Promise<ThumbnailProjectRevision[]> {
  const resp = await authedFetch(
    withWorkspace(`${projectPath(projectId)}/revisions`, workspaceId),
    init,
  );
  const data = (await resp.json()) as { items?: ThumbnailProjectRevision[] };
  return data.items ?? [];
}

export async function getThumbnailRevision(
  workspaceId: number,
  projectId: string,
  revisionId: string,
  init: RequestInit = {},
): Promise<ThumbnailProjectRevision> {
  const resp = await authedFetch(
    withWorkspace(
      `${projectPath(projectId)}/revisions/${encodeURIComponent(revisionId)}`,
      workspaceId,
    ),
    init,
  );
  const data = (await resp.json()) as { revision: ThumbnailProjectRevision };
  return data.revision;
}

export interface RestoreThumbnailRevisionRequest {
  base_version: number;
  renderer_version?: string;
}

/**
 * POST .../restore/{revision_id} — restores an old revision as a NEW
 * immutable revision (history is never deleted).
 */
export async function restoreThumbnailRevision(
  workspaceId: number,
  projectId: string,
  revisionId: string,
  body: RestoreThumbnailRevisionRequest,
  init: RequestInit = {},
): Promise<ThumbnailProjectSnapshotResult> {
  const resp = await authedFetch(
    withWorkspace(
      `${projectPath(projectId)}/restore/${encodeURIComponent(revisionId)}`,
      workspaceId,
    ),
    { method: "POST", body: JSON.stringify(body), ...init },
  );
  return (await resp.json()) as ThumbnailProjectSnapshotResult;
}

// ─── Render + exports ─────────────────────────────────────────────

export interface RenderThumbnailProjectRequest {
  /** Pins the render to a revision; defaults to the project's current one. */
  revision_id?: string;
  content_type?: "image/png" | "image/jpeg";
  width?: number;
  height?: number;
}

/**
 * POST .../render — rasterizes the persisted snapshot through the
 * canonical renderer, stores the file via the Media Library and creates
 * a `ready` export. 201 with the full ThumbnailExport on success.
 */
export async function renderThumbnailProject(
  workspaceId: number,
  projectId: string,
  body: RenderThumbnailProjectRequest = {},
  init: RequestInit = {},
): Promise<ThumbnailExport> {
  const resp = await authedFetch(
    withWorkspace(`${projectPath(projectId)}/render`, workspaceId),
    { method: "POST", body: JSON.stringify(body), ...init },
  );
  return (await resp.json()) as ThumbnailExport;
}

/** GET /api/v1/thumbnail-exports/{export_id}?workspace_id= */
export async function getThumbnailExport(
  workspaceId: number,
  exportId: string,
  init: RequestInit = {},
): Promise<ThumbnailExport> {
  const resp = await authedFetch(
    withWorkspace(`${EXPORTS_PATH}/${encodeURIComponent(exportId)}`, workspaceId),
    init,
  );
  return (await resp.json()) as ThumbnailExport;
}

// ─── Assignments ──────────────────────────────────────────────────

export interface ThumbnailAssignmentTarget {
  platform_account_id: number;
  youtube_video_id: string;
  target_language?: string | null;
}

export interface CreateThumbnailAssignmentsRequest {
  targets: ThumbnailAssignmentTarget[];
}

/**
 * POST /api/v1/thumbnail-exports/{export_id}/assignments — links an
 * EXISTING ready export to one or more YouTube videos. The export
 * exists before any assignment; the original project is never
 * modified.
 *
 * Note: contract-first client — the backend endpoint is the next
 * server step; the response shape follows the `{ items: [...] }`
 * list convention (with an array fallback).
 */
export async function createThumbnailAssignments(
  exportId: string,
  body: CreateThumbnailAssignmentsRequest,
  init: RequestInit = {},
): Promise<ThumbnailProjectAssignment[]> {
  const resp = await authedFetch(
    `${EXPORTS_PATH}/${encodeURIComponent(exportId)}/assignments`,
    { method: "POST", body: JSON.stringify(body), ...init },
  );
  const data = (await resp.json()) as
    | { items?: ThumbnailProjectAssignment[] }
    | ThumbnailProjectAssignment[]
    | undefined;
  if (Array.isArray(data)) return data;
  return data?.items ?? [];
}

// ─── 409 PROJECT_VERSION_CONFLICT helpers ─────────────────────────

/**
 * Detects the optimistic-concurrency conflict the server returns on
 * stale snapshot saves / updates. Returns the parsed
 * `{ code, current_version? }` or null for any other error
 * (`current_version` is absent on lifecycle CAS conflicts).
 *
 *   const conflict = parseProjectVersionConflict(err);
 *   if (conflict) {
 *     // offer "Ricarica versione recente" (reload project → version
 *     // conflict.current_version) or "Salva come copia"
 *   }
 */
export function parseProjectVersionConflict(
  err: unknown,
): ProjectVersionConflict | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null;
  const data = err.data;
  if (!data || typeof data !== "object") return null;
  const conflict = data as Partial<ProjectVersionConflict>;
  if (conflict.code !== "PROJECT_VERSION_CONFLICT") return null;
  const result: ProjectVersionConflict = { code: conflict.code };
  if (typeof conflict.current_version === "number") {
    result.current_version = conflict.current_version;
  }
  return result;
}

/**
 * Convenience wrapper for callers that want a typed throw/catch:
 * converts a conflicting 409 into `ProjectVersionConflictError`
 * (carrying `currentVersion`), or returns null when the error is not
 * a version conflict.
 */
export function toProjectVersionConflictError(
  err: unknown,
): ProjectVersionConflictError | null {
  const conflict = parseProjectVersionConflict(err);
  if (!conflict) return null;
  return new ProjectVersionConflictError(conflict.current_version);
}

export class ProjectVersionConflictError extends Error {
  currentVersion?: number;
  constructor(currentVersion?: number) {
    super(
      currentVersion === undefined
        ? "PROJECT_VERSION_CONFLICT: the project was modified elsewhere — reload the latest version"
        : `PROJECT_VERSION_CONFLICT: current project version is ${currentVersion}`,
    );
    this.name = "ProjectVersionConflictError";
    this.currentVersion = currentVersion;
  }
}
