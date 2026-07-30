import { useCallback, useEffect, useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import {
  ArrowLeft,
  RefreshCw,
  TrendingUp,
  TrendingDown,
  Users,
  Eye,
  Video,
  Calendar,
  Table,
} from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { authedFetch, AuthError } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { ErrorState } from "../../components/feedback";

type PerformanceMetricGrowth = {
  absolute: number;
  percent: number;
};

type PerformanceSummary = {
  subscribers: number;
  views: number;
  videos: number;
  engagement_rate: number;
  publication_frequency: number;
};

type PerformanceHistoryPoint = {
  date: string;
  subscribers: number;
  views: number;
  videos: number;
};

type PerformanceData = {
  summary: PerformanceSummary;
  growth: {
    subscribers: PerformanceMetricGrowth;
    views: PerformanceMetricGrowth;
    videos: PerformanceMetricGrowth;
  };
  history: PerformanceHistoryPoint[];
  period_days: number;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: PerformanceData }
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
  if (Number.isInteger(n)) return `${n}`;
  return n.toFixed(2);
}

function formatDate(value: string | number): string {
  const iso = typeof value === "number" ? String(value) : value;
  return new Date(iso).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

function GrowthBadge({ value }: { value: PerformanceMetricGrowth }) {
  const positive = value.absolute >= 0;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-[12px] font-semibold px-2 py-0.5 rounded-full",
        positive
          ? "bg-emerald-500/[0.10] text-emerald-400 border border-emerald-500/[0.20]"
          : "bg-red-500/[0.10] text-red-400 border border-red-500/[0.20]",
      )}
    >
      {positive ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
      {positive ? "+" : ""}
      {formatNumber(value.absolute)} ({value.percent.toFixed(1)}%)
    </span>
  );
}

function MetricCard({
  label,
  value,
  icon: Icon,
  growth,
  subtitle,
}: {
  label: string;
  value: number;
  icon: React.ElementType;
  growth?: PerformanceMetricGrowth;
  subtitle?: string;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
      <div className="flex items-start justify-between mb-3">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa]">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-white mt-1">
            {formatNumber(value)}
          </p>
          {subtitle && (
            <p className="text-[12px] text-[#9aa0aa] mt-1">{subtitle}</p>
          )}
        </div>
        <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa]">
          <Icon size={20} />
        </div>
      </div>
      {growth && <GrowthBadge value={growth} />}
    </div>
  );
}

function HistoryTable({ history }: { history: PerformanceHistoryPoint[] }) {
  if (history.length === 0) {
    return (
      <div className="text-center py-12 rounded-2xl border border-dashed border-white/[0.12] bg-white/[0.02]">
        <p className="text-[14px] text-[#9aa0aa]">
          No historical data yet. Sync the account to start collecting metrics.
        </p>
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-white/[0.08]">
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Date</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Subscribers</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Δ</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Δ</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Videos</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Δ</th>
          </tr>
        </thead>
        <tbody>
          {[...history].reverse().map((row, index, arr) => {
            const prev = arr[index + 1];
            const subsDelta = prev ? row.subscribers - prev.subscribers : 0;
            const viewsDelta = prev ? row.views - prev.views : 0;
            const videosDelta = prev ? row.videos - prev.videos : 0;
            return (
              <tr
                key={row.date}
                className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors"
              >
                <td className="py-3 pr-4 text-[13px] text-white whitespace-nowrap">
                  {new Date(row.date).toLocaleDateString(undefined, {
                    weekday: "short",
                    year: "numeric",
                    month: "short",
                    day: "numeric",
                  })}
                </td>
                <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                  {formatNumber(row.subscribers)}
                </td>
                <td className={cn("py-3 pr-4 text-[13px] text-right tabular-nums", subsDelta >= 0 ? "text-emerald-400" : "text-red-400")}>
                  {subsDelta > 0 ? `+${formatNumber(subsDelta)}` : formatNumber(subsDelta)}
                </td>
                <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                  {formatNumber(row.views)}
                </td>
                <td className={cn("py-3 pr-4 text-[13px] text-right tabular-nums", viewsDelta >= 0 ? "text-emerald-400" : "text-red-400")}>
                  {viewsDelta > 0 ? `+${formatNumber(viewsDelta)}` : formatNumber(viewsDelta)}
                </td>
                <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">
                  {formatNumber(row.videos)}
                </td>
                <td className={cn("py-3 pr-4 text-[13px] text-right tabular-nums", videosDelta >= 0 ? "text-emerald-400" : "text-red-400")}>
                  {videosDelta > 0 ? `+${formatNumber(videosDelta)}` : formatNumber(videosDelta)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function AccountPerformancePage() {
  const { accountId } = useParams<{ accountId: string }>();
  const navigate = useNavigate();
  const [period, setPeriod] = useState<number>(30);
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const response = await authedFetch(
        `/api/v1/accounts/${accountId}/performance?days=${period}`,
      );
      const data = (await response.json()) as PerformanceData;
      setState({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load performance.";
      setState({ kind: "error", message });
    }
  }, [accountId, navigate, period]);

  useEffect(() => {
    void load();
  }, [load, period]);

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-6xl mx-auto">
        {/* Header */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div className="flex items-center gap-3">
            <Link
              to={`/app/accounts/${accountId}`}
              className="inline-flex items-center gap-1.5 text-[13px] text-[#9aa0aa] hover:text-white transition-colors no-underline"
            >
              <ArrowLeft size={14} /> Back to account
            </Link>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        <div className="mb-8">
          <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white">
            Performance
          </h1>
          <p className="text-[15px] text-[#9aa0aa] mt-1">
            Historical metrics and growth trends for this channel.
          </p>
        </div>

        {state.kind === "loading" && (
          <div className="space-y-6">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
              <div className="h-32 rounded-2xl bg-white/[0.06] animate-pulse" />
              <div className="h-32 rounded-2xl bg-white/[0.06] animate-pulse" />
              <div className="h-32 rounded-2xl bg-white/[0.06] animate-pulse" />
            </div>
            <div className="h-80 rounded-2xl bg-white/[0.06] animate-pulse" />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load performance"
            message={state.message}
            onRetry={() => void load()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && (
          <>
            {/* Period selector */}
            <div className="flex items-center gap-2 mb-6">
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
            </div>

            {/* Summary cards */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4 mb-8">
              <MetricCard
                label="Subscribers"
                value={state.data.summary.subscribers}
                icon={Users}
                growth={state.data.growth.subscribers}
              />
              <MetricCard
                label="Views"
                value={state.data.summary.views}
                icon={Eye}
                growth={state.data.growth.views}
              />
              <MetricCard
                label="Videos"
                value={state.data.summary.videos}
                icon={Video}
                growth={state.data.growth.videos}
              />
              <MetricCard
                label="Engagement"
                value={state.data.summary.engagement_rate}
                icon={TrendingUp}
                subtitle="views / video"
              />
              <MetricCard
                label="Frequency"
                value={state.data.summary.publication_frequency}
                icon={Calendar}
                subtitle="videos / day"
              />
            </div>

            {/* Subscribers chart */}
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 mb-6">
              <h2 className="text-[16px] font-bold text-white mb-4">Subscribers</h2>
              <div className="h-80">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={state.data.history}>
                    <defs>
                      <linearGradient id="subscribersGradient" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor="#a78bfa" stopOpacity={0.4} />
                        <stop offset="100%" stopColor="#a78bfa" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid stroke="rgba(255,255,255,0.06)" vertical={false} />
                    <XAxis
                      dataKey="date"
                      tickFormatter={formatDate}
                      tick={{ fill: "#9aa0aa", fontSize: 12 }}
                      axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
                      tickLine={false}
                    />
                    <YAxis
                      tickFormatter={formatNumber}
                      tick={{ fill: "#9aa0aa", fontSize: 12 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "#1f1f2e",
                        border: "1px solid rgba(255,255,255,0.12)",
                        borderRadius: 12,
                        color: "#e8e8ef",
                      }}
                      labelFormatter={(label) => formatDate(String(label))}
                    />
                    <Area
                      type="monotone"
                      dataKey="subscribers"
                      stroke="#a78bfa"
                      fill="url(#subscribersGradient)"
                      strokeWidth={2}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>

            {/* Views + Videos chart */}
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 mb-6">
              <h2 className="text-[16px] font-bold text-white mb-4">Views &amp; Videos</h2>
              <div className="h-80">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={state.data.history}>
                    <CartesianGrid stroke="rgba(255,255,255,0.06)" vertical={false} />
                    <XAxis
                      dataKey="date"
                      tickFormatter={formatDate}
                      tick={{ fill: "#9aa0aa", fontSize: 12 }}
                      axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
                      tickLine={false}
                    />
                    <YAxis
                      yAxisId="left"
                      tickFormatter={formatNumber}
                      tick={{ fill: "#9aa0aa", fontSize: 12 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <YAxis
                      yAxisId="right"
                      orientation="right"
                      tickFormatter={formatNumber}
                      tick={{ fill: "#9aa0aa", fontSize: 12 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "#1f1f2e",
                        border: "1px solid rgba(255,255,255,0.12)",
                        borderRadius: 12,
                        color: "#e8e8ef",
                      }}
                      labelFormatter={(label) => formatDate(String(label))}
                    />
                    <Legend wrapperStyle={{ color: "#9aa0aa" }} />
                    <Line
                      yAxisId="left"
                      type="monotone"
                      dataKey="views"
                      name="Views"
                      stroke="#22d3ee"
                      strokeWidth={2}
                      dot={false}
                    />
                    <Line
                      yAxisId="right"
                      type="monotone"
                      dataKey="videos"
                      name="Videos"
                      stroke="#f472b6"
                      strokeWidth={2}
                      dot={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            </div>

            {/* Time-series table */}
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 mb-6">
              <div className="flex items-center gap-2 mb-4">
                <Table className="w-4 h-4 text-[#9aa0aa]" />
                <h2 className="text-[16px] font-bold text-white">Daily history</h2>
              </div>
              <HistoryTable history={state.data.history} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
