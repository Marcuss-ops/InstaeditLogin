import { useState } from "react";
import { Calendar as CalendarIcon } from "lucide-react";
import { type CalendarViewMode } from "./CalendarGrid";
import { useCalendarPosts } from "./useCalendarPosts";
import { CalendarToolbar } from "./CalendarToolbar";
import { CalendarPostsPanel } from "./CalendarPostsPanel";

export function CalendarScheduleView() {
  const view: CalendarViewMode = "month";
  const [currentDate, setCurrentDate] = useState(() => new Date());
  const posts = useCalendarPosts();
  const horizonEnd = new Date(currentDate);
  horizonEnd.setDate(horizonEnd.getDate() + 29);
  const formattedDate = `${currentDate.toLocaleDateString(undefined, { day: "numeric", month: "short" })} – ${horizonEnd.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}`;

  return <div className="flex min-h-0 flex-1 flex-col">
    <div className="mb-4 flex items-center gap-2 text-xs text-white/45"><CalendarIcon size={14} />Vista calendario · per pianificare un periodo limitato</div>
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
    <CalendarPostsPanel state={posts.state} filteredPosts={posts.filteredPosts} view={view} currentDate={currentDate} hasActiveFilters={posts.hasActiveFilters} clearFilters={posts.clearFilters} load={posts.load} />
  </div>;
}
