import { AlertTriangle, BarChart3, CheckCircle2, Clock, Server, XCircle } from "lucide-react";
import { Card, formatDate } from "./AdminDashboardKpis";
import type { FleetReadinessData } from "./adminDashboardTypes";

// Fleet Readiness KPI grid (extracted from AdminDashboard.tsx, pure file move).

export function FleetReadinessSection({ data }: { data: FleetReadinessData }) {
  return (
    <div className="mb-8">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-[18px] font-bold text-white flex items-center gap-2">
          <BarChart3 size={20} className="text-[#9aa0aa]" />
          Fleet Readiness
        </h2>
        <div className="text-[12px] text-[#9aa0aa]">
          Snapshot {data.snapshot_id.slice(-12)} at{" "}
          {formatDate(
            Math.floor(new Date(data.taken_at).getTime() / 1000),
          )}
        </div>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <Card title="Total YouTube channels" value={data.fleet_readiness.youtube_channels_total} icon={Server} />
        <Card title="Active" value={data.fleet_readiness.active} icon={CheckCircle2} variant="success" />
        <Card title="Reauth required" value={data.fleet_readiness.reauth_required} icon={AlertTriangle} variant="warning" />
        <Card title="Error" value={data.fleet_readiness.error} icon={XCircle} variant="danger" />
        <Card title="Pending authorization" value={data.fleet_readiness.pending_authorization} icon={Clock} />
        <Card title="Revoked" value={data.fleet_readiness.revoked} icon={XCircle} />
        <Card title="Refresh test OK" value={data.fleet_readiness.refresh_test_ok} icon={CheckCircle2} variant="success" />
        <Card title="Channel binding OK" value={data.fleet_readiness.channel_binding_ok} icon={CheckCircle2} variant="success" />
        <Card title="Upload scope OK" value={data.fleet_readiness.scope_youtube_upload_ok} icon={CheckCircle2} variant="success" />
        <Card title="Readonly scope OK" value={data.fleet_readiness.scope_youtube_readonly_ok} icon={CheckCircle2} variant="success" />
        <Card title="Private canary OK" value={data.fleet_readiness.private_canary_ok} icon={CheckCircle2} variant="success" />
        <Card title="Canary channel match OK" value={data.fleet_readiness.canary_channel_match_ok} icon={CheckCircle2} variant="success" />
      </div>
    </div>
  );
}

