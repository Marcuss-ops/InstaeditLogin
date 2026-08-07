import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError } from "../../lib/auth";

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
    videos: number;
    revenue_cents?: number | null;
  };
  channels: DashboardChannelRow[];
  top_videos: DashboardTopVideo[];
  generated_at: string;
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

/**
 * Fetches the analytics-only dashboard read model for the requested
 * period (1/7/14/28/90 days). Group memberships deliberately live in
 * the Groups section and are never fetched here.
 */
export function useDashboardAnalytics(periodDays: number) {
  const navigate = useNavigate();
  const [state, setState] = useState<DashboardAnalyticsState>({
    kind: "loading",
  });

  const load = useCallback(
    async (days: number) => {
      setState({ kind: "loading" });
      try {
        const response = await authedFetch(
          `/api/v1/dashboard/analytics?days=${days}`,
        );
        const data = (await response.json()) as DashboardAnalyticsData;
        setState({ kind: "ready", data });
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
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
