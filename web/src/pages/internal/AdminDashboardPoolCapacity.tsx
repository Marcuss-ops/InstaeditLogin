import { useState } from "react";
import { AlertTriangle, ChevronDown, Server, Users } from "lucide-react";
import { Card, Section, formatDate, formatNumber } from "./AdminDashboardKpis";
import { cn } from "../../lib/utils";
import type { YouTubeOAuthPoolCapacityResponse } from "./adminDashboardTypes";

// OAuth pool capacity bars and Google manager account tables
// (extracted from AdminDashboard.tsx, pure file move).

type PoolHealth = "healthy" | "warning" | "high" | "critical" | "blocked";

const poolHealthMeta: Record<
  PoolHealth,
  { label: string; badge: string; bar: string }
> = {
  healthy: {
    label: "Healthy",
    badge: "bg-emerald-500/[0.12] text-emerald-400 border-emerald-500/30",
    bar: "bg-emerald-400",
  },
  warning: {
    label: "Warning",
    badge: "bg-amber-500/[0.12] text-amber-400 border-amber-500/30",
    bar: "bg-amber-400",
  },
  high: {
    label: "High",
    badge: "bg-orange-500/[0.12] text-orange-400 border-orange-500/30",
    bar: "bg-orange-400",
  },
  critical: {
    label: "Critical",
    badge: "bg-red-500/[0.12] text-red-400 border-red-500/30",
    bar: "bg-red-400",
  },
  blocked: {
    label: "Blocked",
    badge: "bg-purple-500/[0.12] text-purple-400 border-purple-500/30",
    bar: "bg-purple-400",
  },
};

function poolHealthOf(health: string): PoolHealth {
  return (["healthy", "warning", "high", "critical", "blocked"] as const).includes(
    health as PoolHealth,
  )
    ? (health as PoolHealth)
    : "healthy";
}

function PoolHealthBadge({ health }: { health: string }) {
  const meta = poolHealthMeta[poolHealthOf(health)];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[11px] font-bold uppercase tracking-wider border",
        meta.badge,
      )}
    >
      <span className={cn("w-1.5 h-1.5 rounded-full", meta.bar)} />
      {meta.label}
    </span>
  );
}

function statusChip(status: string) {
  const normalized = status.toLowerCase();
  if (normalized === "active") {
    return "bg-emerald-500/[0.12] text-emerald-400 border-emerald-500/30";
  }
  if (normalized === "reauth_required") {
    return "bg-amber-500/[0.12] text-amber-400 border-amber-500/30";
  }
  return "bg-white/[0.06] text-[#9aa0aa] border-white/[0.1]";
}

export function PoolCapacityView({ data }: { data: YouTubeOAuthPoolCapacityResponse }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const toggleManager = (subject: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(subject)) next.delete(subject);
      else next.add(subject);
      return next;
    });
  };

  return (
    <div className="mb-8">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-[18px] font-bold text-white flex items-center gap-2">
          <Server size={20} className="text-[#9aa0aa]" />
          YouTube OAuth Pool Capacity
        </h2>
        <div className="text-[12px] text-[#9aa0aa]">
          Generated at {formatDate(data.generated_at_unix)}
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
        <Card
          title="Google managers"
          value={formatNumber(data.totals.managers_total)}
          icon={Users}
        />
        <Card
          title="Channels total"
          value={formatNumber(data.totals.channels_total)}
          icon={Server}
        />
        <Card
          title="Channels to re-link"
          value={formatNumber(data.totals.channels_reauth_required)}
          icon={AlertTriangle}
          variant={data.totals.channels_reauth_required > 0 ? "warning" : "default"}
        />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
        {data.pools.map((pool) => {
          const meta = poolHealthMeta[poolHealthOf(pool.health)];
          const pct = Math.min(
            100,
            (pool.active_refresh_tokens / pool.recommended_capacity) * 100,
          );
          return (
            <div
              key={pool.oauth_client_key}
              className="rounded-2xl p-5 border bg-[#1f1f2e] border-white/[0.12]"
            >
              <div className="flex items-start justify-between mb-3">
                <div>
                  <p className="text-[13px] font-semibold text-white font-mono">
                    {pool.oauth_client_key}
                  </p>
                  <p className="text-[12px] text-[#9aa0aa] mt-0.5">
                    Google hard cap {formatNumber(pool.provider_limit)} per
                    account+client
                  </p>
                </div>
                <PoolHealthBadge health={pool.health} />
              </div>
              <div className="flex items-baseline justify-between mb-2">
                <p className="text-[26px] font-extrabold tracking-tight text-white tabular-nums">
                  {formatNumber(pool.active_refresh_tokens)}
                  <span className="text-[14px] font-semibold text-[#9aa0aa]">
                    {" "}
                    / {formatNumber(pool.recommended_capacity)} active
                  </span>
                </p>
                <p className="text-[12px] text-[#9aa0aa]">
                  {formatNumber(pool.remaining_capacity)} slots left
                </p>
              </div>
              <div className="h-2 rounded-full bg-white/[0.06] overflow-hidden">
                <div
                  className={cn("h-full rounded-full transition-all", meta.bar)}
                  style={{ width: `${Math.max(0, pct)}%` }}
                />
              </div>
            </div>
          );
        })}
      </div>

      <Section title={`Google manager accounts (${data.managers.length})`}>
        {data.managers.length === 0 ? (
          <p className="text-[13px] text-[#9aa0aa]">
            No Google manager accounts connected yet.
          </p>
        ) : (
          <div className="space-y-2">
            {data.managers.map((manager) => {
              const isOpen = expanded.has(manager.provider_subject_id);
              return (
                <div
                  key={manager.provider_subject_id}
                  className="rounded-xl border border-white/[0.08] bg-white/[0.02] overflow-hidden"
                >
                  <button
                    type="button"
                    onClick={() => toggleManager(manager.provider_subject_id)}
                    className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-white/[0.04] transition-colors"
                  >
                    <ChevronDown
                      size={16}
                      className={cn(
                        "text-[#9aa0aa] transition-transform shrink-0",
                        isOpen && "rotate-180",
                      )}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-semibold text-white truncate font-mono">
                        {manager.provider_subject_id}
                      </p>
                      <p className="text-[11px] text-[#9aa0aa] mt-0.5">
                        Pool{" "}
                        <span className="font-mono text-[#e8e8ef]">
                          {manager.oauth_client_key}
                        </span>{" "}
                        · Grant {manager.grant_status} ·{" "}
                        {manager.channels_total} channels ·{" "}
                        {manager.channels_reauth_required} to re-link
                      </p>
                    </div>
                    <span
                      className={cn(
                        "px-2.5 py-0.5 rounded-full text-[11px] font-bold uppercase tracking-wider border shrink-0",
                        statusChip(manager.grant_status),
                      )}
                    >
                      {manager.grant_status}
                    </span>
                  </button>
                  {isOpen && (
                    <div className="border-t border-white/[0.06] divide-y divide-white/[0.04]">
                      {manager.channels.length === 0 ? (
                        <p className="px-4 py-3 text-[12px] text-[#9aa0aa]">
                          No channels attached to this grant.
                        </p>
                      ) : (
                        manager.channels.map((channel) => (
                          <div
                            key={channel.platform_account_id}
                            className="flex items-center gap-3 px-4 py-2.5"
                          >
                            <div className="min-w-0 flex-1">
                              <p className="text-[13px] text-white font-medium truncate">
                                {channel.username || channel.platform_user_id}
                              </p>
                              <p className="text-[11px] text-[#9aa0aa] truncate font-mono">
                                {channel.platform_user_id}
                              </p>
                            </div>
                            <span
                              className={cn(
                                "px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase tracking-wider border shrink-0",
                                statusChip(channel.status),
                              )}
                            >
                              {channel.status}
                            </span>
                            <span className="hidden sm:inline-flex px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase tracking-wider border bg-white/[0.06] text-[#9aa0aa] border-white/[0.1] font-mono shrink-0">
                              {channel.oauth_client_key}
                            </span>
                          </div>
                        ))
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Section>
    </div>
  );
}
