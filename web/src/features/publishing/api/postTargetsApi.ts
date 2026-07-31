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
 * Both endpoints use the same typed target shapes. The parent list
 * returns the targets attached to the post, including an empty array
 * when the post has no targets. */

import { authedFetch } from "../../../lib/auth";
import type { PostTarget, PostTargetDetail } from "./types";

const POSTS_PATH = "/api/v1/posts";
/** Path templates — `id` is the numeric post_target.id. */
const TARGET_PATH = (id: number): string =>
  `/api/v1/post_targets/${encodeURIComponent(String(id))}`;

/**
 * GET /api/v1/post-targets/{id}.
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
 * Parent endpoint; returns the targets attached to a post.
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
