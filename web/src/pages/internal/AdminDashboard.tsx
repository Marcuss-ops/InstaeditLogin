import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  BarChart3,
  CheckCircle2,
  Clock,
  RefreshCw,
  Server,
  Shield,
  XCircle,
} from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";

type FleetReadiness = {
  youtube_channels_total: number;
  active: number;
  pending_authorization: number;
  reauth_required: number;
  revoked: number;
  error: number;
  refresh_test_ok: number;
  scope_youtube_upload_ok: number;
  scope_youtube_readonly_ok: number;
  channel_binding_ok: number;
  private_canary_ok: number;
  canary_channel_match_ok: number;
};

type FleetReadinessData = {
  fleet_readiness: FleetReadiness;
  snapshot_id: string;
  taken_at: string;
};

type AdminErrorRate = {
  platform_account_id: number;
  platform: string;
  username: string;
  window_label: string;
  total_count: number;
  failed_count: number;
  error_rate: number;
};

type YouTubeQuota = {
  window_hours: number;
  estimated_units: number;
  success_count: number;
  quota_failures: number;
  daily_budget_units: number;
  remaining_estimate: number;
  cost_per_upload_units: number;
};

type QueueCounts = {
  pending_count: number;
  leased_count: number;
  processing_count: number;
  ingest_completed: number;
  publish_completed: number;
  failed_count: number;
  dead_letter_count: number;
  cancelled_count: number;
  retry_wait_count: number;
  total: number;
  stuck_count: number;
};

type AdminHealth = {
  youtube_quota_estimate: YouTubeQuota;
  error_rate_1h: AdminErrorRate[];
  error_rate_24h: AdminErrorRate[];
  queue_counts: QueueCounts;
  generated_at_unix: number;
};

type FetchState<T> =
  | { kind: "loading" }
  | { kind: "ready"; data: T }
  | { kind: "error"; message: string };

function formatNumber(value: number | string): string {
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return String(value);
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

function formatDate(value: number): string {
  return new Date(value * 1000).toLocaleString();
}

function Card({
  title,
  value,
  subtitle,
  icon: Icon,
  variant = "default",
}: {
  title: string;
  value: number | string;
  subtitle?: string;
  icon: React.ElementType;
  variant?: "default" | "success" | "warning" | "danger";
}) {
  const variantClasses = {
    default: "bg-[#1f1f2e] border-white/[0.12] text-white",
    success: "bg-emerald-500/[0.08] border-emerald-500/20 text-emerald-400",
    warning: "bg-amber-500/[0.08] border-amber-500/20 text-amber-400",
    danger: "bg-red-500/[0.08] border-red-500/20 text-red-400",
  };

  return (
    <div
      className={cn(
        "rounded-2xl p-5 border",
        variantClasses[variant],
      )}
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa]">{title}</p>
          <p className="text-[28px] font-extrabold tracking-tight mt-1">
            {value}
          </p>
          {subtitle && (
            <p className="text-[12px] text-[#9aa0aa] mt-1">{subtitle}</p>
          )}
        </div>
        <div
          className={cn(
            "w-10 h-10 rounded-xl flex items-center justify-center",
            variant === "default"
              ? "bg-white/[0.04] border border-white/[0.08] text-[#9aa0aa]"
              : "bg-white/[0.08]",
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

function ErrorRateTable({ rows }: { rows: AdminErrorRate[] }) {
  if (rows.length === 0) {
    return (
      <p className="text-[13px] text-[#9aa0aa]">
        No channels reported errors in this window.
      </p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-white/[0.08]">
            <th className="py-2 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">
              Channel
            </th>
            <th className="py-2 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">
              Total
            </th>
            <th className="py-2 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">
              Failed
            </th>
            <th className="py-2 pr-4 text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider text-right">
              Rate
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={`${row.platform_account_id}-${row.window_label}`}
              className="border-b border-white/[0.06] hover:bg-white/[0.03] transition-colors"
            >
              <td className="py-2 pr-4 text-[13px] text-white font-medium">
                @{row.username}
              </td>
              <td className="py-2 pr-4 text-[13px] text-white text-right tabular-nums">
                {formatNumber(row.total_count)}
              </td>
              <td className="py-2 pr-4 text-[13px] text-white text-right tabular-nums">
                {formatNumber(row.failed_count)}
              </td>
              <td
                className={cn(
                  "py-2 pr-4 text-[13px] text-right tabular-nums font-semibold",
                  row.error_rate > 0.2
                    ? "text-red-400"
                    : row.error_rate > 0.05
                      ? "text-amber-400"
                      : "text-emerald-400",
                )}
              >
                {formatPercent(row.error_rate)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function AdminDashboardPage() {
  const navigate = useNavigate();
  const [fleet, setFleet] = useState<FetchState<FleetReadinessData>>({
    kind: "loading",
  });
  const [health, setHealth] = useState<FetchState<AdminHealth>>({
    kind: "loading",
  });

  const loadFleet = useCallback(async () => {
    setFleet({ kind: "loading" });
    try {
      const response = await authedFetch("/admin/youtube/fleet_readiness");
      const data = (await response.json()) as FleetReadinessData;
      setFleet({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load fleet readiness.";
      setFleet({ kind: "error", message });
    }
  }, [navigate]);

  const loadHealth = useCallback(async () => {
    setHealth({ kind: "loading" });
    try {
      const response = await authedFetch("/admin/health");
      const data = (await response.json()) as AdminHealth;
      setHealth({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load health.";
      setHealth({ kind: "error", message });
    }
  }, [navigate]);

  const loadAll = useCallback(() => {
    void loadFleet();
    void loadHealth();
  }, [loadFleet, loadHealth]);

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  const isLoading =
    fleet.kind === "loading" || health.kind === "loading";

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Shield size={28} className="text-white/40" />
              Admin Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Fleet readiness and platform health.
            </p>
          </div>
          <button
            type="button"
            onClick={() => loadAll()}
            disabled={isLoading}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50"
          >
            <RefreshCw size={14} className={cn(isLoading && "animate-spin")} /> Refresh
          </button>
        </div>

        {fleet.kind === "error" && (
          <ErrorState
            title="Couldn't load fleet readiness"
            message={fleet.message}
            onRetry={() => loadFleet()}
            className="bg-[#1f1f2e] border-white/[0.12] mb-8"
          />
        )}

        {fleet.kind === "ready" && (
          <div className="mb-8">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[18px] font-bold text-white flex items-center gap-2">
                <BarChart3 size={20} className="text-[#9aa0aa]" />
                Fleet Readiness
              </h2>
              <div className="text-[12px] text-[#9aa0aa]">
                Snapshot {fleet.data.snapshot_id.slice(-12)} at{" "}
                {formatDate(
                  Math.floor(new Date(fleet.data.taken_at).getTime() / 1000),
                )}
              </div>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              <Card title="Total YouTube channels" value={fleet.data.fleet_readiness.youtube_channels_total} icon={Server} />
              <Card title="Active" value={fleet.data.fleet_readiness.active} icon={CheckCircle2} variant="success" />
              <Card title="Reauth required" value={fleet.data.fleet_readiness.reauth_required} icon={AlertTriangle} variant="warning" />
              <Card title="Error" value={fleet.data.fleet_readiness.error} icon={XCircle} variant="danger" />
              <Card title="Pending authorization" value={fleet.data.fleet_readiness.pending_authorization} icon={Clock} />
              <Card title="Revoked" value={fleet.data.fleet_readiness.revoked} icon={XCircle} />
              <Card title="Refresh test OK" value={fleet.data.fleet_readiness.refresh_test_ok} icon={CheckCircle2} variant="success" />
              <Card title="Channel binding OK" value={fleet.data.fleet_readiness.channel_binding_ok} icon={CheckCircle2} variant="success" />
              <Card title="Upload scope OK" value={fleet.data.fleet_readiness.scope_youtube_upload_ok} icon={CheckCircle2} variant="success" />
              <Card title="Readonly scope OK" value={fleet.data.fleet_readiness.scope_youtube_readonly_ok} icon={CheckCircle2} variant="success" />
              <Card title="Private canary OK" value={fleet.data.fleet_readiness.private_canary_ok} icon={CheckCircle2} variant="success" />
              <Card title="Canary channel match OK" value={fleet.data.fleet_readiness.canary_channel_match_ok} icon={CheckCircle2} variant="success" />
            </div>
          </div>
        )}

        {health.kind === "error" && (
          <ErrorState
            title="Couldn't load health"
            message={health.message}
            onRetry={() => loadHealth()}
            className="bg-[#1f1f2e] border-white/[0.12] mb-8"
          />
        )}

        {health.kind === "ready" && (
          <>
            <div className="mb-8">
              <h2 className="text-[18px] font-bold text-white mb-4 flex items-center gap-2">
                <Activity size={20} className="text-[#9aa0aa]" />
                YouTube Quota
              </h2>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card
                  title="Estimated units"
                  value={formatNumber(health.data.youtube_quota_estimate.estimated_units)}
                  subtitle={`Last ${health.data.youtube_quota_estimate.window_hours}h`}
                  icon={Activity}
                />
                <Card
                  title="Remaining estimate"
                  value={formatNumber(health.data.youtube_quota_estimate.remaining_estimate)}
                  icon={Activity}
                  variant="success"
                />
                <Card
                  title="Upload successes"
                  value={formatNumber(health.data.youtube_quota_estimate.success_count)}
                  icon={CheckCircle2}
                  variant="success"
                />
                <Card
                  title="Quota failures"
                  value={formatNumber(health.data.youtube_quota_estimate.quota_failures)}
                  icon={AlertTriangle}
                  variant={health.data.youtube_quota_estimate.quota_failures > 0 ? "danger" : "default"}
                />
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
              <Section title="Queue Counts">
                <div className="grid grid-cols-2 gap-3">
                  {[
                    { label: "Pending", value: health.data.queue_counts.pending_count },
                    { label: "Leased", value: health.data.queue_counts.leased_count },
                    { label: "Processing", value: health.data.queue_counts.processing_count },
                    { label: "Ingest completed", value: health.data.queue_counts.ingest_completed },
                    { label: "Publish completed", value: health.data.queue_counts.publish_completed },
                    { label: "Failed", value: health.data.queue_counts.failed_count },
                    { label: "Dead letter", value: health.data.queue_counts.dead_letter_count },
                    { label: "Cancelled", value: health.data.queue_counts.cancelled_count },
                    { label: "Retry wait", value: health.data.queue_counts.retry_wait_count },
                    { label: "Total", value: health.data.queue_counts.total },
                    { label: "Stuck", value: health.data.queue_counts.stuck_count },
                  ].map((item) => (
                    <div
                      key={item.label}
                      className="flex items-center justify-between py-2 px-3 rounded-xl bg-white/[0.03] border border-white/[0.06]"
                    >
                      <span className="text-[12px] text-[#9aa0aa]">{item.label}</span>
                      <span className="text-[13px] font-semibold text-white tabular-nums">
                        {formatNumber(item.value)}
                      </span>
                    </div>
                  ))}
                </div>
              </Section>

              <div className="lg:col-span-2 space-y-6">
                <Section title="Error Rate (1h)">
                  <ErrorRateTable rows={health.data.error_rate_1h} />
                </Section>
                <Section title="Error Rate (24h)">
                  <ErrorRateTable rows={health.data.error_rate_24h} />
                </Section>
              </div>
            </div>

            <div className="text-[12px] text-[#9aa0aa]">
              Generated at {formatDate(health.data.generated_at_unix)}.
            </div>
          </>
        )}
      </div>
    </div>
  );
}
