import type { ElementType } from "react";
import { Users, Eye, Video } from "lucide-react";
import { formatNumber } from "./ChannelsPerformanceChart";
import type { Aggregates } from "./channelsPerformanceTypes";

function KPICard({
  label,
  value,
  icon: Icon,
}: {
  label: string;
  value: number;
  icon: ElementType;
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


export function ChannelsPerformanceKpis({ aggregates }: { aggregates: Aggregates }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
      <KPICard label="Channels" value={aggregates.channels} icon={Video} />
      <KPICard label="Total subscribers" value={aggregates.subscribers} icon={Users} />
      <KPICard label="Total views" value={aggregates.views} icon={Eye} />
      <KPICard label="Total videos" value={aggregates.videos} icon={Video} />
    </div>
  );
}
