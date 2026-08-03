import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import { toastBus } from "../../components/toast/toast-bus";
import type { LivestreamRow, LivestreamsResponse } from "./livestreamsTypes";

const POLL_INTERVAL_MS = 30_000;

export type LivestreamsState =
  | { kind: "loading" }
  | { kind: "ready"; items: LivestreamRow[] }
  | { kind: "error"; message: string };

/**
 * Fetch the workspace-scoped livestream rows.
 *
 * The backend requires `workspace_id` (404-shaped without it), so the
 * active workspace is resolved from /auth/me on every cycle — this
 * keeps the list correct when the user switches workspace (the JWT
 * workspace_id is re-stamped server-side on switch).
 */
async function fetchItems(signal: AbortSignal): Promise<LivestreamRow[]> {
  if (isDemoMode()) return [];
  const meResponse = await authedFetch("/api/v1/auth/me", { signal });
  const me = (await meResponse.json()) as { workspace_id?: number };
  const workspaceID = me.workspace_id;
  if (typeof workspaceID !== "number" || !Number.isInteger(workspaceID) || workspaceID <= 0) {
    return [];
  }
  const params = new URLSearchParams({ workspace_id: String(workspaceID) });
  const response = await authedFetch(`/api/v1/livestreams?${params.toString()}`, { signal });
  const data = (await response.json()) as LivestreamsResponse;
  return Array.isArray(data.items) ? data.items : [];
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

  const load = useCallback(async (initial: boolean) => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    if (initial) setState({ kind: "loading" });
    try {
      const items = await fetchItems(controller.signal);
      if (controller.signal.aborted) return;
      setState({ kind: "ready", items });
    } catch (err) {
      if (controller.signal.aborted) return;
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
    const onVisibility = () => {
      if (document.visibilityState === "visible") void load(false);
    };
    document.addEventListener("visibilitychange", onVisibility);
    const timer = window.setInterval(() => {
      if (document.visibilityState === "hidden") return;
      void load(false);
    }, POLL_INTERVAL_MS);
    return () => {
      abortRef.current?.abort();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [load]);

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

  return { state, deletingID, deleteLivestream, reload };
}
