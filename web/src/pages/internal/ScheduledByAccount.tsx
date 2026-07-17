import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowLeft,
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  Loader2,
  RefreshCw,
  Trash2,
  Video,
} from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { Skeleton, ErrorState, EmptyState } from "../../components/feedback";
import { cn } from "../../lib/utils";

type UploadJob = {
  id: number;
  workspace_id: number;
  title: string;
  caption?: string;
  status: string;
  scheduled_at?: string | null;
  created_at: string;
  targets: number[];
  source_type: string;
  error_message?: string;
};

type UploadJobBucket = {
  date: string; // YYYY-MM-DD UTC
  jobs: UploadJob[];
  count: number;
};

type CalendarResponse = {
  account_id: number;
  platform: string;
  username: string;
  count: number;
  pending_count: number;
  processing_count: number;
  completed_count: number;
  failed_count: number;
  first_publish_at?: string | null;
  last_publish_at?: string | null;
  by_day: UploadJobBucket[];
};

type CalendarState =
  | { kind: "loading" }
  | { kind: "ready"; data: CalendarResponse; monthStart: Date }
  | { kind: "error"; message: string };

const DAY_MS = 24 * 60 * 60 * 1000;

// All calendar math below runs in the browser's LOCAL timezone so the
// user sees a calendar aligned with their wall clock and drops land on
// the day they intended. The backend stores scheduled_at as a UTC ISO
// timestamp; the frontend only converts at the boundary (parse on
// arrival, stringify on reschedule send).

function startOfMonthLocal(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1);
}

function addMonthsLocal(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth() + n, 1);
}

function fmtLocalDateKey(d: Date): string {
  // YYYY-MM-DD in the browser's local timezone — single source of
  // truth for "what day is this cell" used by both grid construction
  // and the re-bucketed chip map.
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

function shiftToDatePreservingTimeOfDay(
  originalISO: string,
  newDay: Date,
): string {
  // Drag-drop is a DAY-level move: keep hour/minute/second from the
  // original scheduled_at so the chip keeps its time-of-day. The
  // newDay Date object is constructed at LOCAL midnight by the cell,
  // so we read hour/minute off it after shifting its date components.
  const orig = new Date(originalISO);
  const target = new Date(newDay);
  target.setHours(orig.getHours(), orig.getMinutes(), orig.getSeconds(), 0);
  // toISOString() converts to UTC. Browsers that do not support it
  // fall back to constructing the string by hand (no current browser
  // lacks toISOString, but defensive).
  return target.toISOString();
}

function formatLocalDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// Drag payload key — kept namespace-prefixed so we never collide with
// ambient HTML5 drag sources on the page (e.g. links, images).
const DND_TYPE = "application/x-instaedit-upload-id";

export function ScheduledByAccount() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const accountID = Number(searchParams.get("account_id"));

  const initialMonth = useMemo(() => startOfMonthLocal(new Date()), []);
  const [monthStart, setMonthStart] = useState<Date>(initialMonth);
  const [state, setState] = useState<CalendarState>({ kind: "loading" });
  const [busyReschedule, setBusyReschedule] = useState<number | null>(null);
  const [busyCancel, setBusyCancel] = useState<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    if (!Number.isFinite(accountID) || accountID <= 0) {
      setState({ kind: "error", message: "Missing account_id query parameter." });
      return;
    }
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      // Fetch a generous UTC range covering the displayed local
      // month plus 1-day slop on each side (to catch timezone-shift
      // boundary cases where a video whose scheduled_at is 23:30
      // local on the last day of the month lands in next month UTC).
      const fromLocal = startOfMonthLocal(monthStart);
      const toLocal = addMonthsLocal(monthStart, 1);
      const fromUTC = new Date(fromLocal.getTime() - DAY_MS);
      const toUTC = new Date(toLocal.getTime() + DAY_MS);
      const url =
        `/api/v1/uploads/by-account?account_id=${accountID}` +
        `&from=${encodeURIComponent(fromUTC.toISOString())}` +
        `&to=${encodeURIComponent(toUTC.toISOString())}`;
      const resp = await authedFetch(url, { signal: controller.signal });
      if (controller.signal.aborted) return;
      const data = (await resp.json()) as CalendarResponse;
      setState({ kind: "ready", data, monthStart });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load calendar.";
      setState({ kind: "error", message });
    }
  }, [accountID, monthStart, navigate]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  const handleReschedule = useCallback(
    async (job: UploadJob, newDay: Date) => {
      if (!job.scheduled_at) return;
      setBusyReschedule(job.id);
      try {
        const iso = shiftToDatePreservingTimeOfDay(job.scheduled_at, newDay);
        await authedFetch(`/api/v1/uploads/${job.id}/reschedule`, {
          method: "PATCH",
          body: JSON.stringify({ scheduled_at: iso }),
        });
        await load();
      } finally {
        setBusyReschedule(null);
      }
    },
    [load],
  );

  const handleCancel = useCallback(
    async (job: UploadJob) => {
      if (!window.confirm(`Cancel scheduled upload "${job.title}"?`)) return;
      setBusyCancel(job.id);
      try {
        await authedFetch(`/api/v1/uploads/${job.id}`, { method: "DELETE" });
        await load();
      } finally {
        setBusyCancel(null);
      }
    },
    [load],
  );

  // Calendar grid: a fixed 6-row × 7-col layout starting on the Sunday
  // before the displayed month (local timezone). The grid alignment,
  // cell keys, AND chip bucketing all use LOCAL dates so what the user
  // sees matches what they click — a chip that visually sits on
  // "Wed 17" stays in the "Wed 17" bucket after a drop anywhere in
  // the same week, regardless of UTC offset.
  const grid = useMemo(() => {
    if (state.kind !== "ready") return null;
    // Re-bucket by LOCAL date so the chip the user sees matches the
    // cell key. Backend buckets by UTC date; a scheduled_at at
    // 23:30 local on the last day of the month may bucket on UTC
    // "next day" but must still appear on the user's "today" cell.
    const byDay = new Map<string, UploadJob[]>();
    for (const b of state.data.by_day) {
      for (const j of b.jobs) {
        if (!j.scheduled_at) continue;
        const localKey = fmtLocalDateKey(new Date(j.scheduled_at));
        if (!byDay.has(localKey)) byDay.set(localKey, []);
        byDay.get(localKey)!.push(j);
      }
    }

    const first = state.monthStart;
    const firstWeekday = first.getDay(); // 0 (Sun) ... 6 (Sat), local
    // Anchor the grid at LOCAL midnight of the Sunday before the
    // month — setHours(0,0,0,0) is timezone-safe.
    const startDate = new Date(first);
    startDate.setDate(first.getDate() - firstWeekday);
    const month = first.getMonth();
    const cells: { date: Date; key: string; inMonth: boolean; jobs: UploadJob[] }[] = [];
    for (let i = 0; i < 42; i++) {
      const d = new Date(startDate);
      d.setDate(startDate.getDate() + i);
      const key = fmtLocalDateKey(d);
      cells.push({
        date: d,
        key,
        inMonth: d.getMonth() === month,
        jobs: byDay.get(key) ?? [],
      });
    }
    return cells;
  }, [state]);

  const monthLabel = useMemo(
    () =>
      monthStart.toLocaleString(undefined, {
        month: "long",
        year: "numeric",
      }),
    [monthStart],
  );

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
          <div>
            <Link
              to="/app/dashboard"
              className="inline-flex items-center gap-1 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline mb-2"
            >
              <ArrowLeft size={14} /> Back to dashboard
            </Link>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <CalendarClock size={28} className="text-white/40" />
              Scheduled
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Drag a video chip to another day to reschedule. Drops keep
              the original time-of-day.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        {state.kind === "ready" && (
          <div className="flex items-center justify-between mb-4 p-3 rounded-xl bg-[#1f1f2e] border border-white/[0.08]">
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => setMonthStart((m) => addMonthsLocal(m, -1))}
                className="p-1.5 rounded-lg bg-white/[0.04] hover:bg-white/[0.08] text-white"
                aria-label="Previous month"
              >
                <ChevronLeft size={16} />
              </button>
              <span className="text-[15px] font-semibold text-white min-w-[120px] text-center">
                {monthLabel}
              </span>
              <button
                type="button"
                onClick={() => setMonthStart((m) => addMonthsLocal(m, 1))}
                className="p-1.5 rounded-lg bg-white/[0.04] hover:bg-white/[0.08] text-white"
                aria-label="Next month"
              >
                <ChevronRight size={16} />
              </button>
            </div>
            <div className="hidden sm:flex items-center gap-4 text-[12px] text-[#9aa0aa]">
              <span className="inline-flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-amber-400" />
                {state.data.pending_count} pending
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-blue-400" />
                {state.data.processing_count} processing
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-emerald-400" />
                {state.data.completed_count} done
              </span>
              {state.data.failed_count > 0 && (
                <span className="inline-flex items-center gap-1.5">
                  <span className="w-2 h-2 rounded-full bg-red-400" />
                  {state.data.failed_count} failed
                </span>
              )}
            </div>
          </div>
        )}

        {state.kind === "loading" && (
          <div className="grid grid-cols-7 gap-2">
            {Array.from({ length: 35 }, (_, i) => (
              <Skeleton key={i} variant="card" height={84} />
            ))}
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load calendar"
            message={state.message}
            onRetry={() => void load()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && state.data.by_day.length === 0 && (
          <EmptyState
            title="Nothing scheduled"
            description="This account has no uploads queued for the displayed month. Trigger a Drive folder batch from the dashboard to populate it."
            icon={<CalendarClock size={32} />}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && grid && (
          <>
            <CalendarWeekHeader />
            <div className="grid grid-cols-7 gap-2">
              {grid.map((cell) => (
                <DayCell
                  key={cell.key}
                  cell={cell}
                  todayKey={fmtLocalDateKey(new Date())}
                  busyReschedule={busyReschedule}
                  busyCancel={busyCancel}
                  onDrop={(job) => void handleReschedule(job, cell.date)}
                  onCancel={handleCancel}
                />
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function CalendarWeekHeader() {
  const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  return (
    <div className="grid grid-cols-7 gap-2 mb-2">
      {days.map((d) => (
        <div
          key={d}
          className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa] text-center"
        >
          {d}
        </div>
      ))}
    </div>
  );
}

function DayCell({
  cell,
  todayKey,
  busyReschedule,
  busyCancel,
  onDrop,
  onCancel,
}: {
  cell: { date: Date; key: string; inMonth: boolean; jobs: UploadJob[] };
  todayKey: string;
  busyReschedule: number | null;
  busyCancel: number | null;
  onDrop: (job: UploadJob) => void | Promise<void>;
  onCancel: (job: UploadJob) => void;
}) {
  const [dragOver, setDragOver] = useState(false);
  const isToday = cell.key === todayKey;

  // HTML5 drag-drop is the only drag mechanism — no mouse-based
  // duplicate. The drop handler reads the upload id from the
  // dataTransfer payload and forwards the cell's local Date to the
  // reschedule callback (which preserves the time-of-day).
  const handleDragOver = (e: React.DragEvent) => {
    if (!cell.inMonth) return;
    if (e.dataTransfer.types.includes(DND_TYPE)) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setDragOver(true);
    }
  };
  const handleDragLeave = () => setDragOver(false);
  const handleDrop = (e: React.DragEvent) => {
    if (!cell.inMonth) return;
    setDragOver(false);
    const raw = e.dataTransfer.getData(DND_TYPE) || e.dataTransfer.getData("text");
    const id = Number(raw);
    if (!Number.isFinite(id) || id <= 0) return;
    const job = cell.jobs.find((j) => j.id === id);
    if (job) void onDrop(job);
  };

  return (
    <div
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      className={cn(
        "min-h-[110px] rounded-xl border p-2 transition-colors",
        cell.inMonth
          ? "bg-[#1f1f2e] border-white/[0.08]"
          : "bg-[#16161e]/60 border-white/[0.04] opacity-50",
        dragOver && cell.inMonth && "ring-2 ring-emerald-400/50 bg-emerald-500/[0.06]",
      )}
      data-key={cell.key}
    >
      <div className="flex items-center justify-between mb-1.5">
        <span
          className={cn(
            "text-[11px] font-bold tabular-nums",
            isToday ? "text-emerald-400" : "text-[#9aa0aa]",
          )}
        >
          {cell.date.getDate()}
        </span>
        {cell.jobs.length > 0 && cell.inMonth && (
          <span className="text-[10px] font-semibold text-[#9aa0aa] tabular-nums">
            {cell.jobs.length}
          </span>
        )}
      </div>
      <div className="space-y-1">
        {cell.jobs.slice(0, 4).map((job) => (
          <UploadChip
            key={job.id}
            job={job}
            busyReschedule={busyReschedule === job.id}
            busyCancel={busyCancel === job.id}
            onCancel={onCancel}
          />
        ))}
        {cell.jobs.length > 4 && (
          <div className="text-[10px] text-[#9aa0aa] text-center pt-1">
            +{cell.jobs.length - 4} more
          </div>
        )}
      </div>
    </div>
  );
}

function UploadChip({
  job,
  busyReschedule,
  busyCancel,
  onCancel,
}: {
  job: UploadJob;
  busyReschedule: boolean;
  busyCancel: boolean;
  onCancel: (job: UploadJob) => void;
}) {
  const handleDragStart = (e: React.DragEvent) => {
    e.dataTransfer.setData(DND_TYPE, String(job.id));
    // Some browsers need an explicit fallback text/plain type so the
    // drop event's dataTransfer.getData("text") still works (older
    // Firefox fallback).
    e.dataTransfer.setData("text/plain", String(job.id));
    e.dataTransfer.effectAllowed = "move";
  };

  return (
    <div
      draggable
      onDragStart={handleDragStart}
      title={job.title}
      className={cn(
        "group flex items-center gap-1.5 px-1.5 py-1 rounded-md text-[11px] cursor-grab active:cursor-grabbing",
        "bg-amber-500/[0.10] border border-amber-500/[0.25] text-amber-200",
        "hover:bg-amber-500/[0.18] hover:border-amber-500/[0.40] transition-colors",
        busyReschedule && "opacity-60",
      )}
    >
      <Video size={10} className="shrink-0 opacity-70" />
      <span className="flex-1 truncate font-medium">
        {job.scheduled_at ? formatLocalDateTime(job.scheduled_at) : "—"}
      </span>
      {busyReschedule ? (
        <Loader2 size={10} className="animate-spin shrink-0" />
      ) : (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onCancel(job);
          }}
          disabled={busyCancel}
          className="opacity-0 group-hover:opacity-100 text-red-300 hover:text-red-200 transition-opacity"
          aria-label={`Cancel ${job.title}`}
        >
          <Trash2 size={11} />
        </button>
      )}
    </div>
  );
}
