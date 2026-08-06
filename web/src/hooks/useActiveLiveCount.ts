import { API_BASE_URL } from "../lib/api";
import { authedFetch, fetchSession } from "../lib/auth";
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

async function fetchWorkspaceID(signal: AbortSignal): Promise<number | null> {
  // ProtectedRoute and the internal data loaders already resolve /auth/me.
  // Reuse that cached session instead of starting a second polling request
  // which can keep producing 401s after the browser session expires.
  const session = await fetchSession();
  return signal.aborted ? null : session?.workspaceId ?? null;
}

async function fetchActiveLiveCount(workspaceID: number, signal: AbortSignal): Promise<number | null> {
  const params = new URLSearchParams({ workspace_id: String(workspaceID) });
  const response = await authedFetch(`/api/v1/livestreams?${params}`, { signal });
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
