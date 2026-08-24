import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import timeGridPlugin from "@fullcalendar/timegrid";
import interactionPlugin from "@fullcalendar/interaction";
import type { EventDropArg, EventInput } from "@fullcalendar/core";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";
import { useNavigate } from "react-router-dom";
import { cn } from "../../lib/utils";
import { AlertCircle } from "lucide-react";
import type { Post } from "./calendarTypes";

type PostStatus = "draft" | "queued" | "publishing" | "published" | "failed";

type CalendarPost = Post & { status: PostStatus | string };

const STATUS_META: Record<string, { label: string; dot: string; bg: string; text: string; border: string }> = {
  draft: { label: "Draft", dot: "bg-[#9aa0aa]", bg: "bg-white/[0.04]", text: "text-[#9aa0aa]", border: "border-white/[0.08]" },
  queued: { label: "Scheduled", dot: "bg-amber-400", bg: "bg-amber-500/[0.08]", text: "text-amber-400", border: "border-amber-500/[0.15]" },
  publishing: { label: "Publishing", dot: "bg-blue-400", bg: "bg-blue-500/[0.08]", text: "text-blue-400", border: "border-blue-500/[0.15]" },
  published: { label: "Published", dot: "bg-emerald-400", bg: "bg-emerald-500/[0.08]", text: "text-emerald-400", border: "border-emerald-500/[0.15]" },
  failed: { label: "Failed", dot: "bg-red-400", bg: "bg-red-500/[0.08]", text: "text-red-400", border: "border-red-500/[0.15]" },
};

function StatusBadge({ status }: { status: string }) {
  const meta = STATUS_META[status] ?? {
    label: status,
    dot: "bg-[#9aa0aa]",
    bg: "bg-white/[0.04]",
    text: "text-[#9aa0aa]",
    border: "border-white/[0.08]",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded text-[10px] font-semibold border",
        meta.bg,
        meta.text,
        meta.border,
      )}
    >
      <span className={cn("w-1.5 h-1.5 rounded-full", meta.dot)} />
      {meta.label}
    </span>
  );
}

function EventCard({ post, busy }: { post: CalendarPost; busy?: boolean }) {
  const [alertOpen, setAlertOpen] = useState(false);
  const alerts = post.copyright_alerts ?? [];
  const firstAlert = alerts[0];
  return (
    <div
      className={cn(
        "relative h-full w-full rounded-md border border-white/[0.12] bg-[#1f1f2e] p-1.5 text-left shadow-sm",
        "hover:border-white/[0.30] hover:bg-[#252536] transition-colors cursor-grab active:cursor-grabbing overflow-visible",
        busy && "opacity-60",
      )}
    >
      <div className="flex items-start gap-1.5">
        <div className="w-6 h-6 rounded bg-gradient-to-br from-violet-500 to-blue-500 flex items-center justify-center text-white shrink-0">
          <span className="text-[10px] font-bold">
            {(post.title ?? "?").slice(0, 1).toUpperCase()}
          </span>
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[11px] font-semibold text-white truncate leading-tight">
            {post.title || <span className="text-white/40 font-normal italic">Untitled</span>}
          </p>
          <div className="mt-1">
            <StatusBadge status={post.status} />
            {post.source === "upload" && post.targets && post.targets.length > 0 && (
              <span className="ml-1.5 text-[10px] text-[#9aa0aa]">{post.targets.length} canal{post.targets.length === 1 ? "e" : "i"}</span>
            )}
          </div>
        </div>
        {firstAlert && (
          <button
            type="button"
            aria-label={`Problema copyright${alerts.length > 1 ? ` (${alerts.length})` : ""}`}
            title="Problema copyright"
            onClick={(event) => { event.stopPropagation(); setAlertOpen((open) => !open); }}
            className="relative z-20 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-red-500 text-white shadow-sm hover:bg-red-400"
          >
            <AlertCircle size={14} aria-hidden="true" />
          </button>
        )}
      </div>
      {alertOpen && firstAlert && (
        <div role="dialog" className="absolute right-1 top-8 z-50 w-64 rounded-lg border border-red-200 bg-white p-3 text-left text-[11px] text-[#111] shadow-xl" onClick={(event) => event.stopPropagation()}>
          <p className="font-bold text-red-700">Problema copyright</p>
          <p className="mt-1 leading-relaxed">{firstAlert.message}</p>
          <p className="mt-2 font-mono text-[10px] text-[#6e6e73]">Video: {firstAlert.youtube_video_id}</p>
          {firstAlert.blocked_regions?.length ? <p className="mt-1 text-red-700">Paesi bloccati: {firstAlert.blocked_regions.join(", ")}</p> : null}
          {alerts.length > 1 ? <p className="mt-2 font-semibold text-[#6e6e73]">Altri avvisi: {alerts.length - 1}</p> : null}
        </div>
      )}
    </div>
  );
}

export type CalendarViewMode = "month" | "week" | "day";

type CalendarGridProps = {
  view: CalendarViewMode;
  currentDate: Date;
  posts: CalendarPost[];
  onPostsChange?: () => void;
};

export function CalendarGrid({ view, currentDate, posts, onPostsChange }: CalendarGridProps) {
  const navigate = useNavigate();
  const calendarRef = useRef<FullCalendar>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [conflictMessage, setConflictMessage] = useState<string | null>(null);

  const events: EventInput[] = useMemo(() => {
    return posts
      .filter((p): p is CalendarPost & { scheduled_at: string } => Boolean(p.scheduled_at))
      .map((p) => ({
        id: `${p.source ?? "post"}-${p.id}`,
        start: p.scheduled_at,
        allDay: false,
        extendedProps: p,
      }));
  }, [posts]);

  const scheduledVideoCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const post of posts) {
      if (post.source !== "upload" || !post.scheduled_at) continue;
      const date = new Date(post.scheduled_at);
      const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return counts;
  }, [posts]);

  useEffect(() => {
    const api = calendarRef.current?.getApi();
    if (!api) return;
    api.gotoDate(currentDate);
    const fcView = view === "month" ? "dayGridMonth" : view === "week" ? "timeGridWeek" : "timeGridDay";
    if (api.view.type !== fcView) {
      api.changeView(fcView);
    }
  }, [view, currentDate]);

  const handleEventDrop = useCallback(
    async (arg: EventDropArg) => {
      const newDate = arg.event.start;
      if (!newDate) {
        arg.revert();
        return;
      }
      const eventKey = arg.event.id;
      const separator = eventKey.indexOf("-");
      const source = separator > 0 ? eventKey.slice(0, separator) : "post";
      const id = separator > 0 ? eventKey.slice(separator + 1) : eventKey;
      const movedPost = arg.event.extendedProps as CalendarPost;
      const conflict = findSchedulingConflict(posts, movedPost, newDate);
      if (conflict) {
        arg.revert();
        setConflictMessage(conflict);
        return;
      }
      setConflictMessage(null);
      setBusyId(eventKey);
      try {
        const endpoint = source === "upload" ? `/api/v1/uploads/${id}/reschedule` : `/api/v1/posts/${id}`;
        await authedFetch(endpoint, {
          method: "PATCH",
          body: JSON.stringify(source === "upload"
            ? { publish_at: newDate.toISOString() }
            : { scheduled_at: newDate.toISOString() }),
        });
        onPostsChange?.();
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const message = err instanceof ApiError ? err.message : "Unable to reschedule post.";
        // eslint-disable-next-line no-console
        console.error(message);
        arg.revert();
      } finally {
        setBusyId((current) => (current === eventKey ? null : current));
      }
    },
    [navigate, onPostsChange],
  );



  return (
    <div className="fc-dark-theme flex-1 min-h-0 min-w-0">
      {conflictMessage && (
        <div
          className="mb-3 flex items-start gap-2 rounded-xl border border-amber-400/30 bg-amber-400/[0.08] px-4 py-3 text-sm text-amber-100"
          role="alert"
          data-testid="calendar-conflict-warning"
        >
          <AlertCircle size={17} className="mt-0.5 shrink-0 text-amber-300" aria-hidden="true" />
          <div className="flex-1">{conflictMessage}</div>
          <button type="button" onClick={() => setConflictMessage(null)} className="text-xs text-amber-200 underline hover:text-white">
            Chiudi
          </button>
        </div>
      )}
      <FullCalendar
        ref={calendarRef}
        plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin]}
        initialView="dayGridMonth"
        headerToolbar={false}
        editable={true}
        events={events}
        eventContent={(eventInfo) => {
          const post = eventInfo.event.extendedProps as CalendarPost;
          return <EventCard post={post} busy={busyId === eventInfo.event.id} />;
        }}
        eventDrop={handleEventDrop}
        eventClassNames={() => "border-none bg-transparent"}
        slotMinTime="00:00:00"
        slotMaxTime="24:00:00"
        allDaySlot={false}
        nowIndicator={true}
        dayMaxEvents={4}
        dayCellContent={(arg) => {
          const date = arg.date;
          const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
          const count = scheduledVideoCounts.get(key) ?? 0;
          return (
            <div className="flex w-full items-center justify-between gap-1 px-1 py-0.5">
              <span>{arg.dayNumberText}</span>
              {count > 0 && <span className="rounded-full bg-violet-500/20 px-1.5 py-0.5 text-[9px] font-bold text-violet-200">{count} video</span>}
            </div>
          );
        }}
        dayHeaderFormat={{ weekday: "short", day: "numeric" }}
      />
    </div>
  );
}

function findSchedulingConflict(
  posts: CalendarPost[],
  movedPost: CalendarPost,
  newDate: Date,
): string | null {
  const movedTargets = new Set(movedPost.targets ?? []);
  if (movedTargets.size === 0) return null;
  const movedTime = newDate.getTime();
  const conflict = posts.find((post) => {
    if (post.id === movedPost.id && post.source === movedPost.source) return false;
    if (!post.scheduled_at || post.status === "published") return false;
    const scheduledTime = new Date(post.scheduled_at).getTime();
    if (!Number.isFinite(scheduledTime) || Math.abs(scheduledTime - movedTime) >= 30 * 60 * 1000) return false;
    return (post.targets ?? []).some((targetId) => movedTargets.has(targetId));
  });
  if (!conflict) return null;
  return `Conflitto di programmazione: “${conflict.title || "Senza titolo"}” usa già lo stesso canale in questa fascia oraria. Scegli un altro orario.`;
}

export type { CalendarPost };
