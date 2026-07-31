import { Link } from "react-router-dom";
import {
  BarChart3,
  TrendingUp,
  ArrowRight,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { ErrorState } from "../../components/feedback";
import { useChannelsPerformance } from "./useChannelsPerformance";
import { formatNumber, TrendChart, TopSubscribersChart } from "./ChannelsPerformanceChart";
import { ChannelsPerformanceKpis } from "./ChannelsPerformanceKpis";
import { ChannelsPerformanceRankings, RankingCard } from "./ChannelsPerformanceRankings";
import type { MetricGrowth } from "./channelsPerformanceTypes";
import { PERIODS } from "./channelsPerformanceTypes";

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


export function ChannelsPerformancePage() {
  const {
    state, period, localFilters, setLocalFilters, workspaces,
    workspacesLoading, workspacesError, setPeriod, applyFilters,
    clearFilters, load, topSubscribers,
  } = useChannelsPerformance();
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
            <ChannelsPerformanceKpis aggregates={state.data.aggregates} />

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
              <TrendChart title="Subscribers trend" data={state.data.trends} dataKey="subscribers" color="#a78bfa" />
              <TrendChart title="Views trend" data={state.data.trends} dataKey="views" color="#22d3ee" />
              <TrendChart
                title="Engagement (views / video)"
                data={state.data.trends}
                dataKey="engagement"
                color="#f472b6"
                valueFormatter={(value) => `${formatNumber(value)} /video`}
                axisFormatter={formatNumber}
              />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
              <TopSubscribersChart data={topSubscribers} />
              {state.data.rankings && (
                <RankingCard
                  title="Fastest growing (subscribers)"
                  icon={TrendingUp}
                  items={state.data.rankings.fastest_growing_subscribers}
                  valueLabel="percent"
                />
              )}
            </div>

            <ChannelsPerformanceRankings rankings={state.data.rankings} />

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
