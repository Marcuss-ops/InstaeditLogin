/**
 * Polling hook for the publish status page.
 *
 * Every 3000ms while mounted the hook GETs `/api/v1/posts/{postId}/targets`
 * and inspects the per-target state machine. Polling pauses only when
 * the browser tab goes hidden (a cheap bandwidth saver — the OS will
 * throttle setInterval to ~1Hz after ~1 min of hidden tabs anyway, but
 * we skip the round-trip entirely so the user's quota isn't burned).
 *
 * The hook does NOT stop polling when all targets become terminal —
 * the wizard UI exits the status page explicitly via navigation. The
 * Cross-tab publish invalidation is a separate concern owned by
 * `useYouTubePublishLiveUpdate`.
 *
 * State machine surfaced to consumers:
 *   - `idle`      → no postId yet; no fetches fired
 *   - `loading`   → initial fetch in flight (entered once, then never
 *                   revisited on subsequent ticks — a successful tick
 *                   transitions directly to `polling` or `terminal`)
 *   - `polling`   → at least one target's status is `queued`,
 *                   `publishing`, `retrying`, or `waiting_provider`
 *   - `terminal`  → every target's status is `draft`, `published`,
 *                   `failed`, `partially_published`, or `dlq`
 *   - `error`     → last fetch threw; `targets` keeps the last known
 *                   good snapshot (network blips don't blank the UI)
 *
 * Edge cases:
 *   - `postId == null` → the hook stays in `idle` and does NOT fire
 *     fetches. Useful for pages that haven't completed `createPost`
 *     yet.
 *   - Overlapping fetches are coalesced via an in-flight ref. The
 *     setInterval timer never starts a second concurrent call.
 *   - The hook uses the parent endpoint
 *     `GET /api/v1/posts/{id}/targets`; an empty target list remains
 *     `polling` because the post may not have fanned out yet.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { getPostTargets } from "../api/postTargetsApi";
import type { PostStatus, PostTarget } from "../api/types";

/**
 * Polling interval. 3s is the right order of magnitude for a
 * single-machine async publish (sub-target transitions typically
 * happen on sub-second scales). Lowering further risks burning the
 * 60/minute per-account rate limit (openapi.yaml: §"/accounts/{id}/
 * content"); raising further makes the UX feel sluggish after the
 * worker publishes.
 */
const POLL_INTERVAL_MS = 3_000;

/**
 * State values that mean "the server is done with this target, no
 * further mutations expected unless the user explicitly retries".
 * A target set is `terminal` when EVERY row is in this set AND the
 * non-empty guard passes (an empty parent-response is treated as
 * `polling` because the worker may not have fanned out yet). All
 * other states (`queued`, `publishing`, `retrying`,
 * `waiting_provider`, plus unknown ledger values) leave the hook
 * in `polling` — we only gate on the terminal-set because an
 * unexpected status is conservatively treated as still-mutating.
 *
 * The complementary ACTIVE set is intentionally NOT defined:
 * classifyTargets returns `polling` for everything except the
 * explicit terminal case, so the active-set check would be
 * redundant and prone to drift if the server adds a new active
 * state (we'd have to update both sets in lockstep).
 */
const TERMINAL_STATUSES: ReadonlySet<PostStatus> = new Set<PostStatus>([
  "draft",
  "published",
  "failed",
  "partially_published",
  "dlq",
]);

export type PostTargetPollingStatus =
  | "idle"
  | "loading"
  | "polling"
  | "terminal"
  | "error";

export interface UsePostTargetStatusResult {
  /** Latest snapshot of targets. Sticky across errors (last known good). */
  targets: PostTarget[];
  status: PostTargetPollingStatus;
  /** Populated only when the LAST fetch threw; cleared on the next success. */
  error: string | null;
  /** Manually force an out-of-band poll (e.g. after a retry button click). */
  refetch: () => Promise<void>;
}

function classifyTargets(targets: PostTarget[]): PostTargetPollingStatus {
  if (targets.length === 0) return "polling";
  const allTerminal = targets.every((t) => TERMINAL_STATUSES.has(t.status));
  if (allTerminal) return "terminal";
  return "polling";
}

function deriveErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Unable to fetch target status.";
}

export function usePostTargetStatus(
  postId: number | null,
): UsePostTargetStatusResult {
  const [targets, setTargets] = useState<PostTarget[]>([]);
  const [status, setStatus] = useState<PostTargetPollingStatus>("idle");
  const [error, setError] = useState<string | null>(null);

  /**
   * In-flight ref guards against overlapping fetches — if a slow
   * response is still pending when the interval timer fires, the
   * next tick is skipped. This keeps the loop rate-bounded at 1
   * request per POLL_INTERVAL_MS in the worst case, with no
   * thundering herd on slow links.
   */
  const inFlightRef = useRef<boolean>(false);

  const fetchOnce = useCallback(async (): Promise<void> => {
    if (postId == null) return;
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    try {
      const result = await getPostTargets(postId);
      setTargets(result);
      setStatus(classifyTargets(result));
      setError(null);
    } catch (err) {
      if (err instanceof AuthError) {
        // 401 — let the caller navigate. Don't bury the error in
        // the hook's local state.
        throw err;
      }
      // Keep last-known-good `targets`. The hook is in 'polling'
      // before the blip; after the blip, transition to 'error'
      // but the timer keeps firing so a successful next fetch
      // will return to 'polling'/'terminal'.
      setError(deriveErrorMessage(err));
      setStatus("error");
    } finally {
      inFlightRef.current = false;
    }
  }, [postId]);

  useEffect(() => {
    if (postId == null) {
      setTargets([]);
      setStatus("idle");
      setError(null);
      return;
    }
    setStatus("loading");
    void fetchOnce();
    const intervalId = setInterval(() => {
      // Cheap bandwidth saver: skip while tab is hidden. The browser
      // throttles setInterval to ~1Hz after ~1 min hidden, but skipping
      // entirely avoids even that.
      if (typeof document !== "undefined" && document.hidden) return;
      // Wrap the interval-tick fetch in a `.catch` so an AuthError
      // (session expiry mid-poll) becomes a swallowed promise rather
      // than an unhandled rejection. The router-level ProtectedRoute
      // handles session expiry from the authedFetch 401 path; we just
      // don't navigate from this side because there's no anchor in the
      // interval callback. Any other error propagates so unexpected
      // 5xx still surfaces in DevTools + the periodic 'error' state.
      void fetchOnce().catch((err: unknown) => {
        if (err instanceof AuthError) return;
        throw err;
      });
    }, POLL_INTERVAL_MS);
    return () => {
      clearInterval(intervalId);
    };
  }, [postId, fetchOnce]);

  return { targets, status, error, refetch: fetchOnce };
}
