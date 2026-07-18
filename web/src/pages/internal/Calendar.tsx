import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  Calendar as CalendarIcon,
  ChevronLeft,
  ChevronRight,
  LayoutGrid,
  Clock,
  Filter,
  Plus,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { authedFetch, AuthError, ApiError } from "../../lib/auth";
import { CalendarGrid, type CalendarViewMode } from "./CalendarGrid";
import { Skeleton, ErrorState } from "../../components/feedback";

type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; posts: Post[] }
  | { kind: "error"; message: string };

const viewTabs: { id: CalendarViewMode; label: string; icon: React.ElementType }[] = [
  { id: "month", label: "Month", icon: CalendarIcon },
  { id: "week", label: "Week", icon: LayoutGrid },
  { id: "day", label: "Day", icon: Clock },
];

export function CalendarPage() {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [view, setView] = useState<CalendarViewMode>("week");
  const [currentDate, setCurrentDate] = useState(new Date());

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const resp = await authedFetch("/api/v1/posts", { signal: controller.signal });
      if (controller.signal.aborted) return;
      const data = (await resp.json()) as { posts: Post[] };
      setState({ kind: "ready", posts: data.posts ?? [] });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof ApiError ? err.message : "Unable to load posts.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

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
        {/* Header */}
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
              <Plus size={16} /> New post
            </Link>
          </div>
        </div>

        {/* Toolbar */}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between mb-4 shrink-0">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => shiftDate(-1)}
              className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
              aria-label="Previous"
            >
              <ChevronLeft size={18} />
            </button>
            <button
              type="button"
              onClick={() => setCurrentDate(new Date())}
              className="px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              Today
            </button>
            <button
              type="button"
              onClick={() => shiftDate(1)}
              className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
              aria-label="Next"
            >
              <ChevronRight size={18} />
            </button>
            <h2 className="ml-2 text-[16px] sm:text-[18px] font-bold text-white min-w-[140px]">
              {formattedDate}
            </h2>
          </div>

          <div className="flex items-center gap-2">
            <div className="inline-flex p-1 rounded-xl bg-white/[0.04] border border-white/[0.08]">
              {viewTabs.map((tab) => {
                const Icon = tab.icon;
                const active = view === tab.id;
                return (
                  <button
                    key={tab.id}
                    type="button"
                    onClick={() => setView(tab.id)}
                    className={cn(
                      "flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all",
                      active
                        ? "bg-white/[0.08] text-white shadow-[inset_0_1px_0_0_rgba(255,255,255,0.1)]"
                        : "text-[#9aa0aa] hover:text-white hover:bg-white/[0.04]",
                    )}
                  >
                    <Icon size={14} />
                    <span className="hidden sm:inline">{tab.label}</span>
                  </button>
                );
              })}
            </div>

            <button
              type="button"
              disabled
              className="inline-flex items-center gap-1.5 px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-[#9aa0aa] transition-colors opacity-60 cursor-not-allowed"
              aria-label="Filter (coming soon)"
            >
              <Filter size={14} /> Filter
            </button>
          </div>
        </div>

        {/* Calendar surface */}
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

          {state.kind === "ready" && (
            <CalendarGrid
              view={view}
              currentDate={currentDate}
              posts={state.posts}
              onPostsChange={load}
            />
          )}
        </div>
      </div>
    </div>
  );
}
