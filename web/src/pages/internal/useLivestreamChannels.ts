import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import type { LivestreamChannel, LivestreamChannelsResponse } from "./livestreamsTypes";

export type LivestreamChannelsState =
  | { kind: "loading" }
  | { kind: "ready"; channels: LivestreamChannel[] }
  | { kind: "error"; message: string };

/**
 * Fetch the creation-wizard preflight rows (GET
 * /api/v1/livestreams/channels) for the active workspace. The backend
 * requires `workspace_id`, resolved from /auth/me (the same pattern
 * as the control-center hook).
 */
async function fetchChannels(signal: AbortSignal): Promise<LivestreamChannel[]> {
  if (isDemoMode()) return [];
  const meResponse = await authedFetch("/api/v1/auth/me", { signal });
  const me = (await meResponse.json()) as { workspace_id?: number };
  const workspaceID = me.workspace_id;
  if (typeof workspaceID !== "number" || !Number.isInteger(workspaceID) || workspaceID <= 0) {
    return [];
  }
  const params = new URLSearchParams({ workspace_id: String(workspaceID) });
  const response = await authedFetch(`/api/v1/livestreams/channels?${params.toString()}`, { signal });
  const data = (await response.json()) as LivestreamChannelsResponse;
  return Array.isArray(data.channels) ? data.channels : [];
}

export function useLivestreamChannels() {
  const [state, setState] = useState<LivestreamChannelsState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });
    try {
      const channels = await fetchChannels(controller.signal);
      if (controller.signal.aborted) return;
      setState({ kind: "ready", channels });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        setState({ kind: "error", message: "Sessione scaduta. Accedi di nuovo." });
        return;
      }
      const message = err instanceof Error ? err.message : "Impossibile caricare i canali.";
      setState({ kind: "error", message });
    }
  }, []);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  return { state, reload: load };
}
