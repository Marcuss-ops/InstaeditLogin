import { useState } from "react";
import { Link } from "react-router-dom";
import { Calendar as CalendarIcon, Cpu, Plus } from "lucide-react";
import { type CalendarViewMode } from "./CalendarGrid";
import { useCalendarPosts } from "./useCalendarPosts";
import { CalendarToolbar } from "./CalendarToolbar";
import { CalendarPostsPanel } from "./CalendarPostsPanel";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";
import { RemoteJobDialog } from "./RemoteJobDialog";

export function CalendarPage() {
  const view: CalendarViewMode = "month";
  const [currentDate, setCurrentDate] = useState(() => new Date());
  const [jobDialogOpen, setJobDialogOpen] = useState(false);
  const posts = useCalendarPosts();
  const horizonEnd = new Date(currentDate);
  horizonEnd.setDate(horizonEnd.getDate() + 29);
  const formattedDate = `${currentDate.toLocaleDateString(undefined, { day: "numeric", month: "short" })} – ${horizonEnd.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}`;

  return (
    <div className="min-h-full p-4 sm:p-6 lg:p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="w-full max-w-none h-[calc(100vh-64px-2rem)] flex flex-col">
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
            <button
              type="button"
              onClick={() => setJobDialogOpen(true)}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl border border-white/[0.12] bg-white/[0.04] text-white text-[13px] font-semibold hover:bg-white/[0.08] transition-colors"
            >
              <Cpu size={16} /> Invia job
            </button>
            <Link
              to="/app/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
            >
              <Plus size={16} /> Nuovo post
            </Link>
          </div>
        </div>

        <CalendarToolbar
            formattedDate={formattedDate}
            onPrevious={() => setCurrentDate((date) => { const next = new Date(date); next.setDate(next.getDate() - 30); return next; })}
            onNext={() => setCurrentDate((date) => { const next = new Date(date); next.setDate(next.getDate() + 30); return next; })}
            onToday={() => setCurrentDate(new Date())}
            statusFilter={posts.statusFilter}
            setStatusFilter={posts.setStatusFilter}
            groupFilter={posts.groupFilter}
            setGroupFilter={posts.setGroupFilter}
            groups={posts.state.kind === "ready" ? posts.state.groups : []}
            hasActiveFilters={posts.hasActiveFilters}
            clearFilters={posts.clearFilters}
          />

        <CalendarPostsPanel
            state={posts.state}
            filteredPosts={posts.filteredPosts}
            view={view}
            currentDate={currentDate}
            hasActiveFilters={posts.hasActiveFilters}
            clearFilters={posts.clearFilters}
            load={posts.load}
          />

        {posts.groupFilter !== "all" && Number.isFinite(Number(posts.groupFilter)) && (
          <div className="mt-4 shrink-0">
            <GroupYouTubeVideos groupId={Number(posts.groupFilter)} />
          </div>
        )}
      </div>
      <RemoteJobDialog open={jobDialogOpen} onClose={() => setJobDialogOpen(false)} onCalendarRefresh={posts.load} />
    </div>
  );
}
