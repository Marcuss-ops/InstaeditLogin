import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";

export type DashboardTopVideo = {
  video_id: string;
  title: string;
  thumbnail_url?: string;
  views: number;
  published_at?: string | null;
  channel_name: string;
  youtube_url: string;
};

export type DashboardChannelRow = {
  id: number;
  username: string;
  views: number;
  views_growth: { absolute: number; percent: number };
  revenue_cents?: number | null;
  revenue_growth?: { absolute: number; percent: number } | null;
};

export type DashboardAnalyticsData = {
  period_days: number;
  aggregates: {
    channels: number;
    views: number;
    subscribers: number;
    videos: number;
    revenue_cents?: number | null;
  };
  operations?: {
    published: number;
    failed: number;
    copyright_claims: number;
    average_processing_seconds?: number | null;
  };
  channels: DashboardChannelRow[];
  top_videos: DashboardTopVideo[];
	generated_at: string;
	data_updated_at?: string;
};

export type DashboardAnalyticsState =
  | { kind: "loading" }
  | { kind: "ready"; data: DashboardAnalyticsData }
  | { kind: "error"; message: string };

export const DASHBOARD_PERIODS = [
  { days: 1, label: "1G" },
  { days: 7, label: "7G" },
  { days: 14, label: "14G" },
  { days: 28, label: "28G" },
  { days: 90, label: "90G" },
] as const;

// ─── Client-side cache (in-memory + localStorage) ────────────────
// YouTube analytics change slowly (daily metric-history snapshots +
// a 1-hour server-side cache), so returning to the Dashboard should
// not refetch on every visit. The Refresh button bypasses the cache
// with { force: true }.
//
// Both cache layers are scoped per user (userId from fetchSession):
// analytics include revenue, so a cache entry must NEVER leak from
// one account to the next on a shared browser.
const DASHBOARD_CACHE_TTL_MS = 60 * 60 * 1000; // 1 ora
const DASHBOARD_CACHE_STORAGE_PREFIX = "dashboard.analytics.v1";

type DashboardCacheEntry = {
  data: DashboardAnalyticsData;
  expiresAt: number;
};

// In-memory cache shared across hook instances and page mounts, so
// navigating away and back within the same tab never refetches.
// Keyed by `${userId}:${days}`.
const memoryCache = new Map<string, DashboardCacheEntry>();

function cacheKeyFor(userKey: string, days: number): string {
  return `${userKey}:${days}`;
}

function readStoredEntry(userKey: string, days: number): DashboardCacheEntry | null {
  try {
    const raw = localStorage.getItem(
      `${DASHBOARD_CACHE_STORAGE_PREFIX}.${userKey}.${days}`,
    );
    if (!raw) return null;
    const entry = JSON.parse(raw) as DashboardCacheEntry;
    if (!entry || typeof entry.expiresAt !== "number" || !entry.data) return null;
    return entry;
  } catch {
    return null; // Corrupted / older-shape payloads are ignored.
  }
}

function writeStoredEntry(userKey: string, days: number, entry: DashboardCacheEntry): void {
  try {
    localStorage.setItem(
      `${DASHBOARD_CACHE_STORAGE_PREFIX}.${userKey}.${days}`,
      JSON.stringify(entry),
    );
  } catch {
    // Quota exceeded / storage unavailable — the in-memory cache
    // still serves this tab session.
  }
}

/**
 * Clears every dashboard analytics cache entry (in-memory + all
 * per-user localStorage entries). Exposed for logout and test
 * isolation — mirrors the clearAccountsCache / clearSharedQueryCache
 * helpers used in other modules.
 */
export function clearDashboardAnalyticsCache(): void {
  memoryCache.clear();
  try {
    const prefix = `${DASHBOARD_CACHE_STORAGE_PREFIX}.`;
    const keys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key && key.startsWith(prefix)) keys.push(key);
    }
    for (const key of keys) localStorage.removeItem(key);
  } catch {
    // Storage unavailable — nothing to clear.
  }
}

/**
 * Fetches the analytics-only dashboard read model for the requested
 * period (1/7/14/28/90 days), backed by a per-user 1-hour client
 * cache. `load(days, { force: true })` bypasses the cache (Refresh
 * button); if that refresh fails, previously-cached data is shown
 * instead of blanking the dashboard. Group memberships deliberately
 * live in the Groups section and are never fetched here.
 */
export function useDashboardAnalytics(periodDays: number) {
  const navigate = useNavigate();
  const [state, setState] = useState<DashboardAnalyticsState>({
    kind: "loading",
  });
  // Guards against out-of-order responses when the period changes
  // while a previous request is still in flight.
  const requestToken = useRef(0);

  const load = useCallback(
    async (days: number, opts?: { force?: boolean }) => {
      const token = ++requestToken.current;
      const now = Date.now();

      // Resolve the current user so cache entries never cross accounts.
      let userKey = "anon";
      try {
        const session = await fetchSession();
        if (session) userKey = String(session.userId);
      } catch {
        // fetchSession is fail-closed and never rejects; keep safe anyway.
      }
      const cacheKey = cacheKeyFor(userKey, days);

      if (!opts?.force) {
        const cached = memoryCache.get(cacheKey) ?? readStoredEntry(userKey, days);
        if (cached && cached.expiresAt > now) {
          memoryCache.set(cacheKey, cached);
          setState({ kind: "ready", data: cached.data });
          return;
        }
      }

      setState({ kind: "loading" });
      try {
		const refreshQuery = opts?.force ? "&refresh=1" : "";
		const response = await authedFetch(
			`/api/v1/dashboard/analytics?days=${days}${refreshQuery}`,
		);
        const data = (await response.json()) as DashboardAnalyticsData;
        const entry: DashboardCacheEntry = {
          data,
          expiresAt: Date.now() + DASHBOARD_CACHE_TTL_MS,
        };
        memoryCache.set(cacheKey, entry);
        writeStoredEntry(userKey, days, entry);
        if (token === requestToken.current) {
          setState({ kind: "ready", data });
        }
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        if (token !== requestToken.current) {
          return; // Superseded by a newer request — ignore.
        }
        // A refresh/network failure must not blank a dashboard that
        // already has data: fall back to any cached entry (even an
        // expired one) rather than showing a hard error.
        const cached = memoryCache.get(cacheKey) ?? readStoredEntry(userKey, days);
        if (cached) {
          setState({ kind: "ready", data: cached.data });
          return;
        }
        const message =
          err instanceof Error ? err.message : "Unable to load dashboard analytics.";
        setState({ kind: "error", message });
      }
    },
    [navigate],
  );

  useEffect(() => {
    void load(periodDays);
  }, [periodDays, load]);

  return { state, load };
}
