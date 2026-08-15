/**
 * YouTube editor-sessions client.
 *
 * Single canonical client for the `/api/v1/youtube/editor-sessions`
 * resource. It owns the request/response shapes and the shared editor
 * opening helper used by the account, calendar, and studio surfaces.
 * Keeping those shapes here ensures every caller receives the complete
 * `{ session_id, velox_project_id, editor_url }` response.
 *
 * Server contract map:
 *
 *   POST   /api/v1/youtube/editor-sessions
 *     body:  CreateYouTubeEditorSessionRequest
 *     resp:  CreateYouTubeEditorSessionResponse
 *            { session_id, velox_project_id, editor_url }
 *     201:   created (handler uses http.StatusCreated)
 *
 *   GET    /api/v1/youtube/editor-sessions?workspace_id=&account_id=
 *     resp:  { sessions: EditorSession[] }
 *     200:   list (always returns an array; empty when no rows)
 *
 *   POST   /api/v1/youtube/editor-sessions/{sessionId}/thumbnail
 *     body:  AttachYouTubeEditorSessionThumbnailRequest
 *            { thumbnail_media_id: string }
 *     resp:  EditorSession  (preserves the post-attach state, e.g.
 *            thumbnail_media_id is now populated)
 *
 *   POST   /api/v1/youtube/editor-sessions/{sessionId}/publish
 *     body:  PublishYouTubeEditorSessionRequest
 *            { privacy_status?: "public"|"unlisted"|"private",
 *              publish_at?: ISO-8601 | null }
 *     resp:  EditorSession
 *
 * Errors:
 *   authedFetch throws AuthError (401) and ApiError (other 4xx/5xx)
 *   for every non-2xx response. Callers re-throw AuthError so the
 *   page-level router can bounce to /login; ApiError is left for
 *   the caller to surface (most pages fall through to authedFetch's
 *   built-in toast).
 *
 * Notes:
 *   - The editor-session endpoint is idempotent for an open
 *     `(workspace_id, platform_account_id, youtube_video_id)` tuple:
 *     the server reuses its existing session and `velox_project_id`,
 *     while a published/terminal session may create a new edit session.
 *     The client still avoids blind retries so callers do not create
 *     an unintended new session after a terminal-state transition.
 *   - The payload key for `youtube_video_id` is canonical — server
 *     also accepts `video_id` as a legacy alias (migration 031) but
 *     new callers MUST send `youtube_video_id`.
 *   - Navigation to `editor_url` is a UI concern, not an API concern.
 *     Callers choose either the legacy new-tab helper or the explicit
 *     top-level redirect helper below; neither embeds the editor.
 */

import { authedFetch } from "../../../lib/auth";
import type {
  EditorSession,
  YouTubePublishResult,
} from "../../../types/uploads";

const SESSIONS_PATH = "/api/v1/youtube/editor-sessions";

const sessionPath = (sessionId: string): string =>
  `${SESSIONS_PATH}/${encodeURIComponent(sessionId)}`;

// ─── Public request / response types ─────────────────────────────

/**
 * Body of POST /api/v1/youtube/editor-sessions.
 */
export interface CreateYouTubeEditorSessionRequest {
  workspace_id: number;
  platform_account_id: number;
  youtube_video_id: string;
  source_thumbnail_url?: string;
}

/**
 * Response from POST /api/v1/youtube/editor-sessions.
 *
 * Server returns this exact shape, including the resolved project and
 * session identifiers needed by downstream editor flows. Beyond the
 * identity it carries the AUTHORITATIVE video projection fetched from
 * YouTube during creation (videos.list) — the initial document
 * InstaEditor needs: video id, title, description, thumbnail URL (the
 * editing canvas), category id, privacy status and the source marker.
 * These are server-derived on purpose: the client never supplies
 * title/description/category/privacy for an existing video.
 */
export interface CreateYouTubeEditorSessionResponse {
  session_id: string;
  velox_project_id: string;
  editor_url: string;
  /** The video the session edits (mirrors youtube_video_id in the request). */
  youtube_video_id: string;
  title: string;
  description: string;
  /** Authoritative YouTube thumbnail (editing canvas). */
  thumbnail_url?: string;
  /** YouTube snippet categoryId, e.g. "24". */
  category_id?: string;
  /** YouTube privacy read-back: "private" | "unlisted" (public is rejected at create). */
  privacy_status: string;
  /** Always "youtube" — the platform source of the session. */
  source: "youtube";
}

export interface GeneratedYouTubeMetadata {
  title: string;
  description: string;
  tags: string[];
  default_language: string;
  default_audio_language: string;
  translations: Record<string, { title: string; description: string }>;
}

/**
 * Poll response of the async metadata generation job
 * (GET /api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}).
 * When `status === "completed"`, `result` carries the full generated
 * metadata; when `status === "failed"`, `error_message` explains why.
 */
export interface MetadataGenerationJob {
  job_id: number;
  status: "queued" | "processing" | "completed" | "failed";
  velox_project_id: string;
  result?: GeneratedYouTubeMetadata;
  error_message?: string;
  created_at?: string;
  completed_at?: string;
}

/**
 * Query-string options for GET /api/v1/youtube/editor-sessions.
 * Both filters are independent and may be omitted for an unscoped
 * list (server enforces tenant isolation server-side; an unscoped
 * call may 403 in some workspaces).
 */
export interface ListYouTubeEditorSessionsOptions {
  workspace_id?: number;
  account_id?: number;
  /** Include published/terminal sessions so the UI can show the final state. */
  include_terminal?: boolean;
  signal?: AbortSignal;
}

/**
 * Body of POST /api/v1/youtube/editor-sessions/{id}/thumbnail.
 */
export interface AttachYouTubeEditorSessionThumbnailRequest {
  thumbnail_media_id: string;
}

/**
 * Body of POST /api/v1/youtube/editor-sessions/{id}/publish.
 *
 * At least one of the two fields should be set — the server treats
 * privacy_status as the desired post-publish state and publish_at
 * as the schedule cursor (RFC3339 in UTC).
 */
export interface PublishYouTubeEditorSessionRequest {
  privacy_status?: "public" | "unlisted" | "private";
  /** RFC3339 timestamp. `null` clears any pending scheduled publish. */
  publish_at?: string | null;
}

/**
 * GET /api/v1/youtube/editor-sessions/{id}.
 * This is the read-back used to verify the YouTube-side privacy after
 * the publish call returns `youtube_sync_status: "pending"`.
 */
export interface YouTubeEditorSessionDetail
  extends Omit<EditorSession, "editor_url"> {
  workspace_id: number;
  platform_account_id: number;
  channel_id?: string;
  source_thumbnail_url?: string;
  /** Extended session contract: authoritative YouTube thumbnail (canvas). */
  thumbnail_url?: string;
  /** Extended session contract: YouTube snippet categoryId (e.g. "24"). */
  category_id?: string;
  /** Extended session contract: live read-back privacy (falls back to desired). */
  privacy_status?: string;
  last_error?: string;
  draft_title?: string;
  draft_description?: string;
  created_at: string;
  updated_at: string;
}

// ─── Public API functions ───────────────────────────────────────

/**
 * POST /api/v1/youtube/editor-sessions.
 *
 * Returns the resolved `editor_url` and stable `velox_project_id` —
 * callers can pass it to the isolated InstaEditor SPA through either
 * the legacy new-tab helper or the explicit top-level redirect helper.
 */
export async function createYouTubeEditorSession(
  body: CreateYouTubeEditorSessionRequest,
  init: RequestInit = {},
): Promise<CreateYouTubeEditorSessionResponse> {
  const resp = await authedFetch(SESSIONS_PATH, {
    method: "POST",
    body: JSON.stringify(body),
    ...init,
  });
  return (await resp.json()) as CreateYouTubeEditorSessionResponse;
}

/**
 * Async metadata generation (migration 113): the POST enqueues a job
 * and returns 202 immediately — the server never blocks on the
 * 60-180s NVIDIA call. This client polls the job until it completes
 * (or fails), preserving the `GeneratedYouTubeMetadata` return shape.
 */
const METADATA_POLL_INTERVAL_MS = 2000;
const METADATA_POLL_TIMEOUT_MS = 6 * 60 * 1000; // 6 minutes cap

/** Kick off the async metadata generation job (POST → 202 + job_id). */
export async function startYouTubeMetadataGeneration(
  veloxProjectID: string,
  prompt: string,
  init: RequestInit = {},
): Promise<{ job_id: number; status: string }> {
  const resp = await authedFetch(
    `${SESSIONS_PATH}/by-project/${encodeURIComponent(veloxProjectID)}/generate-metadata`,
    {
      method: "POST",
      body: JSON.stringify({ prompt }),
      ...init,
    },
  );
  return (await resp.json()) as { job_id: number; status: string };
}

/** Poll one metadata generation job (GET). */
export async function pollYouTubeMetadataGeneration(
  jobID: number,
  init: RequestInit = {},
): Promise<MetadataGenerationJob> {
  const resp = await authedFetch(
    `${SESSIONS_PATH}/generate-metadata/jobs/${encodeURIComponent(String(jobID))}`,
    { ...init },
  );
  return (await resp.json()) as MetadataGenerationJob;
}

/**
 * Generate reviewable NVIDIA metadata without publishing anything.
 *
 * ASYNC: kicks off the job, then polls until `completed` (returns the
 * generated metadata) or `failed` (throws with the server's
 * error_message). Callers that need fine-grained progress can use
 * `startYouTubeMetadataGeneration` + `pollYouTubeMetadataGeneration`
 * directly instead.
 */
export async function generateYouTubeMetadata(
  veloxProjectID: string,
  prompt: string,
  init: RequestInit = {},
): Promise<GeneratedYouTubeMetadata> {
  const { job_id } = await startYouTubeMetadataGeneration(veloxProjectID, prompt, init);

  const deadline = Date.now() + METADATA_POLL_TIMEOUT_MS;
  for (;;) {
    const job = await pollYouTubeMetadataGeneration(job_id, init);
    if (job.status === "completed") {
      if (!job.result) {
        throw new Error("Metadata generation completed without a result");
      }
      return job.result;
    }
    if (job.status === "failed") {
      throw new Error(job.error_message || "Metadata generation failed");
    }
    if (Date.now() >= deadline) {
      throw new Error("Metadata generation timed out");
    }
    await new Promise((resolve) => setTimeout(resolve, METADATA_POLL_INTERVAL_MS));
  }
}

/**
 * GET /api/v1/youtube/editor-sessions — filtered list.
 *
 * Empty array when 0 rows match. The server always returns 200
 * (the empty list is not an error condition).
 */
export async function listYouTubeEditorSessions(
  opts: ListYouTubeEditorSessionsOptions = {},
): Promise<EditorSession[]> {
  const params = new URLSearchParams();
  if (opts.workspace_id !== undefined) {
    params.set("workspace_id", String(opts.workspace_id));
  }
  if (opts.account_id !== undefined) {
    params.set("account_id", String(opts.account_id));
  }
  if (opts.include_terminal) {
    params.set("include_terminal", "true");
  }
  const qs = params.toString();
  const url = qs ? `${SESSIONS_PATH}?${qs}` : SESSIONS_PATH;
  const resp = await authedFetch(url, { signal: opts.signal });
  const data = (await resp.json()) as { sessions?: EditorSession[] };
  return data.sessions ?? [];
}

/**
 * POST /api/v1/youtube/editor-sessions/{id}/thumbnail.
 *
 * Returns the post-attach editor session (fields like
 * `thumbnail_media_id` now reflect the attach).
 */
export async function getYouTubeEditorSession(
  sessionId: string,
  init: RequestInit = {},
): Promise<YouTubeEditorSessionDetail> {
  const resp = await authedFetch(sessionPath(sessionId), init);
  return (await resp.json()) as YouTubeEditorSessionDetail;
}

export async function attachYouTubeEditorSessionThumbnail(
  sessionId: string,
  body: AttachYouTubeEditorSessionThumbnailRequest,
  init: RequestInit = {},
): Promise<EditorSession> {
  const resp = await authedFetch(`${sessionPath(sessionId)}/thumbnail`, {
    method: "POST",
    body: JSON.stringify(body),
    ...init,
  });
  return (await resp.json()) as EditorSession;
}

/**
 * POST /api/v1/youtube/editor-sessions/{id}/publish.
 *
 * Returns the post-publish editor session. Privacy_status and a
 * (cleared) publish_at are reflected in the next /posts/poll.
 */
export async function publishYouTubeEditorSession(
  sessionId: string,
  body: PublishYouTubeEditorSessionRequest,
  init: RequestInit = {},
): Promise<YouTubePublishResult> {
  const resp = await authedFetch(`${sessionPath(sessionId)}/publish`, {
    method: "POST",
    body: JSON.stringify(body),
    ...init,
  });
  return (await resp.json()) as YouTubePublishResult;
}

// ─── Single-purpose helpers (UI-agnostic on purpose) ─────────────

/**
 * Optional launch-time context carried into the separately deployed
 * editor so its in-editor navigation knows where to return the user.
 * `returnTo` is a RELATIVE InstaEdit SPA path (for example
 * `/app/covers?group=7`) — never an absolute URL: the editor combines
 * it with its own configured InstaEdit origin, which keeps the value
 * origin-agnostic and safe to embed in the launch URL.
 */
export interface EditorLaunchOptions {
  returnTo?: string;
  /**
   * Window reference opened SYNCHRONOUSLY inside the triggering click
   * handler (e.g. `window.open("about:blank", "_blank")`). The editor
   * tab is then navigated to the launch URL once it is minted, which
   * keeps the popup open even though several async round-trips happen
   * in between — popup blockers would otherwise swallow a window.open
   * issued outside the user-activation window.
   */
  tab?: Window | null;
}

/**
 * Relative SPA path that returns the user to the Copertine hub of a
 * specific group (the destination of the in-editor Home pill). Kept in
 * the editor client so every "Modifica copertina" entrypoint builds the
 * same return URL and the group id can never drift between callers.
 */
export function coversHubReturnTo(groupId: number): string {
  return `/app/covers?group=${groupId}`;
}

/**
 * Open the Velox / InstaEditor in a new tab.
 *
 * Centralized helper used by AccountDetails and Calendar (and
 * any future "Modifica copertina" entrypoints) so the popup
 * window features (`_blank`, `noopener, noreferrer`) stay consistent.
 * YouTubeStudio inlines its own open because it ALSO refreshes
 * the sessions list right after open completes — different shape,
 * not a candidate for unification there.
 */
function validateEditorURL(editorUrl: string): URL {
  const rawURL = editorUrl.trim();
  if (!rawURL) {
    throw new Error("Editor unavailable / misconfigured");
  }
  let parsed: URL;
  try {
    // Do not provide a base URL here. Relative or empty values would
    // otherwise resolve against InstaEdit and silently reopen the main
    // frontend instead of the separately deployed editor.
    parsed = new URL(rawURL);
  } catch {
    throw new Error("Editor unavailable / misconfigured");
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    throw new Error("Editor unavailable / misconfigured");
  }
  return parsed;
}

// The old Caddy hostname is still returned by sessions created before the
// Vercel editor cutover. Keep those links usable while its DNS is migrated;
// new launches must land on the production Vercel editor so the active bundle
// and the visible InstaEdit 1.0 marker are actually served.
function normalizeEditorURLForDeployment(parsed: URL): URL {
  if (parsed.hostname.toLowerCase() !== "dev.instaedit.org") return parsed;
  const next = new URL("https://instaeditor.vercel.app");
  next.pathname = parsed.pathname;
  return next;
}

export function openInstaEditorInNewTab(editorUrl: string): void {
  const parsed = validateEditorURL(editorUrl);
  window.open(parsed.toString(), "_blank", "noopener,noreferrer");
}

/** Navigate the current top-level document to the separate editor SPA. */
export function redirectToInstaEditor(
  editorUrl: string,
  navigate: (url: string) => void = (url) => window.location.assign(url),
): void {
  const parsed = validateEditorURL(editorUrl);
  navigate(parsed.toString());
}

/**
 * Mint a one-time project-scoped launch token before crossing into the
 * separately deployed editor. The token is placed in the URL fragment so
 * it is not sent in the initial editor HTTP request or reverse-proxy logs.
 *
 * `opts.returnTo` (a relative InstaEdit SPA path) is stamped as a
 * `return_to` query parameter on the launch URL — AFTER the strict
 * editor-URL validation — so the editor can link the in-app Home pill
 * back to the exact surface the user came from (for example the
 * Copertine hub of a specific group).
 */
export async function createEditorLaunchURL(
  editorUrl: string,
  projectId: string,
  opts: EditorLaunchOptions = {},
): Promise<string> {
  const parsed = normalizeEditorURLForDeployment(validateEditorURL(editorUrl));
  if (opts.returnTo) {
    parsed.searchParams.set("return_to", opts.returnTo);
  }
  const response = await authedFetch("/api/v1/editor/launch", {
    method: "POST",
    body: JSON.stringify({ project_id: projectId }),
  });
  const payload = (await response.json()) as { launch_token?: string };
  if (!payload.launch_token) {
    throw new Error("Editor unavailable / misconfigured");
  }
  parsed.hash = `launch_token=${encodeURIComponent(payload.launch_token)}`;
  return parsed.toString();
}

/** Mint a launch token and redirect to the separate editor application. */
export async function redirectToInstaEditorWithLaunch(
  editorUrl: string,
  projectId: string,
  navigate: (url: string) => void = (url) => window.location.assign(url),
  opts: EditorLaunchOptions = {},
): Promise<void> {
  navigate(await createEditorLaunchURL(editorUrl, projectId, opts));
}

/** Mint a launch token and open the separate editor in a new tab. */
export async function openInstaEditorWithLaunch(
  editorUrl: string,
  projectId: string,
  opts: EditorLaunchOptions = {},
): Promise<void> {
  const target = await createEditorLaunchURL(editorUrl, projectId, opts);
  // Popup-proof: when the caller already opened a tab synchronously in
  // the click gesture, navigate it instead of issuing a fresh (and
  // possibly popup-blocked) window.open after the async launch round-trip.
  if (opts.tab && !opts.tab.closed) {
    opts.tab.location.href = target;
    return;
  }
  window.open(target, "_blank", "noopener,noreferrer");
}

// ─── Barrel helper (kept terse on purpose) ───────────────────────

/**
 * Single-call helper for legacy "Modifica copertina" surfaces:
 * resolve the idempotent session for a video, then open it in a new tab.
 *
 * Returns the created session response so callers can chain post-open
 * UI state updates (for example, refreshing the studio session list).
 *
 * The `workspaces` callback supplies the workspace_id for the
 * request; AccountDetails and Calendar both pre-fetched a workspaces
 * list, YouTubeStudio had it in scope already. Centralizing means
 * callers provide the workspace lookup closure once instead of
 * duplicating the lookup + null-guard.
 *
 * `opts.returnTo` (relative SPA path, e.g. `/app/covers?group=7`) is
 * forwarded to the editor launch so the editor Home pill returns the
 * user to the Copertine hub of the group they came from.
 */
export async function createEditorSessionAndOpen(
  body: CreateYouTubeEditorSessionRequest,
  init: RequestInit = {},
  opts: EditorLaunchOptions = {},
): Promise<CreateYouTubeEditorSessionResponse> {
  const session = await createYouTubeEditorSession(body, init);
  await openInstaEditorWithLaunch(session.editor_url, session.velox_project_id, opts);
  return session;
}

/** Create/resolve the idempotent session, then leave InstaEdit for Velox. */
export async function createEditorSessionAndRedirect(
  body: CreateYouTubeEditorSessionRequest,
  init: RequestInit = {},
  navigate?: (url: string) => void,
  opts: EditorLaunchOptions = {},
): Promise<CreateYouTubeEditorSessionResponse> {
  const session = await createYouTubeEditorSession(body, init);
  await redirectToInstaEditorWithLaunch(session.editor_url, session.velox_project_id, navigate, opts);
  return session;
}
