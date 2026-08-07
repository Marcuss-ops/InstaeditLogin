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
 *   - The editor-session resource is NOT idempotent at the
 *     transport layer. Each POST creates a fresh session row
 *     server-side. Pages should not retry the create call on
 *     5xx unless they want a double session.
 *   - The payload key for `youtube_video_id` is canonical — server
 *     also accepts `video_id` as a legacy alias (migration 031) but
 *     new callers MUST send `youtube_video_id`.
 *   - `window.open(editor_url, "_blank", "noopener,noreferrer")` is
 *     a UI concern, not an API concern. Pages call this AFTER
 *     resolving the create promise; the openEditorInNewTab helper
 *     below exists for the single-purpose `openVeloxEditor` shim
 *     used by AccountDetails/Calendar (YouTubeStudio inlines its own
 *     because it has post-open refresh logic).
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
 * session identifiers needed by downstream editor flows.
 */
export interface CreateYouTubeEditorSessionResponse {
  session_id: string;
  velox_project_id: string;
  editor_url: string;
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
  last_error?: string;
  draft_title?: string;
  created_at: string;
  updated_at: string;
}

// ─── Public API functions ───────────────────────────────────────

/**
 * POST /api/v1/youtube/editor-sessions.
 *
 * Returns the resolved `editor_url` — callers should open it via
 * {@link openEditorInNewTab} (or pass through to a component that
 * already does the popup dance).
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

/** Generate reviewable NVIDIA metadata without publishing anything. */
export async function generateYouTubeMetadata(
  veloxProjectID: string,
  prompt: string,
  init: RequestInit = {},
): Promise<GeneratedYouTubeMetadata> {
  const resp = await authedFetch(
    `${SESSIONS_PATH}/by-project/${encodeURIComponent(veloxProjectID)}/generate-metadata`,
    {
      method: "POST",
      body: JSON.stringify({ prompt }),
      ...init,
    },
  );
  return (await resp.json()) as GeneratedYouTubeMetadata;
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
 * Open the Velox / InstaEditor in a new tab.
 *
 * Centralized helper used by AccountDetails and Calendar (and
 * any future "Modifica copertina" entrypoints) so the popup
 * window features (`_blank`, `noopener, noreferrer`) stay consistent.
 * YouTubeStudio inlines its own open because it ALSO refreshes
 * the sessions list right after open completes — different shape,
 * not a candidate for unification there.
 */
export function openEditorInNewTab(editorUrl: string): void {
  const parsed = new URL(editorUrl, window.location.origin);
  if (!["http:", "https:"].includes(parsed.protocol)) {
    throw new Error("URL di InstaEditor non valido: il browser ha bloccato un collegamento locale.");
  }
  window.open(editorUrl, "_blank", "noopener,noreferrer");
}

// ─── Barrel helper (kept terse on purpose) ───────────────────────

/**
 * Single-call helper for the common "Modifica copertina" flow:
 * create a session for a video, then open it in a new tab.
 *
 * Returns the created session response so callers can chain post-open
 * UI state updates (for example, refreshing the studio session list).
 *
 * The `workspaces` callback supplies the workspace_id for the
 * request; AccountDetails and Calendar both pre-fetched a workspaces
 * list, YouTubeStudio had it in scope already. Centralizing means
 * callers provide the workspace lookup closure once instead of
 * duplicating the lookup + null-guard.
 */
export async function createEditorSessionAndOpen(
  body: CreateYouTubeEditorSessionRequest,
  init: RequestInit = {},
): Promise<CreateYouTubeEditorSessionResponse> {
  const session = await createYouTubeEditorSession(body, init);
  // Open immediately so the browser popup-blocker trusts us — the
  // gesture chains through from the click handler unchanged.
  openEditorInNewTab(session.editor_url);
  return session;
}
