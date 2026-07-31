import { useEffect, type Dispatch, type FormEvent, type ReactNode, type RefObject, type SetStateAction } from "react";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  Clock,
  ExternalLink,
  Loader2,
  Sparkles,
} from "lucide-react";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import {
  MAX_JITTER_MIN,
  MIN_JITTER_MIN,
  type FormValues,
  type PlatformAccount,
  type SuccessPayload,
  type Workspace,
} from "./driveBatchImportTypes";

export function ImportForm({
  form,
  setForm,
  workspaces,
  pages,
  folderValid,
  jitterError,
  isSubmitting,
  firstFieldRef,
  onSubmit,
}: {
  form: FormValues;
  setForm: Dispatch<SetStateAction<FormValues>>;
  workspaces: Workspace[];
  pages: PlatformAccount[];
  folderValid: boolean | null;
  jitterError: string | null;
  isSubmitting: boolean;
  firstFieldRef: RefObject<HTMLInputElement | null>;
  onSubmit: (e: FormEvent) => void;
}) {
  const canSubmit =
    form.workspaceId !== "" &&
    form.facebookAccountId !== "" &&
    folderValid === true &&
    jitterError === null &&
    !isSubmitting;

  // Focus the folder ID input on mount so keyboard users land on a labeled
  // field the moment the form becomes visible (workspaces + pages fetch
  // already resolved). Runs once per ImportForm mount.
  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  return (
    <form onSubmit={onSubmit} className="p-6 space-y-5">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <FormSelect
          id="drive-batch-workspace"
          label="Workspace"
          value={form.workspaceId}
          onChange={(v) =>
            setForm((f) => ({ ...f, workspaceId: v as number | "" }))
          }
          placeholder="Select a workspace…"
          disabled={isSubmitting}
          options={workspaces.map((w) => ({ value: w.id, label: w.name }))}
        />
        <FormSelect
          id="drive-batch-page"
          label="Facebook Page"
          value={form.facebookAccountId}
          onChange={(v) =>
            setForm((f) => ({ ...f, facebookAccountId: v as number | "" }))
          }
          placeholder="Select a Page…"
          disabled={isSubmitting}
          options={pages.map((p) => ({
            value: p.id,
            label: `@${p.username}`,
          }))}
        />
      </div>

      <div>
        <FormField
          id="drive-batch-folder"
          label="Google Drive Folder ID or link"
          helpText="Paste the part after /folders/ in any Google Drive URL, e.g. 1HregS58okcSoe8597qdXgpZM6K4CwEBD."
          error={
            folderValid === false
              ? "Folder ID must be 1–100 letters, digits, hyphens, or underscores."
              : null
          }
        >
          <input
            ref={firstFieldRef}
            id="drive-batch-folder"
            type="text"
            placeholder="1HregS58okcSoe8597qdXgpZM6K4CwEBD"
            value={form.folderId}
            disabled={isSubmitting}
            onChange={(e) =>
              setForm((f) => ({ ...f, folderId: e.target.value }))
            }
            className={cn(
              "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:ring-1 focus:ring-white/10 transition-all",
              folderValid === false
                ? "border-red-500/40 focus:border-red-500/60"
                : "border-white/[0.08] focus:border-white/[0.20]",
            )}
            spellCheck={false}
            autoComplete="off"
          />
        </FormField>
      </div>

      <button
        type="button"
        onClick={() => setForm((f) => ({ ...f, advanced: !f.advanced }))}
        className="inline-flex items-center gap-1.5 text-[12px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
        aria-expanded={form.advanced}
        aria-controls="drive-batch-advanced-panel"
        data-testid="drive-batch-advanced-toggle"
      >
        <ChevronDown
          size={14}
          className={cn(
            "transition-transform",
            form.advanced && "rotate-180",
          )}
        />
        {form.advanced ? "Hide advanced options" : "Show advanced options"}
      </button>

      {form.advanced && (
        <div
          id="drive-batch-advanced-panel"
          className="space-y-4 pl-1 border-l border-white/[0.08] ml-1"
        >
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <FormField
                id="drive-batch-min-jitter"
                label="Minimum gap (minutes)"
                helpText="Random lower bound between posts."
              >
                <input
                  id="drive-batch-min-jitter"
                  type="number"
                  min={MIN_JITTER_MIN}
                  max={MAX_JITTER_MIN}
                  step={15}
                  value={form.minJitterMinutes}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      minJitterMinutes: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
            <div>
              <FormField
                id="drive-batch-max-jitter"
                label="Maximum gap (minutes)"
                helpText="Must be ≥ minimum. 270 = 4.5 hours."
                error={jitterError}
              >
                <input
                  id="drive-batch-max-jitter"
                  type="number"
                  min={MIN_JITTER_MIN}
                  max={MAX_JITTER_MIN}
                  step={15}
                  value={form.maxJitterMinutes}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      maxJitterMinutes: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
          </div>

          <div>
            <FormField
              id="drive-batch-title"
              label="Internal title prefix (optional)"
              helpText="Prepended to each post's internal title so you can recognise the batch in /app/posts."
            >
              <input
                id="drive-batch-title"
                type="text"
                placeholder="Vacation videos"
                disabled={isSubmitting}
                value={form.title}
                onChange={(e) =>
                  setForm((f) => ({ ...f, title: e.target.value }))
                }
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
              />
            </FormField>
          </div>

          <div>
            <FormField
              id="drive-batch-caption"
              label="Caption prefix (optional)"
              helpText="Prepended to each post's caption when published to Facebook."
            >
              <textarea
                id="drive-batch-caption"
                rows={2}
                placeholder="New video from my Drive folder — "
                disabled={isSubmitting}
                value={form.captionPrefix}
                onChange={(e) =>
                  setForm((f) => ({ ...f, captionPrefix: e.target.value }))
                }
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all resize-y"
              />
            </FormField>
          </div>
        </div>
      )}

      <div className="flex items-center justify-end gap-3 pt-2">
        <button
          type="submit"
          disabled={!canSubmit}
          data-testid="drive-batch-submit"
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isSubmitting ? (
            <Loader2 size={16} className="animate-spin" aria-hidden="true" />
          ) : (
            <Sparkles size={16} aria-hidden="true" />
          )}
          {isSubmitting ? "Scheduling…" : "Schedule the folder"}
        </button>
      </div>
    </form>
  );
}

export function FormField({
  id,
  label,
  helpText,
  error,
  children,
}: {
  id: string;
  label: string;
  helpText?: string;
  error?: string | null;
  children: ReactNode;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      {children}
      {helpText && !error && (
        <p className="mt-1.5 text-[12px] text-[#9aa0aa]/80">{helpText}</p>
      )}
      {error && (
        <p className="mt-1.5 text-[12px] text-red-400" role="status">
          {error}
        </p>
      )}
    </div>
  );
}

export function FormSelect({
  id,
  label,
  value,
  onChange,
  placeholder,
  disabled,
  options,
}: {
  id: string;
  label: string;
  value: number | "";
  onChange: (v: number | "") => void;
  placeholder: string;
  disabled?: boolean;
  options: Array<{ value: number; label: string }>;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      <select
        id={id}
        value={value === "" ? "" : String(value)}
        disabled={disabled}
        onChange={(e) =>
          onChange(e.target.value === "" ? "" : Number(e.target.value))
        }
        className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
      >
        <option value="" disabled className="bg-[#1f1f2e]">
          {placeholder}
        </option>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value} className="bg-[#1f1f2e]">
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
}

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

export function ScheduleBlock({
  label,
  icon,
  date,
  empty,
}: {
  label: string;
  icon: ReactNode;
  date: Date | null;
  empty: string;
}) {
  return (
    <div className="p-3 rounded-xl bg-white/[0.04] border border-white/[0.08]">
      <dt className="text-[11px] font-semibold text-[#9aa0aa] uppercase tracking-wider flex items-center gap-1">
        {icon} {label}
      </dt>
      <dd className="mt-1 text-[14px] text-white font-medium">
        {date ? formatDateTime(date) : empty}
      </dd>
    </div>
  );
}

export function formatDateTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return "—";
  const diffMs = date.getTime() - Date.now();
  const absMinutes = Math.round(Math.abs(diffMs) / 60_000);
  // Decide the main unit string first, then attach a direction-aware
  // prefix. Promoting the <1 min case to a single "just now" prevents
  // the `in just now` / `just now ago` artifacts when the slug falls
  // below the rounding threshold in either direction.
  let rel: string;
  if (absMinutes < 1) rel = "just now";
  else if (absMinutes < 60) rel = `${absMinutes} min`;
  else if (absMinutes < 24 * 60) rel = `${Math.round(absMinutes / 60)} h`;
  else rel = `${Math.round(absMinutes / (60 * 24))} d`;
  const relText =
    absMinutes < 1
      ? "just now"
      : diffMs >= 0
        ? `in ${rel}`
        : `${rel} ago`;
  const absolute = date.toLocaleString();
  return `${relText} · ${absolute}`;
}

export function formatRelHours(hours: number): string {
  const sign = hours < 0 ? "-" : "+";
  const abs = Math.abs(hours);
  if (abs < 1) {
    const minutes = Math.round(abs * 60);
    return `${sign}${minutes}m`;
  }
  if (abs < 24) {
    return `${sign}${abs.toFixed(1)}h`;
  }
  return `${sign}${(abs / 24).toFixed(1)}d`;
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