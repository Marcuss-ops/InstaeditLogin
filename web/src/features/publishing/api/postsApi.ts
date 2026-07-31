/**
 * Typed POST /api/v1/posts client.
 *
 * Replaces the inline fetch + payload builder in
 * `web/src/pages/internal/Compose.tsx:handleSubmit` with a typed
 * payload, auto-generated `Idempotency-Key`, and a single-shape
 * return.
 *
 * The Idempotency-Key contract (openapi.yaml → /posts + Go
 * pkg/api/posts_handlers.go handleCreatePost, migration 021):
 *   - key on `(workspace_id, idempotency_key, payload_hash)` tuple →
 *     same key + same payload returns the cached 201/202 (replay),
 *   - same key + different payload → 409 `idempotency_key_conflict`,
 *   - the wizard always sends a fresh UUID per submit so a
 *     browser-retry of the same submit does NOT create a duplicate
 *     post. The cache entry is operator UX, not part of the contract.
 */

import { authedFetch } from "../../../lib/auth";
import type { CreatePostRequest, Post } from "./types";

const POSTS_PATH = "/api/v1/posts";

/**
 * Canonical RFC4122 v4 UUID generator. Uses `crypto.randomUUID()` on
 * every modern browser + Node 19+; falls back to a Math.random-based
 * v4-shaped string when unavailable so SSR/test environments still
 * produce something idempotency-shaped.
 *
 * The server doesn't validate UUID semantics — it only requires
 * the key to be a stable unique string up to 255 chars — but a
 * real v4 is friendlier to log scrubbing than a random base64 blob.
 */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  // RFC4122 v4 fallback.
  const hex = "0123456789abcdef";
  let out = "";
  for (let i = 0; i < 36; i++) {
    if (i === 8 || i === 13 || i === 18 || i === 23) {
      out += "-";
      continue;
    }
    if (i === 14) {
      out += "4";
      continue;
    }
    if (i === 19) {
      out += hex[(Math.random() * 4) | 0 | 8];
      continue;
    }
    out += hex[(Math.random() * 16) | 0];
  }
  return out;
}

export interface CreatePostOptions {
  /**
   * Override the Idempotency-Key (advanced: replaying a known
   * submit). When omitted, `createPost` does NOT auto-attach a
   * header — the wizard is the one place that should call
   * `newIdempotencyKey()` per submit. Other callers (e.g. batch
   * re-arms from the Status page) pass their own key.
   */
  idempotencyKey?: string;
  signal?: AbortSignal;
}

/**
 * POST /api/v1/posts — universal publish payload.
 *
 * Body shape mirrors `CreatePostRequest`. The wizard builds
 * `targets[0].settings.youtube = { title, description, privacy_status: "private",
 * made_for_kids, tags }` per the vertical slice spec.
 *
 * Status codes:
 *   - the OpenAPI spec says 202 Accepted,
 *   - current Go handler returns 201 Created (handleCreatePost writes
 *     `http.StatusCreated`),
 *   - authedFetch treats both as success — wizard doesn't care which
 *     one fired, only that the body carries `id`+`targets[]`.
 *
 * Idempotency-Key conflict: server returns 409 `idempotency_key_conflict`
 * when a replay carries a different body hash. `authedFetch` surfaces
 * it as a regular `ApiError(409, ...)`. Caller-side: the wizard
 * always mints a fresh UUID, so this branch should never fire in
 * normal use; trigger it manually to validate the error UX.
 */
export async function createPost(
  body: CreatePostRequest,
  options: CreatePostOptions = {},
): Promise<Post> {
  const headers: Record<string, string> = {};
  if (options.idempotencyKey) {
    headers["Idempotency-Key"] = options.idempotencyKey;
  }
  const resp = await authedFetch(POSTS_PATH, {
    method: "POST",
    body: JSON.stringify(body),
    headers,
    signal: options.signal,
  });
  return (await resp.json()) as Post;
}

/**
 * GET /api/v1/posts/{id} — fetch a single post by id with cross-tenant
 * isolation enforced server-side. Returns the canonical Post shape;
 * targets are NOT populated here — use `getPostTargets(id)` for the
 * per-target array.
 */
export async function getPost(postId: number, signal?: AbortSignal): Promise<Post> {
  const resp = await authedFetch(`${POSTS_PATH}/${postId}`, { signal });
  return (await resp.json()) as Post;
}
