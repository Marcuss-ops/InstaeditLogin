import { useCallback, useEffect, useState } from "react";
import { useNavigate, Link, useSearchParams } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Cell,
  AreaChart,
  Area,
} from "recharts";
import {
  BarChart3,
  Users,
  Eye,
  Video,
  Trophy,
  TrendingUp,
  TrendingDown,
  ArrowRight,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { ErrorState } from "../../components/feedback";

type MetricGrowth = {
  absolute: number;
  percent: number;
};

type ChannelMetrics = {
  subscribers: number;
  views: number;
  videos: number;
};

type ChannelSummary = {
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

type RankingItem = {
  id: number;
  username: string;
  value: number;
};

type RankingValueLabel =
  | "subscribers"
  | "views"
  | "videos"
  | "percent"
  | "engagement";

type Rankings = {
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

type Aggregates = {
  channels: number;
  subscribers: number;
  views: number;
  videos: number;
};

type TrendPoint = {
  date: string;
  subscribers: number;
  views: number;
  videos: number;
  engagement: number;
};

type SummaryData = {
  period_days: number;
  aggregates: Aggregates;
  channels: ChannelSummary[];
  rankings: Rankings;
  trends: TrendPoint[];
};

type WorkspaceOption = {
  id: number;
  name: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: SummaryData }
  | { kind: "error"; message: string };

const PERIODS = [
  { days: 7, label: "7D" },
  { days: 30, label: "30D" },
  { days: 90, label: "90D" },
] as const;

function formatNumber(value: number | string): string {
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return String(value);
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

function formatTrendDate(value: string) {
  const d = new Date(value + "T00:00:00Z");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function TrendChart({
  title,
  data,
  dataKey,
  color,
  valueFormatter = formatNumber,
  axisFormatter = valueFormatter,
}: {
  title: string;
  data: TrendPoint[];
  dataKey: "subscribers" | "views" | "engagement";
  color: string;
  valueFormatter?: (value: number) => string;
  axisFormatter?: (value: number) => string;
}) {
  if (data.length === 0) {
    return (
      <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
        <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
        <div className="h-64 flex items-center justify-center rounded-2xl border border-dashed border-white/[0.12]">
          <p className="text-[13px] text-[#9aa0aa]">No trend data yet.</p>
        </div>
      </div>
    );
  }
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
      <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} syncId="trend-charts" margin={{ top: 5, right: 5, left: -10, bottom: 0 }}>
            <defs>
              <linearGradient id={`${dataKey}Gradient`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={color} stopOpacity={0.4} />
                <stop offset="100%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke="rgba(255,255,255,0.06)" vertical={false} />
            <XAxis
              dataKey="date"
              tick={{ fill: "#9aa0aa", fontSize: 12 }}
              axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
              tickFormatter={formatTrendDate}
              tickLine={false}
              minTickGap={16}
            />
            <YAxis
              tick={{ fill: "#9aa0aa", fontSize: 12 }}
              axisLine={false}
              tickLine={false}
              tickFormatter={axisFormatter}
            />
            <Tooltip
              contentStyle={{
                background: "#1f1f2e",
                border: "1px solid rgba(255,255,255,0.12)",
                borderRadius: 12,
                color: "#e8e8ef",
              }}
              labelFormatter={(label) => formatTrendDate(String(label))}
              formatter={(value) => [valueFormatter(Number(value)), title]}
            />
            <Area
              type="monotone"
              dataKey={dataKey}
              stroke={color}
              fill={`url(#${dataKey}Gradient)`}
              strokeWidth={2}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function GrowthText({ value }: { value: MetricGrowth }) {
  const positive = value.absolute >= 0;
  return (
    <span
      className={cn(
        "text-[12px] font-semibold",
        positive ? "text-emerald-400" : "text-red-400",
      )}
    >
      {positive ? "+" : ""}
      {formatNumber(value.absolute)} ({value.percent.toFixed(1)}%)
    </span>
  );
}

function KPICard({
  label,
  value,
  icon: Icon,
}: {
  label: string;
  value: number;
  icon: React.ElementType;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa]">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-white mt-1">
            {formatNumber(value)}
          </p>
        </div>
        <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa]">
          <Icon size={20} />
        </div>
      </div>
    </div>
  );
}

function RankingCard({
  title,
  icon: Icon,
  items,
  valueLabel,
}: {
  title: string;
  icon: React.ElementType;
  items: RankingItem[];
  valueLabel: RankingValueLabel;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
      <div className="flex items-center gap-2 mb-4">
        <Icon size={18} className="text-[#9aa0aa]" />
        <h3 className="text-[15px] font-bold text-white">{title}</h3>
      </div>
      <div className="space-y-2">
        {items.slice(0, 5).map((item, index) => (
          <div
            key={item.id}
            className="flex items-center justify-between py-2 px-3 rounded-xl bg-white/[0.03] border border-white/[0.06]"
          >
            <div className="flex items-center gap-3 min-w-0">
              <span className="w-5 h-5 rounded-full bg-white/[0.08] text-[11px] font-bold text-white flex items-center justify-center shrink-0">
                {index + 1}
              </span>
              <Link
                to={`/app/accounts/${item.id}`}
                className="text-[13px] font-medium text-white truncate hover:text-[#9aa0aa] transition-colors no-underline"
              >
                @{item.username}
              </Link>
            </div>
            <span className="text-[13px] font-semibold text-white tabular-nums">
              {(() => {
                switch (valueLabel) {
                  case "percent":
                    return `${(item.value / 10).toFixed(1)}%`;
                  case "engagement":
                    return `${(item.value / 10).toFixed(1)} /video`;
                  default:
                    return formatNumber(item.value);
                }
              })()}
            </span>
          </div>
        ))}
        {items.length === 0 && (
          <p className="text-[13px] text-[#9aa0aa]">No data yet.</p>
        )}
      </div>
    </div>
  );
}

export function ChannelsPerformancePage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const period = Number.parseInt(searchParams.get("days") || "30", 10);

  // Local filter inputs are only committed to the URL (and therefore
  // to the API call) when the user presses Apply. This avoids a
  // re-fetch on every keystroke and keeps the form usable.
  const [localFilters, setLocalFilters] = useState({
    workspace: searchParams.get("workspace") || "",
    group: searchParams.get("group") || "",
    language: searchParams.get("language") || "",
    manager: searchParams.get("manager") || "",
  });
  const [workspaces, setWorkspaces] = useState<WorkspaceOption[]>([]);
  const [workspacesLoading, setWorkspacesLoading] = useState(false);
  const [workspacesError, setWorkspacesError] = useState(false);

  // Keep local inputs in sync with the URL when it changes externally
  // (initial load, back/forward navigation, clear filters).
  useEffect(() => {
    setLocalFilters({
      workspace: searchParams.get("workspace") || "",
      group: searchParams.get("group") || "",
      language: searchParams.get("language") || "",
      manager: searchParams.get("manager") || "",
    });
  }, [searchParams]);

  // Load available workspaces once so the workspace filter can be a
  // dropdown instead of a free-form text field.
  useEffect(() => {
    async function loadWorkspaces() {
      setWorkspacesLoading(true);
      setWorkspacesError(false);
      try {
        const response = await authedFetch("/api/v1/workspaces");
        const data = (await response.json()) as { workspaces: WorkspaceOption[] };
        setWorkspaces(data.workspaces ?? []);
      } catch (err) {
        setWorkspacesError(true);
        console.error("Failed to load workspaces", err);
      } finally {
        setWorkspacesLoading(false);
      }
    }
    void loadWorkspaces();
  }, []);

  const setPeriod = useCallback(
    (days: number) => {
      const next = new URLSearchParams(searchParams);
      next.set("days", String(days));
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const applyFilters = useCallback(() => {
    const next = new URLSearchParams(searchParams);
    if (localFilters.workspace) {
      next.set("workspace", localFilters.workspace);
    } else {
      next.delete("workspace");
    }
    if (localFilters.group) {
      next.set("group", localFilters.group);
    } else {
      next.delete("group");
    }
    if (localFilters.language) {
      next.set("language", localFilters.language);
    } else {
      next.delete("language");
    }
    if (localFilters.manager) {
      next.set("manager", localFilters.manager);
    } else {
      next.delete("manager");
    }
    setSearchParams(next, { replace: true });
  }, [localFilters, searchParams, setSearchParams]);

  const clearFilters = useCallback(() => {
    setSearchParams({ days: String(period) }, { replace: true });
  }, [period, setSearchParams]);

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const params = new URLSearchParams(searchParams);
      if (!params.has("days")) {
        params.set("days", "30");
      }
      const response = await authedFetch(
        `/api/v1/accounts/performance/summary?${params.toString()}`,
      );
      const data = (await response.json()) as SummaryData;
      setState({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load channel performance.";
      setState({ kind: "error", message });
    }
  }, [navigate, searchParams]);

  useEffect(() => {
    void load();
  }, [load]);

  const topSubscribers =
    state.kind === "ready"
      ? state.data.rankings.by_subscribers.slice(0, 5).map((item) => ({
          name: item.username,
          value: item.value,
        }))
      : [];

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <BarChart3 size={28} className="text-white/40" />
              Channel Performance
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Aggregated KPIs and rankings across all your YouTube channels.
            </p>
          </div>
          <div className="flex items-center gap-2">
            {PERIODS.map((p) => (
              <button
                key={p.days}
                type="button"
                onClick={() => setPeriod(p.days)}
                className={cn(
                  "px-4 py-2 rounded-xl text-[13px] font-semibold border transition-all",
                  period === p.days
                    ? "bg-white text-black border-white"
                    : "bg-white/[0.04] border-white/[0.08] text-[#9aa0aa] hover:text-white hover:bg-white/[0.08]",
                )}
              >
                {p.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => void load()}
              className="ml-2 inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        {/* Filters */}
        <div className="mb-6 p-4 rounded-2xl bg-[#1f1f2e] border border-white/[0.12]">
          <div className="flex items-center gap-2 mb-3">
            <SlidersHorizontal size={16} className="text-[#9aa0aa]" />
            <span className="text-[13px] font-semibold text-white">Filters</span>
          </div>
          <div className="flex flex-col lg:flex-row lg:items-end gap-3">
            <div className="flex-1 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
              <div className="flex flex-col gap-1">
                <label htmlFor="filter-workspace" className="text-[12px] font-medium text-[#9aa0aa]">
                  Workspace
                </label>
                <select
                  id="filter-workspace"
                  value={localFilters.workspace}
                  onChange={(e) =>
                    setLocalFilters((prev) => ({ ...prev, workspace: e.target.value }))
                  }
                  disabled={workspacesLoading}
                  className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] text-white focus:outline-none focus:ring-2 focus:ring-white/10 disabled:opacity-50"
                >
                  <option value="">All workspaces</option>
                  {workspaces.map((ws) => (
                    <option key={ws.id} value={ws.id}>
                      {ws.name}
                    </option>
                  ))}
                </select>
                {workspacesError && (
                  <span className="text-[11px] text-red-400">Unable to load workspaces.</span>
                )}
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="filter-group" className="text-[12px] font-medium text-[#9aa0aa]">
                  Group
                </label>
                <input
                  id="filter-group"
                  type="text"
                  value={localFilters.group}
                  onChange={(e) =>
                    setLocalFilters((prev) => ({ ...prev, group: e.target.value }))
                  }
                  placeholder="Group name"
                  className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa]/60 focus:outline-none focus:ring-2 focus:ring-white/10"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="filter-language" className="text-[12px] font-medium text-[#9aa0aa]">
                  Language
                </label>
                <input
                  id="filter-language"
                  type="text"
                  value={localFilters.language}
                  onChange={(e) =>
                    setLocalFilters((prev) => ({ ...prev, language: e.target.value }))
                  }
                  placeholder="e.g. en"
                  className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa]/60 focus:outline-none focus:ring-2 focus:ring-white/10"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="filter-manager" className="text-[12px] font-medium text-[#9aa0aa]">
                  Manager
                </label>
                <input
                  id="filter-manager"
                  type="text"
                  value={localFilters.manager}
                  onChange={(e) =>
                    setLocalFilters((prev) => ({ ...prev, manager: e.target.value }))
                  }
                  placeholder="Manager name"
                  className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa]/60 focus:outline-none focus:ring-2 focus:ring-white/10"
                />
              </div>
            </div>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={clearFilters}
                className="px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-[#9aa0aa] hover:text-white hover:bg-white/[0.08] transition-colors"
              >
                Clear
              </button>
              <button
                type="button"
                onClick={applyFilters}
                className="px-4 py-2 rounded-xl bg-white text-black border border-white text-[13px] font-semibold hover:bg-white/90 transition-colors"
              >
                Apply
              </button>
            </div>
          </div>
        </div>

        {state.kind === "loading" && (
          <div className="space-y-6">
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <div
                  key={i}
                  className="h-32 rounded-2xl bg-white/[0.06] animate-pulse"
                />
              ))}
            </div>
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <div className="h-80 rounded-2xl bg-white/[0.06] animate-pulse" />
              <div className="h-80 rounded-2xl bg-white/[0.06] animate-pulse" />
            </div>
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load channel performance"
            message={state.message}
            onRetry={() => void load()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && (
          <>
            {/* KPI Cards */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
              <KPICard
                label="Channels"
                value={state.data.aggregates.channels}
                icon={Video}
              />
              <KPICard
                label="Total subscribers"
                value={state.data.aggregates.subscribers}
                icon={Users}
              />
              <KPICard
                label="Total views"
                value={state.data.aggregates.views}
                icon={Eye}
              />
              <KPICard
                label="Total videos"
                value={state.data.aggregates.videos}
                icon={Video}
              />
            </div>

            {/* Trends */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
              <TrendChart
                title="Subscribers trend"
                data={state.data.trends}
                dataKey="subscribers"
                color="#a78bfa"
              />
              <TrendChart
                title="Views trend"
                data={state.data.trends}
                dataKey="views"
                color="#22d3ee"
              />
              <TrendChart
                title="Engagement (views / video)"
                data={state.data.trends}
                dataKey="engagement"
                color="#f472b6"
                valueFormatter={(value) => `${formatNumber(value)} /video`}
                axisFormatter={formatNumber}
              />
            </div>

            {/* Charts + Rankings */}
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
              <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
                <h2 className="text-[16px] font-bold text-white mb-4">
                  Top channels by subscribers
                </h2>
                <div className="h-72">
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart
                      data={topSubscribers}
                      layout="vertical"
                      margin={{ top: 5, right: 30, left: 40, bottom: 5 }}
                    >
                      <CartesianGrid stroke="rgba(255,255,255,0.06)" horizontal={false} />
                      <XAxis
                        type="number"
                        tick={{ fill: "#9aa0aa", fontSize: 12 }}
                        axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
                        tickFormatter={formatNumber}
                        tickLine={false}
                      />
                      <YAxis
                        type="category"
                        dataKey="name"
                        tick={{ fill: "#9aa0aa", fontSize: 12 }}
                        axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
                        tickLine={false}
                        width={80}
                      />
                      <Tooltip
                        contentStyle={{
                          background: "#1f1f2e",
                          border: "1px solid rgba(255,255,255,0.12)",
                          borderRadius: 12,
                          color: "#e8e8ef",
                        }}
                        formatter={(value) => {
                          const num = typeof value === "number" ? value : Number(value ?? 0);
                          return [formatNumber(num), "Subscribers"];
                        }}
                      />
                      <Bar dataKey="value" radius={[0, 6, 6, 0]}>
                        {topSubscribers.map((_, index) => (
                          <Cell
                            key={`cell-${index}`}
                            fill={index === 0 ? "#fbbf24" : "#a78bfa"}
                          />
                        ))}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              </div>

              <RankingCard
                title="Fastest growing (subscribers)"
                icon={TrendingUp}
                items={state.data.rankings.fastest_growing_subscribers}
                valueLabel="percent"
              />
            </div>

            {/* Rankings grid */}
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6 mb-8">
              <RankingCard
                title="Top by subscribers"
                icon={Trophy}
                items={state.data.rankings.by_subscribers}
                valueLabel="subscribers"
              />
              <RankingCard
                title="Top by views"
                icon={Eye}
                items={state.data.rankings.by_views}
                valueLabel="views"
              />
              <RankingCard
                title="Top by videos"
                icon={Video}
                items={state.data.rankings.by_videos}
                valueLabel="videos"
              />
              <RankingCard
                title="Fastest growing (views)"
                icon={TrendingUp}
                items={state.data.rankings.fastest_growing_views}
                valueLabel="percent"
              />
              <RankingCard
                title="Top engagement"
                icon={TrendingUp}
                items={state.data.rankings.top_engagement}
                valueLabel="engagement"
              />
            </div>

            <h2 className="text-[16px] font-bold text-white mb-4">Bottom performers</h2>
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6 mb-8">
              <RankingCard
                title="Bottom by subscribers"
                icon={TrendingDown}
                items={state.data.rankings.bottom_subscribers}
                valueLabel="subscribers"
              />
              <RankingCard
                title="Bottom by views"
                icon={TrendingDown}
                items={state.data.rankings.bottom_views}
                valueLabel="views"
              />
              <RankingCard
                title="Bottom engagement"
                icon={TrendingDown}
                items={state.data.rankings.bottom_engagement}
                valueLabel="engagement"
              />
              <RankingCard
                title="Slowest growing (subscribers)"
                icon={TrendingDown}
                items={state.data.rankings.bottom_growing_subscribers}
                valueLabel="percent"
              />
              <RankingCard
                title="Slowest growing (views)"
                icon={TrendingDown}
                items={state.data.rankings.bottom_growing_views}
                valueLabel="percent"
              />
            </div>

            {/* Channel table */}
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
              <h2 className="text-[16px] font-bold text-white mb-4">All channels</h2>
              <div className="overflow-x-auto">
                <table className="w-full text-left border-collapse">
                  <thead>
                    <tr className="border-b border-white/[0.08]">
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Channel</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Subscribers</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Videos</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Subscribers Δ</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views Δ</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Videos Δ</th>
                      <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider"> </th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.data.channels
                      .slice()
                      .sort((a, b) => b.metrics.subscribers - a.metrics.subscribers)
                      .map((channel) => (
                        <tr
                          key={channel.id}
                          className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors"
                        >
                          <td className="py-3 pr-4 text-[13px] text-white font-medium">
                            @{channel.username}
                          </td>
                          <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                            {formatNumber(channel.metrics.subscribers)}
                          </td>
                          <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                            {formatNumber(channel.metrics.views)}
                          </td>
                          <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                            {formatNumber(channel.metrics.videos)}
                          </td>
                          <td className="py-3 pr-4 text-right tabular-nums">
                            <GrowthText value={channel.growth.subscribers} />
                          </td>
                          <td className="py-3 pr-4 text-right tabular-nums">
                            <GrowthText value={channel.growth.views} />
                          </td>
                          <td className="py-3 pr-4 text-right tabular-nums">
                            <GrowthText value={channel.growth.videos} />
                          </td>
                          <td className="py-3 pr-4 text-right">
                            <Link
                              to={`/app/accounts/${channel.id}/performance`}
                              className="inline-flex items-center gap-1 text-[12px] font-semibold text-[#9aa0aa] hover:text-white transition-colors no-underline"
                            >
                              Details <ArrowRight size={12} />
                            </Link>
                          </td>
                        </tr>
                      ))}
                  </tbody>
                </table>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
