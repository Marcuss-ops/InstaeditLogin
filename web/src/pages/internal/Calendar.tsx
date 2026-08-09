import { useState } from "react";
import { Link } from "react-router-dom";
import { Calendar as CalendarIcon, Plus } from "lucide-react";
import { type CalendarViewMode } from "./CalendarGrid";
import { useCalendarPosts } from "./useCalendarPosts";
import { CalendarToolbar } from "./CalendarToolbar";
import { CalendarPostsPanel } from "./CalendarPostsPanel";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";

export function CalendarPage() {
  const [view, setView] = useState<CalendarViewMode>("month");
  const [currentDate, setCurrentDate] = useState(new Date());
  const posts = useCalendarPosts();

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
    </div>
  );
}
