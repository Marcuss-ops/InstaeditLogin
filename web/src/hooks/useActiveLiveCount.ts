import { API_BASE_URL } from "../lib/api";
import { isDemoMode } from "../lib/demo";
import { useSharedQuery } from "../lib/queryRegistry";

/**
 * A livestream row as returned by GET /api/v1/livestreams (see the
 * livestream module plan). Only the state machine's `actual_state` is
 * needed here: the sidebar badge must count streams that are REALLY
 * live, not merely scheduled.
 */
export type LivestreamSummary = {
  actual_state?: string;
};

const POLL_INTERVAL_MS = 30_000;

export function livestreamsURL(workspaceID: number): string {
  const params = new URLSearchParams({ workspace_id: String(workspaceID) });
  return `${API_BASE_URL}/api/v1/livestreams?${params}`;
}

/** Count only rows whose actual state is live. */
export function countActiveLives(payload: unknown): number {
  if (!payload) return 0;
  const items = Array.isArray(payload)
    ? payload
    : Array.isArray((payload as { items?: unknown }).items)
      ? (payload as { items: unknown[] }).items
      : [];
  return items.reduce(
    (total, item) =>
      total + ((item as LivestreamSummary | null)?.actual_state === "live" ? 1 : 0),
    0,
  );
}

type WorkspaceResponse = { workspace_id?: number };

async function fetchActiveLiveCount(signal: AbortSignal): Promise<number | null> {
  const meResponse = await fetch(`${API_BASE_URL}/api/v1/auth/me`, {
    credentials: "include",
    signal,
  });
  if (!meResponse.ok) return null;
  const me = (await meResponse.json()) as WorkspaceResponse;
  const workspaceID = me.workspace_id;
  if (typeof workspaceID !== "number" || !Number.isInteger(workspaceID) || workspaceID <= 0) {
    return null;
  }
  const response = await fetch(livestreamsURL(workspaceID), {
    credentials: "include",
    signal,
  });
  if (!response.ok) return null;
  return countActiveLives(await response.json());
}

/**
 * Shared, deduplicated sidebar badge query. The registry keeps one
 * request/polling loop when multiple layout surfaces mount the badge,
 * pauses it while the tab is hidden, and wakes it on visibility restore.
 */
export function useActiveLiveCount(): number | null {
  const query = useSharedQuery<number | null>("active-live-count", {
    enabled: !isDemoMode(),
    staleTime: 15_000,
    pollingInterval: POLL_INTERVAL_MS,
    fetcher: fetchActiveLiveCount,
  });
  return query.data ?? null;
}
