import type React from "react";
import { formatDateTime } from "../../lib/formatters";

export function ScheduleBlock({
  label,
  icon,
  date,
  empty,
  statValue,
}: {
  label: string;
  icon: React.ReactNode;
  date?: Date | null;
  empty?: string;
  statValue?: number;
}) {
  return (
    <div className="p-3 rounded-xl bg-white/[0.04] border border-white/[0.08]">
      <dt className="text-[11px] font-semibold text-[#9aa0aa] uppercase tracking-wider flex items-center gap-1">
        {icon} {label}
      </dt>
      <dd className="mt-1 text-[14px] text-white font-medium">
        {statValue !== undefined ? (
          <span className="tabular-nums">{statValue}</span>
        ) : date ? (
          formatDateTime(date)
        ) : (
          empty
        )}
      </dd>
    </div>
  );
}
