export type PerformanceFilters = { workspace: string; group: string; language: string; manager: string; };

export type MetricGrowth = {
  absolute: number;
  percent: number;
};

export type ChannelMetrics = {
  subscribers: number;
  views: number;
  videos: number;
};

export type ChannelSummary = {
  id: number;
  platform: string;
  username: string;
  metrics: ChannelMetrics;
  growth: {
    subscribers: MetricGrowth;
    views: MetricGrowth;
    videos: MetricGrowth;
  };
};

export type RankingItem = {
  id: number;
  username: string;
  value: number;
};

export type RankingValueLabel =
  | "subscribers"
  | "views"
  | "videos"
  | "percent"
  | "engagement";

export type Rankings = {
  by_subscribers: RankingItem[];
  by_views: RankingItem[];
  by_videos: RankingItem[];
  fastest_growing_subscribers: RankingItem[];
  fastest_growing_views: RankingItem[];
  top_engagement: RankingItem[];
  bottom_subscribers: RankingItem[];
  bottom_views: RankingItem[];
  bottom_engagement: RankingItem[];
  bottom_growing_subscribers: RankingItem[];
  bottom_growing_views: RankingItem[];
};

export type Aggregates = {
  channels: number;
  subscribers: number;
  views: number;
  videos: number;
};

export type TrendPoint = {
  date: string;
  subscribers: number;
  views: number;
  videos: number;
  engagement: number;
};

export type SummaryData = {
  period_days: number;
  aggregates: Aggregates;
  channels: ChannelSummary[];
  rankings?: Rankings;
  trends: TrendPoint[];
};

export type WorkspaceOption = {
  id: number;
  name: string;
};

export type GroupOption = {
  id: number;
  name: string;
};

export type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: SummaryData }
  | { kind: "error"; message: string };

export const PERIODS = [
  { days: 7, label: "7D" },
  { days: 30, label: "30D" },
  { days: 90, label: "90D" },
] as const;
