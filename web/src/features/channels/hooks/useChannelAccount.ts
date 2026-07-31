/**
 * useChannelAccount — single-account record loader.
 *
 * Calls `GET /api/v1/accounts/{id}` via the api barrel and exposes
 * the result through a state machine:
 *
 *   loading  →  ready { account: ChannelAccount }
 *            ↘ error { message }
 *
 * Refetch / unmount semantics mirror `useYouTubeChannels` and
 * `useChannelContent`:
 *   • AbortController on mount; a fresh one on `refetch()` and
 *     whenever `accountId` changes (via the auto-refetch effect).
 *   • AuthError (401) is RE-THROWN so the caller navigates to
 *     /login (ProtectedRoute handles the redirect).
 *   • ApiError surfaces the server's typed message in
 *     `kind: "error"`.
 *   • accountId=null stays in `kind: "loading"` (no fetch fires).
 *   • Unmount cleanup aborts the in-flight fetch to prevent
 *     zombie setState.
 *
 * The hook is intentionally MINIMAL — it does not cache the last
 * successful account across accountId shifts because the page
 * (DashboardChannels) only renders the new accountId; keeping
 * stale data here would mislead the page.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { getChannelAccount } from "../api/accountApi";
import type { ChannelAccount } from "../types";

export interface UseChannelAccountOptions {
  /** Numeric accountId from the URL param. Null when the route
   *  hasn't resolved (e.g. /dashboard-channels/) — the hook stays
   *  in loading rather than firing a malformed request. */
  accountId: number | null;
}

export type ChannelAccountLoadState =
  | { kind: "loading" }
  | { kind: "ready"; account: ChannelAccount }
  | { kind: "error"; message: string };

export interface UseChannelAccountResult {
  state: ChannelAccountLoadState;
  /** Aborts the in-flight fetch and re-fetches the account. */
  refetch: () => Promise<void>;
}

export function useChannelAccount({
  accountId,
}: UseChannelAccountOptions): UseChannelAccountResult {
  const [state, setState] = useState<ChannelAccountLoadState>({
    kind: "loading",
  });
  const abortRef = useRef<AbortController | null>(null);
  // Latest accountId is read inside refetch so callers don't have
  // to wrap it in useCallback deps; mirrors the propsRef pattern
  // used in useChannelContent.
  const propsRef = useRef({ accountId });
  propsRef.current = { accountId };

  const runFetch = useCallback(async (signal: AbortSignal) => {
    const id = propsRef.current.accountId;
    if (id == null) {
      // Defensive: caller asked for a fetch before resolving
      // accountId. Skip silently (no error, no state change).
      return;
    }
    try {
      const account = await getChannelAccount({ accountId: id, signal });
      if (signal.aborted) return;
      setState({ kind: "ready", account });
    } catch (err) {
      if (signal.aborted) return;
      if (err instanceof AuthError) {
        throw err;
      }
      setState({
        kind: "error",
        message:
          err instanceof ApiError
            ? err.message
            : "Unable to load channel account.",
      });
    }
  }, []);

  // Auto-fetch on mount + on accountId change.
  useEffect(() => {
    if (accountId == null) {
      setState({ kind: "loading" });
      return;
    }
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState({ kind: "loading" });
    // Effects cannot propagate a rejected promise to the router. Keep
    // the state in loading on auth expiry and let the protected route /
    // auth boundary handle the redirect without an unhandled rejection.
    void runFetch(ctrl.signal).catch(() => {});
  }, [accountId, runFetch]);

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
    await runFetch(ctrl.signal);
  }, [runFetch]);

  return { state, refetch };
}
