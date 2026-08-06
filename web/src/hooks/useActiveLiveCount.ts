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

async function fetchWorkspaceID(signal: AbortSignal): Promise<number | null> {
  const response = await fetch(`${API_BASE_URL}/api/v1/auth/me`, {
    credentials: "include",
    signal,
  });
  if (!response.ok) return null;
  const workspaceID = (await response.json() as WorkspaceResponse).workspace_id;
  return typeof workspaceID === "number" && Number.isInteger(workspaceID) && workspaceID > 0
    ? workspaceID
    : null;
}

async function fetchActiveLiveCount(workspaceID: number, signal: AbortSignal): Promise<number | null> {
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
  const workspaceQuery = useSharedQuery<number | null>("auth-me-workspace", {
    enabled: !isDemoMode(),
    staleTime: 30_000,
    pollingInterval: POLL_INTERVAL_MS,
    fetcher: fetchWorkspaceID,
  });
  const workspaceID = workspaceQuery.data;
  const query = useSharedQuery<number | null>(
    `active-live-count:${workspaceID ?? "none"}`,
    {
      enabled: !isDemoMode() && workspaceID != null,
      staleTime: 15_000,
      pollingInterval: POLL_INTERVAL_MS,
      fetcher: (signal) => fetchActiveLiveCount(workspaceID as number, signal),
    },
  );
  return workspaceID == null ? null : query.data ?? null;
}
