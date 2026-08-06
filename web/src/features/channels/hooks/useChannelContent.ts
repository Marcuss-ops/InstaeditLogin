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
 *                                              isLoadingMore?, loadMoreError?,
 *                                              cacheBust }
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
 *
 * ─── Refresh options ───────────────────────────────────────────────────
 *
 * `refetchOnWindowFocus` — fires the same refetch code path that
 * `refetch` exposes whenever the browser tab regains focus. Useful
 * after the user closes the Velox Dark Editor popup and refocuses
 * the channel tab — the live-update hook (useYouTubePublishLiveUpdate)
 * may not have fired (BC has no echo on the same sender tab), but
 * the focus event definitely has. Cleanup removes the listener on
 * unmount; React StrictMode double-mount is handled by the natural
 * addEventListener/removeEventListener cycle.
 *
 * `refetchInterval` — three forms:
 *   - `number`: poll every N milliseconds.
 *   - `null | undefined`: no polling (the default).
 *   - `(state) => number | null`: predicate evaluated against the
 *     LIVE state. The interval is rescheduled as soon as the
 *     predicate returns a NEW number (including transitions to
 *     and from `null`). This is the spec's "5–10s while any
 *     video is processing/publishing or just updated" requirement:
 *     callers wire `(state) => items.some(v => v.status ∈
 *     {processing, publishing}) ? 5_000 : null`.
 *
 * `cacheBust` — exposed twice:
 *   1. On the `ready` state shape as a stable field — the
 *      timestamp of the LAST successful fetch, bumped on every
 *      refill / append / refresh.
 *   2. As a top-level field on the hook return — same value,
 *      exposed without forcing a read through the state union.
 *
 *   Caller pattern:
 *     const { state, cacheBust } = useChannelContent({...});
 *     <ChannelVideoCard video={v} cacheBust={cacheBust} />
 *
 *   Why stable-from-state and not generated per-render: a
 *   per-render `Date.now()` would invalidate the browser image
 *   cache on every React re-render and cause visible flicker on
 *   unrelated state changes (e.g. `isLoadingMore` toggling).
 *   Tying it to the fetch lifecycle means the timestamp changes
 *   ONLY when content actually changes.
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
   * Page size. Defaults to 20 (matching the AccountDetails precedent).
   */
  limit?: number;
  /**
   * Whether the hook may fetch immediately. Page shells can keep this
   * false so provider-backed content is loaded only after an explicit
   * user action. Default `true` preserves the hook's standalone contract.
   */
  enabled?: boolean;
  /**
   * When true, the hook listens to window `focus` events and
   * fires the same refetch code path the `refetch` callback
   * exposes. Default `false` (opt-in — see comment above).
   */
  refetchOnWindowFocus?: boolean;
  /**
   * Polling interval. Three forms:
   *   - number (ms): poll every N ms;
   *   - null/undefined: no polling (default);
   *   - predicate `(state) => number | null`: dynamic cadence,
   *     rescheduled on every state transition.
   *
   * The default `null` keeps the hook quiet when the caller
   * doesn't opt in.
   */
  refetchInterval?:
    | number
    | null
    | ((state: ChannelContentLoadState) => number | null);
}

export type ChannelContentLoadState =
  | { kind: "idle" }
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
      /**
       * Timestamp (epoch ms) of the LAST successful fetch. Bumped
       * on every completed refresh / append / reload. Page-level
       * thumbnail cache-busters read this to invalidate the
       * browser image cache when a new thumbnail revision comes
       * from the server.
       *
       * Initialized on first successful fetch; absent from the
       * type because the loading branch doesn't have one.
       */
      cacheBust: number;
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
  /**
   * Latest `cacheBust` from any successful fetch. Same value as
   * `state.cacheBust` when `state.kind === "ready"`, otherwise 0
   * (the page-level default). Callers wire this into
   * <ChannelVideoCard cacheBust={cacheBust} /> so the image URL
   * busts on every successful refetch.
   */
  cacheBust: number;
}

function deriveErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return "Unable to load channel content.";
}

export function useChannelContent({
  accountId,
  privacy,
  limit,
  enabled = true,
  refetchOnWindowFocus = false,
  refetchInterval = null,
}: UseChannelContentOptions): UseChannelContentResult {
  const [state, setState] = useState<ChannelContentLoadState>({
    kind: enabled ? "loading" : "idle",
  });
  const abortRef = useRef<AbortController | null>(null);
  const loadMoreInFlightRef = useRef(false);
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
  // Stable handle for the refetch callback so window-focus +
  // interval listeners don't churn on every render. cfetchFnRef
  // is updated synchronously each render — listeners always call
  // the LATEST refetch.
  const cfetchFnRef = useRef<() => Promise<void>>(async () => {});

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
        const result = (await listChannelContent({
          accountId: id,
          privacy: p,
          limit: l ?? 20,
          ...(cursor != null ? { cursor } : {}),
          signal,
        })) ?? { items: [] };
        if (signal.aborted) return;
        // Bump the cache-bust timestamp on every successful fetch.
        // Single epoch-millis value shared by all consumers of the
        // hook return; same value lives on the ready state for
        // tight-coupling with state-driven rendering paths.
        const freshBust = Date.now();
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
              cacheBust: freshBust,
            };
          }
          // Refresh: replace fully.
          return {
            kind: "ready",
            items: result.items,
            ...(result.next_cursor != null
              ? { nextCursor: result.next_cursor }
              : {}),
            cacheBust: freshBust,
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
        const message = deriveErrorMessage(err);
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
    if (loadMoreInFlightRef.current) return;
    loadMoreInFlightRef.current = true;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      await runFetch(ctrl.signal, "append");
    } finally {
      loadMoreInFlightRef.current = false;
    }
  }, [runFetch]);

  // Always-fresh refetch handle for window-focus + interval
  // listeners. The interaction with useCallback is delicate:
  //   - useCallback creates a new refetch whenever its deps change
  //     (currently only `runFetch` which is stable).
  //   - The window-focus + interval effects should be stable too,
  //     so they fire unconditionally across renders.
  //   - cfetchFnRef makes the LISTENER stable while letting the
  //     function it calls point at the latest refetch closure.
  cfetchFnRef.current = refetch;

  // Auto-refetch on accountId OR privacy change. We pass accountId
  // AND privacy as deps so flips between equivalent values (e.g.
  // "all" rerenders) still trigger a fetch.
  useEffect(() => {
    if (!enabled) {
      abortRef.current?.abort();
      setState({ kind: accountId == null ? "loading" : "idle" });
      return;
    }
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
    void runFetch(ctrl.signal, "refresh").catch(() => {});
  }, [accountId, privacy, enabled, runFetch]);

  // ─── refetchOnWindowFocus ──────────────────────────────────────────
  // Skip registration when accountId is null — the handler is a
  // safe no-op anyway (runFetch early-returns) but the listener
  // is dead weight. accountId goes into the dep array so flipping
  // back to a real id re-installs the listener automatically.
  useEffect(() => {
    if (!enabled || !refetchOnWindowFocus) return;
    if (accountId == null) return;
    if (typeof window === "undefined") return;
    const handler = (): void => {
      // Trigger the latest refetch closure. cfcatch swallows
      // AuthError so the listener doesn't leak rejects; the
      // router-level boundary handles navigation.
      void cfetchFnRef.current().catch((err: unknown) => {
        if (err instanceof AuthError) return;
        throw err;
      });
    };
    window.addEventListener("focus", handler);
    return () => {
      window.removeEventListener("focus", handler);
    };
  }, [enabled, refetchOnWindowFocus, accountId]);

  // ─── refetchInterval ───────────────────────────────────────────────
  // Resolve the predicate to a concrete ms value whenever the
  // state changes. The interval reschedules when the result
  // changes (null → 5000 → null all clear+restart the timer).
  const intervalMs = (() => {
    if (refetchInterval == null) return null;
    if (typeof refetchInterval === "number") return refetchInterval;
    return refetchInterval(state);
  })();
  useEffect(() => {
    if (!enabled || intervalMs == null) return;
    if (typeof window === "undefined") return;
    const id = window.setInterval(() => {
      void cfetchFnRef.current().catch((err: unknown) => {
        if (err instanceof AuthError) return;
        throw err;
      });
    }, intervalMs);
    return () => {
      window.clearInterval(id);
    };
  }, [intervalMs, enabled]);

  // Unmount cleanup.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  // Top-level cacheBust mirror for callers that prefer not to
  // descend through the ready union. Returns 0 when state is
  // loading/error (cards default to a no-bust thumbnail URL in
  // that case).
  const cacheBust = state.kind === "ready" ? state.cacheBust : 0;

  return { state, refetch, loadMore, cacheBust };
}
