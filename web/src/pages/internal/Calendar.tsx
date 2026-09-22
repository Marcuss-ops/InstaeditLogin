import { useState } from "react";
import { Link } from "react-router-dom";
import { Calendar as CalendarIcon, ChevronRight, Plus } from "lucide-react";
import { type CalendarViewMode } from "./CalendarGrid";
import { useCalendarPosts } from "./useCalendarPosts";
import { CalendarToolbar } from "./CalendarToolbar";
import { CalendarPostsPanel } from "./CalendarPostsPanel";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";
import { useVideoJobs, videoJobStageLabel } from "./videoJobs";

export function CalendarPage() {
  const [view, setView] = useState<CalendarViewMode>("month");
  const [currentDate, setCurrentDate] = useState(new Date());
  const posts = useCalendarPosts();
  const jobs = useVideoJobs();

  function shiftDate(delta: number) {
    setCurrentDate((prev) => {
      const next = new Date(prev);
      if (view === "month") next.setMonth(next.getMonth() + delta);
      else if (view === "week") next.setDate(next.getDate() + delta * 7);
      else next.setDate(next.getDate() + delta);
      return next;
    });
  }

  const formattedDate = currentDate.toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });

  return (
    <div className="min-h-full p-4 sm:p-6 lg:p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto h-[calc(100vh-64px-2rem)] flex flex-col">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between mb-6 shrink-0">
          <div>
            <h1 className="text-[24px] sm:text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <CalendarIcon size={28} className="text-white/40" />
              Calendar
            </h1>
            <p className="text-[14px] sm:text-[15px] text-[#9aa0aa] mt-1">
              Video programmati per tutti i tuoi canali, in un calendario unico.
            </p>
          </div>

          <div className="flex items-center gap-2">
            <Link
              to="/app/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
            >
              <Plus size={16} /> Nuovo post
            </Link>
          </div>
        </div>

        <CalendarToolbar
            view={view}
            setView={setView}
            shiftDate={shiftDate}
            setCurrentDate={setCurrentDate}
            formattedDate={formattedDate}
            statusFilter={posts.statusFilter}
            setStatusFilter={posts.setStatusFilter}
            groupFilter={posts.groupFilter}
            setGroupFilter={posts.setGroupFilter}
            groups={posts.state.kind === "ready" ? posts.state.groups : []}
            hasActiveFilters={posts.hasActiveFilters}
            clearFilters={posts.clearFilters}
          />

        <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
          <CalendarPostsPanel
              state={posts.state}
              filteredPosts={posts.filteredPosts}
              view={view}
              currentDate={currentDate}
              hasActiveFilters={posts.hasActiveFilters}
              clearFilters={posts.clearFilters}
              load={posts.load}
            />
          <aside className="app-card min-h-0 overflow-y-auto rounded-2xl p-4 sm:p-5">
            <div className="flex items-start justify-between gap-3">
              <div><p className="app-eyebrow">Autopilot</p><h2 className="app-card-title mt-1 text-base font-semibold">Job programmati</h2></div>
              <Link to="/app/jobs" className="app-icon-button no-underline" aria-label="Apri AI Jobs"><Plus size={15} /></Link>
            </div>
            {jobs.state.kind === "loading" && <div className="app-card-muted mt-6 flex items-center gap-2 text-xs"><span className="app-spinner" /> Caricamento…</div>}
            {jobs.state.kind === "error" && <p className="app-card-muted mt-6 text-xs">La coda AI non è disponibile in questo momento.</p>}
            {jobs.state.kind === "ready" && jobs.state.jobs.length === 0 && <p className="app-card-muted mt-6 text-xs leading-5">Nessun job programmato. Crea la tua prima pipeline autonoma.</p>}
            {jobs.state.kind === "ready" && jobs.state.jobs.length > 0 && <div className="mt-5 space-y-2">{jobs.state.jobs.slice(0, 12).map((job) => <Link key={job.id} to={`/app/jobs#${job.id}`} className="app-calendar-job block rounded-xl p-3 no-underline"><div className="flex items-center justify-between gap-2"><span className="app-card-title truncate text-xs font-semibold">{job.project_id || "Video AI"}</span><span className="app-card-muted text-[10px]">{job.publish_at ? new Date(job.publish_at).toLocaleDateString("it-IT", { day: "2-digit", month: "short" }) : "Coda"}</span></div><p className="app-card-muted mt-1 text-[11px]">{videoJobStageLabel(job)}</p></Link>)}</div>}
            <Link to="/app/jobs" className="app-inline-link mt-5 inline-flex text-xs no-underline">Gestisci tutti i job <ChevronRight size={13} /></Link>
          </aside>
        </div>

        {posts.groupFilter !== "all" && Number.isFinite(Number(posts.groupFilter)) && (
          <div className="mt-4 shrink-0">
            <GroupYouTubeVideos groupId={Number(posts.groupFilter)} />
          </div>
        )}
      </div>
    </div>
  );
}
