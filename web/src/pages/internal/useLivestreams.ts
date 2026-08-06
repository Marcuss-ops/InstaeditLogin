import { useCallback, useEffect, useRef, useState } from "react";
import { useSharedPolling } from "../../lib/queryRegistry";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import { toastBus } from "../../components/toast/toast-bus";
import type { LivestreamRow, LivestreamsResponse } from "./livestreamsTypes";

const POLL_INTERVAL_MS = 30_000;

export type LivestreamsState =
  | { kind: "loading" }
  | {
      kind: "ready";
      items: LivestreamRow[];
      nextCursor?: string;
      hasMore: boolean;
      loadingMore?: boolean;
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };

/**
 * Fetch the workspace-scoped livestream rows.
 *
 * The backend requires `workspace_id` (404-shaped without it), so the
 * active workspace is resolved from /auth/me on every cycle — this
 * keeps the list correct when the user switches workspace (the JWT
 * workspace_id is re-stamped server-side on switch).
 */
async function fetchItems(signal: AbortSignal, cursor?: string): Promise<LivestreamsResponse> {
  if (isDemoMode()) return { items: [] };
  const meResponse = await authedFetch("/api/v1/auth/me", { signal });
  const me = (await meResponse.json()) as { workspace_id?: number };
  const workspaceID = me.workspace_id;
  if (typeof workspaceID !== "number" || !Number.isInteger(workspaceID) || workspaceID <= 0) {
    return { items: [] };
  }
  const params = new URLSearchParams({ workspace_id: String(workspaceID), limit: "50" });
  if (cursor) params.set("cursor", cursor);
  const response = await authedFetch(`/api/v1/livestreams?${params.toString()}`, { signal });
  return (await response.json()) as LivestreamsResponse;
}

/**
 * Control-center data hook.
 *
 * Polls every 30s (paused while the tab is hidden) so the "live" state
 * of a running stream stays fresh. `initial=true` shows the loading
 * state and surfaces errors; silent refreshes keep the last good data
 * when the backend blips, so the page never flickers.
 */
export function useLivestreams() {
  const [state, setState] = useState<LivestreamsState>({ kind: "loading" });
  const [deletingID, setDeletingID] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async (initial: boolean, sharedSignal?: AbortSignal) => {
    abortRef.current?.abort();
    const controller = sharedSignal ? null : new AbortController();
    const signal = sharedSignal ?? controller!.signal;
    if (controller) abortRef.current = controller;
    if (initial) setState({ kind: "loading" });
    try {
      const data = await fetchItems(signal);
      if (signal.aborted) return;
      setState({
        kind: "ready",
        items: Array.isArray(data.items) ? data.items : [],
        nextCursor: data.next_cursor,
        hasMore: data.has_more === true,
      });
    } catch (err) {
      if (signal.aborted) return;
      if (err instanceof AuthError) {
        // Session expired — the protected layout redirects to /login.
        setState({ kind: "error", message: "Sessione scaduta. Accedi di nuovo." });
        return;
      }
      const message = err instanceof Error ? err.message : "Impossibile caricare le live.";
      // Silent refresh: keep the last good list instead of flashing an
      // error state over data the operator is still looking at.
      setState((prev) =>
        initial || prev.kind !== "ready" ? { kind: "error", message } : prev,
      );
    }
  }, []);

  useEffect(() => {
    void load(true);
    return () => abortRef.current?.abort();
  }, [load]);

  useSharedPolling("livestreams:current-session", {
    interval: POLL_INTERVAL_MS,
    task: async (signal) => {
      await load(false, signal);
    },
  });

  const loadMore = useCallback(async () => {
    if (state.kind !== "ready" || !state.hasMore || !state.nextCursor || state.loadingMore) return;
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    setState((previous) => previous.kind === "ready" ? { ...previous, loadingMore: true, loadMoreError: undefined } : previous);
    try {
      const data = await fetchItems(controller.signal, state.nextCursor);
      if (controller.signal.aborted) return;
      setState((previous) => previous.kind === "ready" ? {
        kind: "ready",
        items: [...previous.items, ...(data.items ?? [])],
        nextCursor: data.next_cursor,
        hasMore: data.has_more === true,
      } : previous);
    } catch (err) {
      if (controller.signal.aborted) return;
      setState((previous) => previous.kind === "ready" ? { ...previous, loadingMore: false, loadMoreError: err instanceof Error ? err.message : "Impossibile caricare altre live." } : previous);
    }
  }, [state]);

  const reload = useCallback(() => void load(true), [load]);

  const deleteLivestream = useCallback(async (id: string): Promise<boolean> => {
    setDeletingID(id);
    try {
      await authedFetch(`/api/v1/livestreams/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
      setState((prev) =>
        prev.kind === "ready"
          ? { ...prev, items: prev.items.filter((row) => row.id !== id) }
          : prev,
      );
      toastBus.push("success", "Live eliminata.");
      return true;
    } catch {
      // authedFetch already emitted the error toast.
      return false;
    } finally {
      setDeletingID(null);
    }
  }, []);

  return { state, deletingID, deleteLivestream, reload, loadMore };
}
