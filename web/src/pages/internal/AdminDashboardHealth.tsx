import { Activity, AlertTriangle, CheckCircle2 } from "lucide-react";
import { Card, Section, formatDate, formatNumber, formatPercent } from "./AdminDashboardKpis";
import { cn } from "../../lib/utils";
import type { AdminErrorRate, AdminHealth } from "./adminDashboardTypes";

// Channel-performance sections: YouTube Quota cards, Queue Counts and
// error-rate tables (extracted from AdminDashboard.tsx, pure file move).

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

export function AdminHealthSection({ data }: { data: AdminHealth }) {
  return (
    <>
      <div className="mb-8">
        <h2 className="text-[18px] font-bold text-white mb-4 flex items-center gap-2">
          <Activity size={20} className="text-[#9aa0aa]" />
          YouTube Quota
        </h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          <Card
            title="Estimated units"
            value={formatNumber(data.youtube_quota_estimate.estimated_units)}
            subtitle={`Last ${data.youtube_quota_estimate.window_hours}h`}
            icon={Activity}
          />
          <Card
            title="Remaining estimate"
            value={formatNumber(data.youtube_quota_estimate.remaining_estimate)}
            icon={Activity}
            variant="success"
          />
          <Card
            title="Upload successes"
            value={formatNumber(data.youtube_quota_estimate.success_count)}
            icon={CheckCircle2}
            variant="success"
          />
          <Card
            title="Quota failures"
            value={formatNumber(data.youtube_quota_estimate.quota_failures)}
            icon={AlertTriangle}
            variant={data.youtube_quota_estimate.quota_failures > 0 ? "danger" : "default"}
          />
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
        <Section title="Queue Counts">
          <div className="grid grid-cols-2 gap-3">
            {[
              { label: "Pending", value: data.queue_counts.pending_count },
              { label: "Leased", value: data.queue_counts.leased_count },
              { label: "Processing", value: data.queue_counts.processing_count },
              { label: "Ingest completed", value: data.queue_counts.ingest_completed },
              { label: "Publish completed", value: data.queue_counts.publish_completed },
              { label: "Failed", value: data.queue_counts.failed_count },
              { label: "Dead letter", value: data.queue_counts.dead_letter_count },
              { label: "Cancelled", value: data.queue_counts.cancelled_count },
              { label: "Retry wait", value: data.queue_counts.retry_wait_count },
              { label: "Total", value: data.queue_counts.total },
              { label: "Stuck", value: data.queue_counts.stuck_count },
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
            <ErrorRateTable rows={data.error_rate_1h} />
          </Section>
          <Section title="Error Rate (24h)">
            <ErrorRateTable rows={data.error_rate_24h} />
          </Section>
        </div>
      </div>

      <div className="text-[12px] text-[#9aa0aa]">
        Generated at {formatDate(data.generated_at_unix)}.
      </div>
    </>
  );
}
