import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import timeGridPlugin from "@fullcalendar/timegrid";
import interactionPlugin from "@fullcalendar/interaction";
import type { EventDropArg, EventInput } from "@fullcalendar/core";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";
import { useNavigate } from "react-router-dom";
import { cn } from "../../lib/utils";
import { AlertCircle, ExternalLink, Loader2, Play, X } from "lucide-react";
import type { Post } from "./calendarTypes";

type PostStatus = "draft" | "queued" | "publishing" | "published" | "failed";

type CalendarPost = Post & { status: PostStatus | string };

function getCalendarRange(date: Date, view: CalendarViewMode): { start: Date; end: Date } {
  if (view === "month") {
    const start = new Date(date);
    start.setHours(0, 0, 0, 0);
    const end = new Date(start);
    end.setDate(end.getDate() + 30);
    return { start, end };
  }
  const start = new Date(date);
  start.setHours(0, 0, 0, 0);
  const day = start.getDay();
  start.setDate(start.getDate() + (day === 0 ? -6 : 1 - day));
  const end = new Date(start);
  end.setDate(end.getDate() + (view === "week" ? 7 : 1));
  return { start, end };
}

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
        "relative h-full w-full rounded-2xl border border-white/[0.10] bg-[#242424]/95 p-2.5 text-left shadow-[0_8px_24px_rgba(0,0,0,0.16)]",
        "hover:border-white/[0.22] hover:bg-[#2a2a2a] transition-all cursor-grab active:cursor-grabbing overflow-visible",
        busy && "opacity-60",
      )}
    >
      <div className="flex items-start gap-1.5">
        <div className="w-7 h-7 rounded-md bg-neutral-200 border border-neutral-300 flex items-center justify-center text-neutral-700 shrink-0">
          <span className="text-[11px] font-bold">
            {(post.title ?? "?").slice(0, 1).toUpperCase()}
          </span>
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[11px] font-semibold text-white truncate leading-tight">
            {post.title || <span className="text-white/40 font-normal italic">Untitled</span>}
          </p>
          <div className="mt-1">
            <StatusBadge status={post.status} />
            {post.generation_status && post.generation_status !== "CONTENT_READY" && (
              <div className="mt-1.5" aria-label={`Generazione ${post.generation_status} ${post.generation_progress ?? 0}%`}>
                <div className="flex justify-between gap-1 text-[9px] text-sky-200"><span className="truncate">{post.generation_phase || post.generation_status}</span><span>{post.generation_progress ?? 0}%</span></div>
                <div className="mt-0.5 h-1 overflow-hidden rounded bg-white/10"><div className="h-full rounded bg-sky-400 transition-all" style={{ width: `${Math.max(0, Math.min(100, post.generation_progress ?? 0))}%` }} /></div>
              </div>
            )}
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
  const [selectedPost, setSelectedPost] = useState<CalendarPost | null>(null);
  const [actionBusy, setActionBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const selectedCalendarPost = selectedPost
    ? posts.find((post) => post.id === selectedPost.id && (post.source ?? "post") === (selectedPost.source ?? "post")) ?? selectedPost
    : null;
  const calendarRange = useMemo(() => getCalendarRange(currentDate, view), [currentDate, view]);

  const events: EventInput[] = useMemo(() => {
    return posts
      .filter((p): p is CalendarPost & { scheduled_at: string } => {
        if (!p.scheduled_at) return false;
        const scheduledAt = new Date(p.scheduled_at);
        return scheduledAt >= calendarRange.start && scheduledAt < calendarRange.end;
      })
      .map((p) => ({
        id: `${p.source ?? "post"}-${p.id}`,
        start: p.scheduled_at,
        allDay: false,
        extendedProps: p,
      }));
  }, [calendarRange.end, calendarRange.start, posts]);

  const scheduledVideoCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const post of posts) {
      if (post.source !== "upload" || !post.scheduled_at) continue;
      const date = new Date(post.scheduled_at);
      if (date < calendarRange.start || date >= calendarRange.end) continue;
      const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return counts;
  }, [calendarRange.end, calendarRange.start, posts]);

  useEffect(() => {
    const api = calendarRef.current?.getApi();
    if (!api) return;
    api.gotoDate(currentDate);
    const fcView = view === "month" ? "calendar30" : view === "week" ? "timeGridWeek" : "timeGridDay";
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
    [navigate, onPostsChange, posts],
  );

  const runPostNow = async () => {
    if (!selectedPost || selectedPost.source === "upload") return;
    setActionBusy(true);
    setActionError("");
    try {
      await authedFetch(`/api/v1/posts/${selectedPost.id}/publish`, { method: "POST" });
      setSelectedPost(null);
      onPostsChange?.();
    } catch (err) {
      if (err instanceof AuthError) { navigate("/login", { replace: true }); return; }
      setActionError(err instanceof ApiError ? err.message : "Impossibile avviare la pubblicazione.");
    } finally { setActionBusy(false); }
  };

  const cancelScheduledPost = async () => {
    if (!selectedPost || selectedPost.source === "upload") return;
    setActionBusy(true);
    setActionError("");
    try {
      await authedFetch(`/api/v1/posts/${selectedPost.id}/cancel`, { method: "POST" });
      setSelectedPost(null);
      onPostsChange?.();
    } catch (err) {
      if (err instanceof AuthError) { navigate("/login", { replace: true }); return; }
      setActionError(err instanceof ApiError ? err.message : "Impossibile annullare il post.");
    } finally { setActionBusy(false); }
  };



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
        initialView="calendar30"
        views={{ calendar30: { type: "dayGrid", duration: { days: 30 }, buttonText: "30 giorni" } }}
        initialDate={currentDate}
        firstDay={1}
        validRange={calendarRange}
        headerToolbar={false}
        editable={true}
        events={events}
        eventContent={(eventInfo) => {
          const post = eventInfo.event.extendedProps as CalendarPost;
          return <EventCard post={post} busy={busyId === eventInfo.event.id} />;
        }}
        eventClick={(info) => setSelectedPost(info.event.extendedProps as CalendarPost)}
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
              {count > 0 && <span className="rounded-full border border-black/10 bg-black/[0.06] px-1.5 py-0.5 text-[9px] font-bold text-neutral-700">{count} video</span>}
            </div>
          );
        }}
        dayHeaderFormat={{ weekday: "short", day: "numeric" }}
      />
      {selectedCalendarPost && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label={`Dettaglio video ${selectedCalendarPost.title ?? ""}`} onMouseDown={(event) => { if (event.target === event.currentTarget) setSelectedPost(null); }}>
          <div className="w-full max-w-3xl overflow-hidden rounded-2xl border border-white/15 bg-[#17171b] text-white shadow-2xl">
            <div className="flex items-start justify-between gap-4 border-b border-white/10 p-5">
              <div><StatusBadge status={selectedCalendarPost.status} /><h2 className="mt-2 text-xl font-bold">{selectedCalendarPost.title || "Video programmato"}</h2><p className="mt-1 text-sm text-white/55">{selectedCalendarPost.scheduled_at ? new Date(selectedCalendarPost.scheduled_at).toLocaleString() : "Data non impostata"}</p></div>
              <button type="button" onClick={() => setSelectedPost(null)} className="rounded-lg p-2 text-white/55 hover:bg-white/10 hover:text-white" aria-label="Chiudi dettaglio"><X size={18} /></button>
            </div>
            <div className="space-y-4 p-5">
              {selectedCalendarPost.caption && <p className="whitespace-pre-wrap text-sm leading-6 text-white/75">{selectedCalendarPost.caption}</p>}
              {actionError && <p role="alert" className="rounded-lg border border-red-400/25 bg-red-400/10 px-3 py-2 text-sm text-red-200">{actionError}</p>}
              {selectedCalendarPost.generation_status && selectedCalendarPost.generation_status !== "CONTENT_READY" && <div className="rounded-xl border border-sky-400/20 bg-sky-400/[0.06] p-4"><div className="flex justify-between text-sm"><span>{selectedCalendarPost.generation_phase || selectedCalendarPost.generation_status}</span><span>{selectedCalendarPost.generation_progress ?? 0}%</span></div><div className="mt-2 h-1.5 overflow-hidden rounded bg-white/10"><div className="h-full bg-sky-400" style={{ width: `${Math.max(0, Math.min(100, selectedCalendarPost.generation_progress ?? 0))}%` }} /></div>{selectedCalendarPost.generation_status === "FAILED" && <p className="mt-2 text-sm text-red-200">Generazione fallita. Dettagli: {JSON.stringify(selectedCalendarPost.generation_snapshot ?? {})}</p>}</div>}
              {selectedCalendarPost.source !== "upload" && (selectedCalendarPost.status === "queued" || selectedCalendarPost.status === "draft") && <div className="flex flex-wrap gap-2">
                {selectedCalendarPost.status === "queued" && <button type="button" disabled={actionBusy} onClick={() => void runPostNow()} className="inline-flex items-center gap-2 rounded-lg bg-emerald-300 px-3 py-2 text-sm font-semibold text-black disabled:opacity-50">{actionBusy ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />}Pubblica ora</button>}
                <button type="button" disabled={actionBusy} onClick={() => void cancelScheduledPost()} className="rounded-lg border border-white/15 px-3 py-2 text-sm font-semibold text-white/75 hover:bg-white/10 disabled:opacity-50">Annulla programmazione</button>
              </div>}
              {selectedCalendarPost.media_url ? <>
                <video className="max-h-[55vh] w-full rounded-xl bg-black" controls preload="metadata" src={selectedCalendarPost.media_url}>Il browser non supporta la riproduzione video.</video>
                <a href={selectedCalendarPost.media_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-2 rounded-lg bg-white px-3 py-2 text-sm font-semibold text-black hover:bg-white/90"><ExternalLink size={15} />Apri video finale</a>
              </> : <p className="rounded-xl border border-white/10 bg-white/[0.04] p-4 text-sm text-white/55">Il video finale non è ancora disponibile. La card si aggiornerà quando la generazione terminerà.</p>}
            </div>
          </div>
        </div>
      )}
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
