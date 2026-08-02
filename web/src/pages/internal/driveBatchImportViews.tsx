import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Clock,
  ExternalLink,
} from "lucide-react";
import { ErrorState } from "../../components/feedback";
import { type SuccessPayload } from "./driveBatchImportTypes";
import { formatRelHours } from "./driveBatchImportFormat";
import { ScheduleBlock } from "./driveBatchImportPrimitives";

export function SuccessView({
  payload,
  hasNextPage,
  onContinue,
  onViewPosts,
}: {
  payload: SuccessPayload;
  hasNextPage: boolean;
  onContinue: () => void;
  onViewPosts: () => void;
}) {
  const firstDate = payload.firstPublishAt
    ? new Date(payload.firstPublishAt)
    : null;
  const lastDate = payload.lastScheduledAt
    ? new Date(payload.lastScheduledAt)
    : null;
  const preview = payload.entries.slice(0, 3);

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-start gap-3" data-testid="drive-batch-success">
        <div className="w-10 h-10 rounded-full bg-emerald-500/[0.12] border border-emerald-500/[0.30] flex items-center justify-center text-emerald-400 shrink-0">
          <CheckCircle2 size={20} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[15px] font-bold text-white">
            {payload.scheduledCount > 0
              ? `${payload.scheduledCount} video${payload.scheduledCount === 1 ? "" : "s"} scheduled`
              : "Folder imported (no publishable videos)"}
          </p>
          {payload.cursorClampedToNow && (
            <p className="text-[12px] text-amber-400 mt-1 inline-flex items-center gap-1">
              <AlertTriangle size={12} aria-hidden="true" />
              Cursor was too far in the past — restart anchor clamped to now.
            </p>
          )}
        </div>
      </div>

      {payload.scheduledCount > 0 && (
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <ScheduleBlock
            label="First publish"
            icon={<Clock size={14} />}
            date={firstDate}
            empty="immediately"
          />
          <ScheduleBlock
            label="Last publish"
            icon={<Clock size={14} />}
            date={lastDate}
            empty="—"
          />
        </dl>
      )}

      {preview.length > 0 && (
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
                  <span className="text-[#9aa0aa] font-mono text-[11px] mr-2">
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
          {payload.entries.length > preview.length && (
            <p className="mt-2 text-[12px] text-[#9aa0aa]/80">
              +{payload.entries.length - preview.length} more in /app/posts.
            </p>
          )}
        </div>
      )}

      <div className="flex items-center justify-between gap-3 pt-2">
        {hasNextPage ? (
          <p className="text-[12px] text-[#9aa0aa] flex items-center gap-1">
            <AlertTriangle size={12} className="text-amber-400" aria-hidden="true" />
            More pages remain in this folder.
          </p>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-2">
          {hasNextPage && (
            <button
              type="button"
              onClick={onContinue}
              data-testid="drive-batch-continue"
              className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
            >
              Continue next page <ArrowRight size={14} aria-hidden="true" />
            </button>
          )}
          <button
            type="button"
            onClick={onViewPosts}
            data-testid="drive-batch-view-posts"
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
          >
            View posts <ExternalLink size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}

export function GuidanceView({
  note,
  onBack,
}: {
  note: string;
  onBack: () => void;
}) {
  return (
    <div className="p-6 space-y-4" data-testid="drive-batch-guidance">
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-amber-500/[0.12] border border-amber-500/[0.30] flex items-center justify-center text-amber-400 shrink-0">
          <AlertTriangle size={20} aria-hidden="true" />
        </div>
        <div>
          <p className="text-[15px] font-bold text-white">
            Server needs configuration
          </p>
          <p className="text-[13px] text-[#9aa0aa] mt-1 leading-relaxed">
            {note}
          </p>
        </div>
      </div>
      <div className="flex items-center justify-end gap-2 pt-2">
        <button
          type="button"
          onClick={onBack}
          data-testid="drive-batch-back"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
        >
          Back to form
        </button>
      </div>
    </div>
  );
}

export function ErrorView({
  message,
  onBack,
}: {
  message: string;
  onBack: () => void;
}) {
  return (
    <div className="p-6 space-y-4" data-testid="drive-batch-error">
      <ErrorState title="Import failed" message={message} />
      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
        >
          Back to form
        </button>
      </div>
    </div>
  );
}
