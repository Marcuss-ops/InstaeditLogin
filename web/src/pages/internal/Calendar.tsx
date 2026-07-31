import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Calendar as CalendarIcon, Plus, Video } from "lucide-react";
import { cn } from "../../lib/utils";
import { type CalendarViewMode } from "./CalendarGrid";
import { useCalendarPosts } from "./useCalendarPosts";
import { usePrivateVideos } from "./usePrivateVideos";
import { CalendarToolbar } from "./CalendarToolbar";
import { CalendarPostsPanel } from "./CalendarPostsPanel";
import { PrivateVideosPanel } from "./PrivateVideosPanel";
import type { CalendarTab } from "./calendarTypes";

export function CalendarPage() {
  const [searchParams] = useSearchParams();
  const accountId = searchParams.get("account_id");
  const [activeTab, setActiveTab] = useState<CalendarTab>("calendar");
  const [view, setView] = useState<CalendarViewMode>("week");
  const [currentDate, setCurrentDate] = useState(new Date());
  const posts = useCalendarPosts();
  const videos = usePrivateVideos(accountId, activeTab === "videos");

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
              Plan, drag and schedule your content across all connected channels.
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

        {accountId && (
          <div className="flex items-center gap-1 mb-4 shrink-0">
            {[
              { id: "calendar" as const, label: "Calendario", icon: CalendarIcon },
              { id: "videos" as const, label: "Video Privati", icon: Video },
            ].map((tab) => {
              const Icon = tab.icon;
              const active = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => setActiveTab(tab.id)}
                  className={cn(
                    "flex items-center gap-1.5 px-4 py-2 rounded-xl text-[13px] font-medium transition-all",
                    active
                      ? "bg-white/[0.08] text-white shadow-[inset_0_1px_0_0_rgba(255,255,255,0.1)]"
                      : "text-[#9aa0aa] hover:text-white hover:bg-white/[0.04]",
                  )}
                >
                  <Icon size={14} />
                  {tab.label}
                </button>
              );
            })}
          </div>
        )}

        {(!accountId || activeTab === "calendar") && (
          <CalendarToolbar
            view={view}
            setView={setView}
            shiftDate={shiftDate}
            setCurrentDate={setCurrentDate}
            formattedDate={formattedDate}
            statusFilter={posts.statusFilter}
            setStatusFilter={posts.setStatusFilter}
            workspaceFilter={posts.workspaceFilter}
            setWorkspaceFilter={posts.setWorkspaceFilter}
            workspaces={posts.state.kind === "ready" ? posts.state.workspaces : []}
            hasActiveFilters={posts.hasActiveFilters}
            clearFilters={posts.clearFilters}
          />
        )}

        {(!accountId || activeTab === "calendar") && (
          <CalendarPostsPanel
            state={posts.state}
            filteredPosts={posts.filteredPosts}
            view={view}
            currentDate={currentDate}
            hasActiveFilters={posts.hasActiveFilters}
            clearFilters={posts.clearFilters}
            load={posts.load}
          />
        )}

        {accountId && activeTab === "videos" && (
          <PrivateVideosPanel
            videoState={videos.videoState}
            loadVideos={videos.loadVideos}
            handleEditThumbnail={videos.handleEditThumbnail}
          />
        )}
      </div>
    </div>
  );
}
