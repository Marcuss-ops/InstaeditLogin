import { useCallback, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, AuthError, authedFetch, fetchSession } from "../../lib/auth";
import { listAllAccounts, type PlatformAccount } from "../../features/channels/api/channelsApi";
import { useSharedQuery } from "../../lib/queryRegistry";

type Post = {
  id: number;
  status: string;
  scheduled_at?: string | null;
};

export type DashboardData = {
  accounts: PlatformAccount[];
  posts: Post[];
  pendingUploads: number;
};

export type DashboardFetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: DashboardData }
  | { kind: "error"; message: string };

const DASHBOARD_STALE_TIME_MS = 60_000;
const DASHBOARD_ACTIVE_POLL_MS = 5_000;
const DASHBOARD_IDLE_POLL_MS = 60_000;

function hasActiveWork(data: DashboardData | undefined): boolean {
  if (!data) return false;
  return data.pendingUploads > 0 || data.posts.some((post) =>
    post.status === "queued" || post.status === "publishing",
  );
}

// The dashboard is an analytics-only view: connected accounts, post counts
// and pending uploads. Group memberships live in the Groups section and are
// deliberately not fetched here.
async function fetchDashboardData(signal: AbortSignal): Promise<DashboardData> {
  const session = await fetchSession();
  if (!session) throw new AuthError();
  const [accounts, postsResp, countsResp] = await Promise.all([
    listAllAccounts({ signal }),
    authedFetch("/api/v1/posts", { signal }),
    authedFetch("/api/v1/uploads/counts", { signal }),
  ]);
  if (signal.aborted) throw new DOMException("The request was aborted", "AbortError");

  const postsData = (await postsResp.json()) as { posts: Post[] };
  const countsData = (await countsResp.json()) as {
    counts: Array<{
      account_id: number;
      count: number;
      next_publish_at: string | null;
    }>;
    total_uploads: number;
  };

  return {
    accounts,
    posts: postsData.posts ?? [],
    pendingUploads: countsData.total_uploads ?? 0,
  };
}

/**
 * Shared dashboard read model. The registry deduplicates concurrent mounts,
 * keeps the last successful value for one minute, polls quickly while work
 * is active, slows to one minute when idle, and pauses all timers while the
 * browser tab is hidden. Focus does not force a request; visibility restore
 * is the explicit wake-up path and `refetch` is reserved for user actions.
 */
export function useDashboardData(): {
  state: DashboardFetchState;
  refetch: () => Promise<void>;
} {
  const navigate = useNavigate();
  const query = useSharedQuery<DashboardData>("dashboard:overview", {
    enabled: true,
    staleTime: DASHBOARD_STALE_TIME_MS,
    pollingInterval: (data) => hasActiveWork(data)
      ? DASHBOARD_ACTIVE_POLL_MS
      : DASHBOARD_IDLE_POLL_MS,
    refetchOnWindowFocus: false,
    fetcher: fetchDashboardData,
  });

  useEffect(() => {
    if (query.error instanceof AuthError) {
      navigate("/login", { replace: true });
    }
  }, [navigate, query.error]);

  const refetch = useCallback(async (): Promise<void> => {
    try {
      await query.refetch();
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      if (error instanceof ApiError) throw error;
      throw error;
    }
  }, [navigate, query.refetch]);

  if (query.isLoading && query.data === undefined) {
    return { state: { kind: "loading" }, refetch };
  }
  if (query.error && query.data === undefined) {
    return {
      state: {
        kind: "error",
        message: query.error instanceof Error ? query.error.message : "Unable to load dashboard.",
      },
      refetch,
    };
  }
  if (query.data) return { state: { kind: "ready", data: query.data }, refetch };
  return { state: { kind: "loading" }, refetch };
}
