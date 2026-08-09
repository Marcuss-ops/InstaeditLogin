import { useState } from "react";
import {
  BarChart3,
  DollarSign,
  Eye,
  RefreshCw,
  TrendingUp,
  Users,
  Video,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { Skeleton, ErrorState } from "../../components/feedback";
import {
  DASHBOARD_PERIODS,
  useDashboardAnalytics,
  type DashboardChannelRow,
  type DashboardTopVideo,
} from "./useDashboardAnalytics";

function formatNumber(value: number): string {
  if (Number.isNaN(value)) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return `${value}`;
}

function formatCents(cents: number | null | undefined): string {
  if (cents == null) return "—";
  return `$${(cents / 100).toFixed(2)}`;
}

function formatGrowth(value: { absolute: number; percent: number } | null | undefined) {
  if (!value) return <span className="text-[12px] text-[#9aa0aa]">—</span>;
  const positive = value.absolute >= 0;
  return (
    <span
      className={cn(
        "text-[12px] font-semibold tabular-nums",
        positive ? "text-emerald-400" : "text-red-400",
      )}
    >
      {positive ? "+" : ""}
      {formatNumber(value.absolute)} ({value.percent.toFixed(1)}%)
    </span>
  );
}

function KpiCard({
  label,
  value,
  icon: Icon,
  variant = "default",
}: {
  label: string;
  value: string;
  icon: React.ElementType;
  variant?: "default" | "success";
}) {
  return (
    <div
      className={cn(
        "rounded-2xl p-5 border",
        variant === "success"
          ? "bg-emerald-500/[0.08] border-emerald-500/20 text-emerald-400"
          : "bg-[#1f1f2e] border-white/[0.12] text-white",
      )}
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight">{value}</p>
        </div>
        <div
          className={cn(
            "w-10 h-10 rounded-xl flex items-center justify-center",
            variant === "success"
              ? "bg-white/[0.08]"
              : "bg-white/[0.04] border border-white/[0.08] text-[#9aa0aa]",
          )}
        >
          <Icon size={20} />
        </div>
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
      <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
      {children}
    </div>
  );
}

function ViewsTable({ rows }: { rows: DashboardChannelRow[] }) {
  const sorted = rows.slice().sort((a, b) => b.views - a.views);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-white/[0.08]">
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Channel</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views Δ</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={row.id} className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors">
              <td className="py-3 pr-4 text-[13px] text-white font-medium">@{row.username}</td>
              <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">{formatNumber(row.views)}</td>
              <td className="py-3 pr-4 text-right tabular-nums">{formatGrowth(row.views_growth)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RevenueTable({ rows }: { rows: DashboardChannelRow[] }) {
  const sorted = rows
    .filter((row) => row.revenue_cents != null)
    .slice()
    .sort((a, b) => (b.revenue_cents ?? 0) - (a.revenue_cents ?? 0));
  if (sorted.length === 0) {
    return (
      <p className="text-[13px] text-[#9aa0aa]">
        Nessun dato di revenue disponibile per questo periodo.
      </p>
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-white/[0.08]">
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Channel</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Revenue</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Revenue Δ</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={row.id} className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors">
              <td className="py-3 pr-4 text-[13px] text-white font-medium">@{row.username}</td>
              <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">{formatCents(row.revenue_cents)}</td>
              <td className="py-3 pr-4 text-right tabular-nums">{formatGrowth(row.revenue_growth)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TopVideosTable({ videos }: { videos: DashboardTopVideo[] }) {
  if (videos.length === 0) {
    return (
      <p className="text-[13px] text-[#9aa0aa]">
        Nessun video pubblicato in questo periodo.
      </p>
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-white/[0.08]">
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Video</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">Channel</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Views</th>
            <th scope="col" className="py-3 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">Pubblicato</th>
          </tr>
        </thead>
        <tbody>
          {videos.map((video) => (
            <tr key={video.video_id} className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors">
              <td className="py-3 pr-4">
                <a
                  href={video.youtube_url}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-3 no-underline group"
                >
                  {video.thumbnail_url ? (
                    <img
                      src={video.thumbnail_url}
                      alt=""
                      className="w-16 h-10 rounded-lg object-cover bg-white/[0.06]"
                      loading="lazy"
                    />
                  ) : (
                    <span className="w-16 h-10 rounded-lg bg-white/[0.06] flex items-center justify-center">
                      <Video size={16} className="text-[#9aa0aa]" />
                    </span>
                  )}
                  <span className="text-[13px] text-white font-medium group-hover:text-[#e8e8ef] line-clamp-2">
                    {video.title || "(Senza titolo)"}
                  </span>
                </a>
              </td>
              <td className="py-3 pr-4 text-[13px] text-[#9aa0aa]">@{video.channel_name}</td>
              <td className="py-3 pr-4 text-[13px] text-white text-right tabular-nums">{formatNumber(video.views)}</td>
              <td className="py-3 pr-4 text-[13px] text-[#9aa0aa] text-right tabular-nums">
                {video.published_at
                  ? new Date(video.published_at).toLocaleDateString(undefined, {
                      day: "2-digit",
                      month: "short",
                      year: "numeric",
                    })
                  : "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function InternalDashboard() {
  const [periodDays, setPeriodDays] = useState(28);
  const { state, load } = useDashboardAnalytics(periodDays);

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <BarChart3 size={28} className="text-white/40" />
              Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Analytics totali di tutti i tuoi canali YouTube.
            </p>
            {state.kind === "ready" && (
              <p className="text-[12px] text-[#6b7280] mt-0.5">
				Ultimo dato salvato alle{" "}
				{new Date(state.data.data_updated_at ?? state.data.generated_at).toLocaleString(undefined, {
					hour: "2-digit",
					minute: "2-digit",
					day: "2-digit",
					month: "2-digit",
				})}
              </p>
            )}
          </div>
          <div className="flex items-center gap-2">
            {DASHBOARD_PERIODS.map((p) => (
              <button
                key={p.days}
                type="button"
                onClick={() => setPeriodDays(p.days)}
                className={cn(
                  "px-4 py-2 rounded-xl text-[13px] font-semibold border transition-all",
                  periodDays === p.days
                    ? "bg-white text-black border-white"
                    : "bg-white/[0.04] border-white/[0.08] text-[#9aa0aa] hover:text-white hover:bg-white/[0.08]",
                )}
              >
                {p.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => void load(periodDays, { force: true })}
              className="ml-2 inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-2 lg:grid-cols-5 gap-4 mb-8">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} variant="card" height={104} />
            ))}
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load dashboard analytics"
            message={state.message}
            onRetry={() => void load(periodDays, { force: true })}
            className="mb-8 bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && (
          <>
            <div className="grid grid-cols-2 lg:grid-cols-5 gap-4 mb-8">
              <KpiCard
                label="Views totali"
                value={formatNumber(state.data.aggregates.views)}
                icon={Eye}
              />
              <KpiCard
                label="Iscritti"
                value={formatNumber(state.data.aggregates.subscribers)}
                icon={Users}
              />
              <KpiCard
                label="Revenue"
                value={formatCents(state.data.aggregates.revenue_cents)}
                icon={DollarSign}
                variant="success"
              />
              <KpiCard
                label="Canali"
                value={formatNumber(state.data.aggregates.channels)}
                icon={TrendingUp}
              />
              <KpiCard
                label="Video"
                value={formatNumber(state.data.aggregates.videos)}
                icon={Video}
              />
            </div>

            <Section title="Migliori video">
              <TopVideosTable videos={state.data.top_videos} />
            </Section>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mt-6">
              <Section title="Views">
                <ViewsTable rows={state.data.channels} />
              </Section>
              <Section title="Revenue">
                <RevenueTable rows={state.data.channels} />
              </Section>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
