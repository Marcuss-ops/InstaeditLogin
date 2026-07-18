import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import timeGridPlugin from "@fullcalendar/timegrid";
import interactionPlugin from "@fullcalendar/interaction";
import type { EventDropArg, EventInput } from "@fullcalendar/core";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";
import { useNavigate } from "react-router-dom";
import { cn } from "../../lib/utils";

type PostStatus = "draft" | "queued" | "publishing" | "published" | "failed";

type CalendarPost = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: PostStatus | string;
  created_at: string;
};

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
  return (
    <div
      className={cn(
        "h-full w-full rounded-md border border-white/[0.12] bg-[#1f1f2e] p-1.5 text-left shadow-sm",
        "hover:border-white/[0.30] hover:bg-[#252536] transition-colors cursor-grab active:cursor-grabbing overflow-hidden",
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
          </div>
        </div>
      </div>
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

  const events: EventInput[] = useMemo(() => {
    return posts
      .filter((p): p is CalendarPost & { scheduled_at: string } => Boolean(p.scheduled_at))
      .map((p) => ({
        id: String(p.id),
        start: p.scheduled_at,
        allDay: false,
        extendedProps: p,
      }));
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
      const id = arg.event.id;
      setBusyId(id);
      try {
        await authedFetch(`/api/v1/posts/${id}`, {
          method: "PATCH",
          body: JSON.stringify({ scheduled_at: newDate.toISOString() }),
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
        setBusyId((current) => (current === id ? null : current));
      }
    },
    [navigate, onPostsChange],
  );



  return (
    <div className="fc-dark-theme flex-1 min-h-0 min-w-0">
      <FullCalendar
        ref={calendarRef}
        plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin]}
        initialView="timeGridWeek"
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
        dayHeaderFormat={{ weekday: "short", day: "numeric" }}
      />
    </div>
  );
}

export type { CalendarPost };
