/**
 * Typed post-target async-state client.
 *
 * The vertical slice polls the per-target state machine (draft →
 * queued → publishing → published, with retrying/waiting_provider/
 * failed/partially_published branches) from the wizard's publish
 * status page (`/content/:postId/publish` equivalent in `/app/*`).
 *
 * Server contract map:
 *   - GET  /api/v1/post_targets/{id}        → PostTargetDetail  (SINGLE)
 *   - GET  /api/v1/posts/{id}/targets       → { targets[] }     (PARENT)
 *   - POST /api/v1/post-targets/{id}/retry  → { status: "queued" }
 *
 * Known server-side gaps (audit 2026-07-30, see
 * `InstaeditLogin/docs/PUBLISH-FLOW-AUDIT.md`):
 *   - The single endpoint `GET /api/v1/post_targets/{id}` is NOT
 *     mounted on the current router. The SDK is built against the
 *     blueprint contract so it typechecks and will start returning
 *     real data the moment the handler lands.
 *   - The parent endpoint `GET /api/v1/posts/{id}/targets` exists
 *     but the underlying `postStore.ListByPost` is not yet wired,
 *     so today it returns `{ targets: [] }`. Tests should expect
 *     an empty array until the repo wiring lands.
 *
 * Both backends ship via the same SDK shapes; consumers do NOT
 * need to distinguish gap vs ready because the wizard UI degrades
 * gracefully when `getPostTargets` returns [] (falls back to a
 * generic "publishing…" state).
 */

import { authedFetch } from "../../../lib/auth";
import type { PostTarget, PostTargetDetail } from "./types";

const POSTS_PATH = "/api/v1/posts";
/** Path templates — `id` is the numeric post_target.id. */
const TARGET_PATH = (id: number): string =>
  `/api/v1/post_targets/${encodeURIComponent(String(id))}`;

/**
 * GET /api/v1/post_targets/{id}.
 *
 * SERVER GAP today: returns 404. Built against the blueprint contract
 * defined in `api/openapi.yaml → /post_targets/{id}` and
 * `internal/models/post.go → PostTarget`. Will start returning real
 * data as soon as the handler lands.
 */
export async function getPostTarget(
  postTargetId: number,
  signal?: AbortSignal,
): Promise<PostTargetDetail> {
  const resp = await authedFetch(TARGET_PATH(postTargetId), { signal });
  return (await resp.json()) as PostTargetDetail;
}

/**
 * GET /api/v1/posts/{postId}/targets.
 *
 * Parent endpoint; returns the targets attached to a post. Today
 * the handler returns `{ targets: [] }` because the repo query
 * isn't wired — see audit gap tracker.
 *
 * The wizard's status page polls this endpoint every 2-5 seconds
 * while at least one target is in `queued`/`publishing`/`retrying`.
 * Once every target is `published`/`failed`/`dlq`, the polling
 * stops and the panel renders the terminal state.
 */
export async function getPostTargets(
  postId: number,
  signal?: AbortSignal,
): Promise<PostTarget[]> {
  const resp = await authedFetch(`${POSTS_PATH}/${postId}/targets`, { signal });
  const data = (await resp.json()) as { targets?: PostTarget[] };
  return data.targets ?? [];
}

export interface RetryPostTargetOptions {
  /**
   * Allow retrying from `partially_published` or `waiting_provider`
   * (default restricts to `failed` only, per the openapi spec).
   * OpenAPI: passed as `?force=true` query param.
   */
  force?: boolean;
  signal?: AbortSignal;
}

/**
 * POST /api/v1/post-targets/{id}/retry.
 *
 * Wired from the status page "Riprova pubblicazione" button. Requires
 * the target to be in a retriable state (`failed`, or `partially_published`
 * / `waiting_provider` with `force: true`).
 *
 * The server responds with the new status string; we mirror it
 * without deviation so callers can branch on `result.status === "queued"`.
 */
export async function retryPostTarget(
  postTargetId: number,
  options: RetryPostTargetOptions = {},
): Promise<{ status: "queued" }> {
  const qs = options.force ? "?force=true" : "";
  const resp = await authedFetch(`${TARGET_PATH(postTargetId)}/retry${qs}`, {
    method: "POST",
    signal: options.signal,
  });
  return (await resp.json()) as { status: "queued" };
}
