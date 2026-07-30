/**
 * useChannelContent — page-by-page loader for the channel-page video list.
 *
 * Calls `listChannelContent` (from the api barrel) for the
 * single-account endpoint:
 *
 *   GET /api/v1/accounts/{accountId}/content?limit=20[&privacy=…][&cursor=…]
 *
 * State machine (mirrors `useYouTubeChannels`' LoadState union so
 * callers share the same mental model — ready has a richer shape
 * here to carry cursor + loadMore meta, but the kind names are
 * identical):
 *
 *   loading                            ↘
 *                                       ready { items, nextCursor,
 *                                              isLoadingMore?, loadMoreError? }
 *                                       ↘ error { message }
 *
 * Auto-refetch: any change to `accountId` or `privacy` (depth-
 * equivalent) aborts the in-flight fetch and starts a fresh one
 * with an empty items list. Cursor is RESET on such a change
 * because the page result set is different.
 *
 * `loadMore()` appends to `items` using the current `nextCursor`.
 * It does NOT reset state — just sets `isLoadingMore=true` while
 * the cursor fetch is in flight. Errors during loadMore surface
 * as `loadMoreError` so the existing items remain visible (the
 * user keeps what they have).
 *
 * Error classification (same contract as `useYouTubeChannels`):
 *   - AuthError (401) is RE-THROWN — the calling router's
 *     ProtectedRoute navigates to /login.
 *   - ApiError surfaces the server message in `kind: "error"`.
 *
 * AbortController lifecycle:
 *   - One AbortController shared by `refetch`, `loadMore`, AND
 *     prop-change refetches. Any new fetch aborts the prior.
 *     loadMore does NOT block a privacy change — if the user
 *     flips to "public" while a "private" cursor-fetch is in
 *     flight, the cursor-fetch aborts cleanly so the
 *     loadMoreError never sets for a stale filter.
 *   - Cleanup on unmount aborts the in-flight fetch to prevent
 *     zombie setState.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { listChannelContent } from "../api/channelContentApi";
import type { ChannelVideo, PrivacyFilter } from "../types";

export interface UseChannelContentOptions {
  /** Numeric accountId from the URL param. */
  accountId: number | null;
  /**
   * Active privacy filter. `"all"` omits the server param
   * entirely (see `buildPrivacyParam`). Changing the filter
   * triggers an auto-refetch with an empty cursor.
   */
  privacy: PrivacyFilter;
  /**
   * Page size. Defaults to 20 (matches the Blocco #2 spec +
   * AccountDetails precedent).
   */
  limit?: number;
}

export type ChannelContentLoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      items: ChannelVideo[];
      /** Opaque token for the next page; absent ⇒ no more pages. */
      nextCursor?: string;
      /** True while a loadMore fetch is in flight. */
      isLoadingMore?: boolean;
      /**
       * Set when a loadMore fetch fails; the existing items stay
       * visible and the user can retry. Cleared on the next
       * successful loadMore attempt.
       */
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };

export interface UseChannelContentResult {
  state: ChannelContentLoadState;
  /** Aborts the in-flight fetch and re-fetches the first page. */
  refetch: () => Promise<void>;
  /**
   * Append the next page using the current `nextCursor`. No-op
   * when state is loading / error / no nextCursor / already
   * loading-more.
   */
  loadMore: () => Promise<void>;
}

export function useChannelContent({
  accountId,
  privacy,
  limit,
}: UseChannelContentOptions): UseChannelContentResult {
  const [state, setState] = useState<ChannelContentLoadState>({
    kind: "loading",
  });
  const abortRef = useRef<AbortController | null>(null);
  // Latest props are read inside runFetch/loadMore/refetch so
  // callers don't need to wrap actions in useCallback deps manually.
  const propsRef = useRef({ accountId, privacy, limit });
  propsRef.current = { accountId, privacy, limit };
  // Same pattern for STATE: stateRef.current is the LATEST state,
  // read inside runFetch/loadMore without making `state` a callback
  // dep. Without this, the cursor passed to listChannelContent on
  // the append path would always be undefined (since the closure
  // was created when state was `{ kind: "loading" }`) and loadMore
  // would silently repeat page 1.
  const stateRef = useRef<ChannelContentLoadState>(state);
  stateRef.current = state;

  const runFetch = useCallback(
    async (
      signal: AbortSignal,
      mode: "refresh" | "append",
    ): Promise<void> => {
      const { accountId: id, privacy: p, limit: l } = propsRef.current;
      if (id == null) {
        // Defensive: caller asked for a fetch before resolving
        // accountId. Skip silently (no error, no state change).
        return;
      }
      try {
        if (mode === "append") {
          // Mark isLoadingMore on the existing ready state.
          setState((prev) =>
            prev.kind === "ready"
              ? { ...prev, isLoadingMore: true, loadMoreError: undefined }
              : prev,
          );
        }
        // READ THE CURSOR FROM STATE REF (not the captured `state`).
        // The captured `state` is from the time `runFetch` was last
        // re-created — with empty deps it stays `{ kind: "loading" }`
        // forever, so a read from it would ALWAYS return undefined.
        // stateRef.current mutates synchronously each render.
        const latest = stateRef.current;
        const cursor =
          mode === "append" && latest.kind === "ready"
            ? latest.nextCursor
            : undefined;
        const result = await listChannelContent({
          accountId: id,
          privacy: p,
          limit: l ?? 20,
          ...(cursor != null ? { cursor } : {}),
          signal,
        });
        if (signal.aborted) return;
        setState((prev) => {
          if (mode === "append" && prev.kind === "ready") {
            // Append: keep existing items, append the new page.
            return {
              kind: "ready",
              items: [...prev.items, ...result.items],
              ...(result.next_cursor != null
                ? { nextCursor: result.next_cursor }
                : {}),
              isLoadingMore: false,
            };
          }
          // Refresh: replace fully.
          return {
            kind: "ready",
            items: result.items,
            ...(result.next_cursor != null
              ? { nextCursor: result.next_cursor }
              : {}),
          };
        });
      } catch (err) {
        if (signal.aborted) return;
        if (err instanceof AuthError) {
          // Re-thrown so the caller's router-level ProtectedRoute
          // redirects to /login. Same contract as useYouTubeChannels
          // and useCreatePost.
          throw err;
        }
        const message =
          err instanceof ApiError
            ? err.message
            : "Unable to load channel content.";
        setState((prev) => {
          if (mode === "append" && prev.kind === "ready") {
            // Append failure: keep existing items, surface the
            // error inline so the user can retry without losing
            // what they've already loaded.
            return { ...prev, isLoadingMore: false, loadMoreError: message };
          }
          return { kind: "error", message };
        });
      }
    },
    // Empty deps are CORRECT here: every read of "live" data
    // (accountId/privacy/limit, current state) goes through a
    // useRef that is updated synchronously each render. Including
    // state as a dep would re-create this callback on every
    // state transition, which would in turn re-fire the auto-refetch
    // effect and cause an infinite render loop.
    [],
  );

  // Auto-refetch on accountId OR privacy change. We pass accountId
  // AND privacy as a stable stringified key so flips between
  // equivalent values (e.g. "all" rerenders) still trigger a
  // fetch — but NOT a rerender that doesn't change either prop.
  useEffect(() => {
    if (accountId == null) {
      // Undefined accountId → keep "loading" so the page doesn't
      // pretend content exists for a missing route param.
      setState({ kind: "loading" });
      return;
    }
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState({ kind: "loading" });
    void runFetch(ctrl.signal, "refresh");
  }, [accountId, privacy, runFetch]);

  // Unmount cleanup.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const refetch = useCallback(async (): Promise<void> => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState({ kind: "loading" });
    await runFetch(ctrl.signal, "refresh");
  }, [runFetch]);

  const loadMore = useCallback(async (): Promise<void> => {
    // Same stateRef pattern: read the LIVE values for gating,
    // not the captured `state` from this useCallback's last
    // render (useCallback with [runFetch] deps is stable, which
    // is what we want so consumers can pass loadMore as a memo
    // dep without churn).
    const latest = stateRef.current;
    if (latest.kind !== "ready") return;
    if (latest.isLoadingMore) return;
    if (!latest.nextCursor) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    await runFetch(ctrl.signal, "append");
  }, [runFetch]);

  return { state, refetch, loadMore };
}
