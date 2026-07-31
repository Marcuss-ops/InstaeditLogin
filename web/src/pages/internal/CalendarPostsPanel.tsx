import { Link } from "react-router-dom";
import { Filter, Plus, X } from "lucide-react";
import { Skeleton, ErrorState } from "../../components/feedback";
import { EmptyState } from "../../components/feedback/EmptyState";
import { CalendarGrid, type CalendarViewMode } from "./CalendarGrid";
import type { FetchState, Post } from "./calendarTypes";

export function CalendarPostsPanel({
  state,
  filteredPosts,
  view,
  currentDate,
  hasActiveFilters,
  clearFilters,
  load,
}: {
  state: FetchState;
  filteredPosts: Post[];
  view: CalendarViewMode;
  currentDate: Date;
  hasActiveFilters: boolean;
  clearFilters: () => void;
  load: () => void | Promise<void>;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-4 sm:p-6 flex-1 min-h-0 flex flex-col">
      {state.kind === "loading" && (
        <div className="flex-1 flex flex-col gap-4">
          <Skeleton variant="card" height={48} />
          <Skeleton variant="card" className="flex-1" />
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

      {state.kind === "ready" && state.posts.length === 0 && (
        <EmptyState
          title="Nessun post ancora programmato"
          description="Crea il tuo primo post per vederlo nel calendario."
          icon={<Plus size={32} />}
          cta={
            <Link
              to="/app/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
              data-testid="calendar-empty-compose"
            >
              <Plus size={16} /> Nuovo post
            </Link>
          }
          className="bg-[#1f1f2e] border-white/[0.12]"
        />
      )}

      {state.kind === "ready" &&
        state.posts.length > 0 &&
        (hasActiveFilters && filteredPosts.length === 0 ? (
          <EmptyState
            title="Nessun post corrisponde ai filtri"
            description="Prova a cancellare i filtri o crea un nuovo post."
            icon={<Filter size={32} />}
            cta={
              <button
                type="button"
                data-testid="calendar-empty-clear"
                onClick={clearFilters}
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
              >
                <X size={16} /> Cancella filtri
              </button>
            }
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        ) : (
          <CalendarGrid
            view={view}
            currentDate={currentDate}
            posts={filteredPosts}
            onPostsChange={load}
          />
        ))}
    </div>
  );
}
