import { Link } from "react-router-dom";
import {
  Clock,
  LayoutDashboard,
  Link2,
} from "lucide-react";
import { Skeleton, ErrorState } from "../../components/feedback";
import { useDashboardData } from "./useDashboardData";

function StatCard({
  label,
  value,
  icon: Icon,
  to,
}: {
  label: string;
  value: number;
  icon: React.ElementType;
  to: string;
}) {
  return (
    <Link
      to={to}
      className="group surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5 hover:border-white/[0.24] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)] transition-all no-underline block"
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-white">{value}</p>
        </div>
        <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa] group-hover:bg-white group-hover:text-[#030308] transition-colors">
          <Icon size={20} />
        </div>
      </div>
    </Link>
  );
}

export function InternalDashboard() {
  const { state, refetch: load } = useDashboardData();

  const stats =
    state.kind === "ready"
      ? {
          connected: state.data.accounts.length,
          queuedUploads: state.data.pendingUploads,
        }
      : null;

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <LayoutDashboard size={28} className="text-white/40" />
              Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Overview of your connected accounts and publishing activity.
            </p>
          </div>
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-8">
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load dashboard"
            message={state.message}
            onRetry={() => void load()}
            className="mb-8 bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && stats && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <StatCard label="Connected accounts" value={stats.connected} icon={Link2} to="/app/linking" />
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
              <Link
                to={`/app/calendar?group_id=all`}
                className="group block no-underline"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">Upload in coda</p>
                    <p className="text-[28px] font-extrabold tracking-tight text-white">{stats.queuedUploads}</p>
                  </div>
                  <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa] group-hover:bg-white group-hover:text-[#030308] transition-colors">
                    <Clock size={20} />
                  </div>
                </div>
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
