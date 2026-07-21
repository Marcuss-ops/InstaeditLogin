import { formatRelHours } from "../../lib/formatters";
import type { BatchResponse } from "../../types/uploads";

export function UploadsTable({
  entries,
  previewLimit = 5,
}: {
  entries: NonNullable<BatchResponse["entries"]>;
  previewLimit?: number;
}) {
  const preview = entries.slice(0, previewLimit);

  if (entries.length === 0) {
    return (
      <p className="text-[13px] text-[#9aa0aa]">
        No entries scheduled.
      </p>
    );
  }

  return (
    <div>
      <p className="text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider mb-2">
        First scheduled
      </p>
      <ul className="space-y-1.5">
        {preview.map((e) => (
          <li
            key={e.job_id}
            className="flex items-center justify-between gap-3 p-2.5 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px]"
          >
            <span className="text-white truncate min-w-0">
              <span className="text-[#9aa0aa] font-mono text-[11px] mr-2 tabular-nums">
                {formatRelHours(e.relative_hours_from_now)}
              </span>
              {e.name}
            </span>
            <time className="text-[11px] text-[#9aa0aa] tabular-nums whitespace-nowrap">
              {new Date(e.scheduled_at).toLocaleString()}
            </time>
          </li>
        ))}
      </ul>
      {entries.length > preview.length && (
        <p className="mt-2 text-[12px] text-[#9aa0aa]/80">
          +{entries.length - preview.length} more — open the calendar to see them
          all.
        </p>
      )}
    </div>
  );
}
