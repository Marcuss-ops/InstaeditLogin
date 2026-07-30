import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  Calendar as CalendarIcon,
  ChevronLeft,
  ChevronRight,
  LayoutGrid,
  Clock,
  Filter,
  Plus,
  X,
  Video,
  ExternalLink,
  Loader2,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { authedFetch, AuthError, ApiError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { CalendarGrid, type CalendarViewMode } from "./CalendarGrid";
import { Skeleton, ErrorState } from "../../components/feedback";
import { EmptyState } from "../../components/feedback/EmptyState";
import { createEditorSessionAndOpen } from "../../features/youtube/api/editorSessionsApi";

type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
};

type Workspace = { id: number; name: string };

type ContentMetric = { key: string; label: string; value: number; display_value: string };

type ContentItem = {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
  metrics?: ContentMetric[];
  properties?: Record<string, unknown>;
};

type ContentPage = { items: ContentItem[]; next_cursor?: string };

type CalendarTab = "calendar" | "videos";

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; posts: Post[]; workspaces: Workspace[] }
  | { kind: "error"; message: string };

const viewTabs: { id: CalendarViewMode; label: string; icon: React.ElementType }[] = [
  { id: "month", label: "Mese", icon: CalendarIcon },
  { id: "week", label: "Settimana", icon: LayoutGrid },
  { id: "day", label: "Giorno", icon: Clock },
];

export function CalendarPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [view, setView] = useState<CalendarViewMode>("week");
  const [currentDate, setCurrentDate] = useState(new Date());
  const toast = useToast();

  const accountId = searchParams.get("account_id");
  const [activeTab, setActiveTab] = useState<CalendarTab>("calendar");
  const [videoState, setVideoState] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ready"; items: ContentItem[]; nextCursor?: string; isLoadingMore?: boolean; loadMoreError?: string }
    | { kind: "error"; message: string }
  >({ kind: "idle" });


  const statusFilter = searchParams.get("status") || "all";
  const workspaceFilter = searchParams.get("workspace_id") || "all";

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });


    try {
      const [postsResp, workspacesResp] = await Promise.all([
        authedFetch("/api/v1/posts", { signal: controller.signal }),
        authedFetch("/api/v1/workspaces", { signal: controller.signal }).catch(() => null),
      ]);
      if (controller.signal.aborted) return;
      const data = (await postsResp.json()) as { posts: Post[] };
      let workspaces: Workspace[] = [];
      if (workspacesResp && workspacesResp.ok) {
        const wsData = (await workspacesResp.json()) as { workspaces: Workspace[] };
        workspaces = wsData.workspaces ?? [];
      }
      setState({ kind: "ready", posts: data.posts ?? [], workspaces });
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

  const filteredPosts =
    state.kind === "ready"
      ? state.posts.filter((post) => {
          if (statusFilter !== "all" && post.status !== statusFilter) return false;
          if (workspaceFilter !== "all" && String(post.workspace_id) !== workspaceFilter) return false;
          return true;
        })
      : [];

  const hasActiveFilters = statusFilter !== "all" || workspaceFilter !== "all";

  const setStatusFilter = (value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === "all") next.delete("status");
        else next.set("status", value);
        return next;
      },
      { replace: true },
    );
  };

  const setWorkspaceFilter = (value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === "all") next.delete("workspace_id");
        else next.set("workspace_id", value);
        return next;
      },
      { replace: true },
    );
  };

  const clearFilters = () => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("status");
        next.delete("workspace_id");
        return next;
      },
      { replace: true },
    );
  };

  const loadVideos = useCallback(
    async (cursor?: string) => {
      if (!accountId) return;
      const isAppend = !!cursor;
      if (isAppend) {
        setVideoState((prev) =>
          prev.kind === "ready"
            ? { ...prev, isLoadingMore: true, loadMoreError: undefined }
            : { kind: "loading" },
        );
      } else {
        setVideoState({ kind: "loading" });
      }
      try {
        const url = `/api/v1/accounts/${accountId}/content?limit=20${cursor ? `&cursor=${cursor}` : ""}&privacy=private`;
        const response = await authedFetch(url);
        const data = (await response.json()) as ContentPage;
        setVideoState((prev) => ({
          kind: "ready",
          items:
            isAppend && prev.kind === "ready"
              ? [...prev.items, ...data.items]
              : data.items,
          nextCursor: data.next_cursor,
          isLoadingMore: false,
          loadMoreError: undefined,
        }));
      } catch (err) {
        const message = err instanceof Error ? err.message : "Unable to load videos.";
        setVideoState((prev) => {
          if (isAppend && prev.kind === "ready") {
            return { ...prev, isLoadingMore: false, loadMoreError: message };
          }
          return { kind: "error", message };
        });
      }
    },
    [accountId],
  );

  useEffect(() => {
    if (accountId && activeTab === "videos" && videoState.kind === "idle") {
      void loadVideos();
    }
  }, [accountId, activeTab, videoState, loadVideos]);

  const handleEditThumbnail = useCallback(
    async (item: ContentItem) => {
      if (!accountId) return;
      try {
        // Workspace lookup is the only thing that stays inline —
        // there's no shared workspaces hook today, and the
        // empty-workspaces fallback is a Calendar-specific UX call.
        const wsResp = await authedFetch("/api/v1/workspaces");
        const { workspaces } = (await wsResp.json()) as { workspaces: { id: number }[] };
        if (!workspaces.length) {
          toast.error("No workspaces found. Create one first.");
          return;
        }
        // Canonical create+open entrypoint — mirrors AccountDetails
        // exactly so the "Modifica copertina" UX is consistent
        // across the app; the response shape (full session record)
        // lives in the editorSessionsApi client.
        await createEditorSessionAndOpen({
          workspace_id: workspaces[0].id,
          platform_account_id: Number(accountId),
          youtube_video_id: item.external_id,
        });
        toast.success("Editor session created — opening Velox…");
      } catch (err) {
        if (err instanceof AuthError) return;
      }
    },
    [accountId, toast],
  );

  const statusOptions = [
    { value: "all", label: "Tutti gli stati" },
    { value: "draft", label: "Bozza" },
    { value: "queued", label: "Programmato" },
    { value: "publishing", label: "In pubblicazione" },
    { value: "published", label: "Pubblicato" },
    { value: "failed", label: "Fallito" },
  ];

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
              <Plus size={16} /> Nuovo post
            </Link>
          </div>
        </div>

        {/* Tabs when account_id is present */}
        {accountId && (
          <div className="flex items-center gap-1 mb-4 shrink-0">
            {([
              { id: "calendar" as const, label: "Calendario", icon: CalendarIcon },
              { id: "videos" as const, label: "Video Privati", icon: Video },
            ]).map((tab) => {
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

        {/* Toolbar — only show on calendar tab */}
        {(!accountId || activeTab === "calendar") && (
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between mb-4 shrink-0">
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => shiftDate(-1)}
                className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
                aria-label="Precedente"
              >
                <ChevronLeft size={18} />
              </button>
              <button
                type="button"
                onClick={() => setCurrentDate(new Date())}
                className="px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
              >
                Oggi
              </button>
              <button
                type="button"
                onClick={() => shiftDate(1)}
                className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
                aria-label="Successivo"
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

              <div className="flex items-center gap-2">
                <select
                  data-testid="calendar-filter-status"
                  value={statusFilter}
                  onChange={(e) => setStatusFilter(e.target.value)}
                  className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-white focus:outline-none focus:border-white/[0.20]"
                  aria-label="Filtra per stato"
                >
                  {statusOptions.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {state.kind === "ready" && state.workspaces.length > 0 && (
                  <select
                    data-testid="calendar-filter-workspace"
                    value={workspaceFilter}
                    onChange={(e) => setWorkspaceFilter(e.target.value)}
                    className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-white focus:outline-none focus:border-white/[0.20]"
                    aria-label="Filtra per workspace"
                  >
                    <option value="all">Tutti i workspace</option>
                    {state.workspaces.map((w) => (
                      <option key={w.id} value={w.id}>
                        {w.name}
                      </option>
                    ))}
                  </select>
                )}
                {hasActiveFilters && (
                  <button
                    type="button"
                    data-testid="calendar-filter-clear"
                    onClick={clearFilters}
                    className="inline-flex items-center gap-1.5 px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-[#9aa0aa] hover:text-white hover:bg-white/[0.08] transition-colors"
                    aria-label="Cancella filtri"
                  >
                    <X size={14} /> Cancella
                  </button>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Calendar surface */}
        {(!accountId || activeTab === "calendar") && (
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
        )}

        {/* Private Videos surface */}
        {accountId && activeTab === "videos" && (
          <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-4 sm:p-6 flex-1 min-h-0 flex flex-col overflow-y-auto">
            {videoState.kind === "loading" && (
              <div className="flex-1 flex flex-col gap-3">
                {Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} variant="card" height={72} />
                ))}
              </div>
            )}

            {videoState.kind === "error" && (
              <ErrorState
                title="Couldn't load videos"
                message={videoState.message}
                onRetry={() => void loadVideos()}
                className="bg-[#1f1f2e] border-white/[0.12]"
              />
            )}

            {videoState.kind === "ready" && videoState.items.length === 0 && (
              <EmptyState
                title="Nessun video privato"
                description="Non ci sono video privati per questo canale."
                icon={<Video size={32} />}
                className="bg-[#1f1f2e] border-white/[0.12]"
              />
            )}

            {videoState.kind === "ready" && (
              <div className="flex flex-col gap-2">
                {videoState.items.map((item) => (
                  <div
                    key={item.external_id}
                    className="flex gap-4 p-3 rounded-xl bg-white/[0.03] border border-white/[0.06] hover:bg-white/[0.06] transition-colors"
                  >
                    <div className="w-40 h-24 rounded-lg bg-white/[0.08] overflow-hidden shrink-0 relative">
                      {item.thumbnail_url ? (
                        <img
                          src={item.thumbnail_url}
                          alt={item.title ?? ""}
                          className="w-full h-full object-cover"
                        />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center">
                          <Video size={20} className="text-white/20" />
                        </div>
                      )}
                      {item.duration && (
                        <span className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded bg-black/70 text-[10px] text-white font-medium">
                          {item.duration}
                        </span>
                      )}
                    </div>
                    <div className="flex flex-col justify-between min-w-0 flex-1 py-0.5">
                      <div>
                        <p className="text-[13px] font-semibold text-white truncate">
                          {item.title}
                        </p>
                        <p className="text-[11px] text-[#9aa0aa] truncate mt-0.5">
                          {item.external_id}
                        </p>
                      </div>
                      <div className="flex items-center gap-3 text-[11px] text-[#9aa0aa]">
                        {item.published_at && (
                          <span>{new Date(item.published_at).toLocaleDateString()}</span>
                        )}
                        {item.privacy && (
                          <span className="capitalize">{item.privacy}</span>
                        )}
                      </div>
                    </div>
                    <div className="flex flex-col items-end justify-center gap-2 shrink-0">
                      <a
                        href={item.public_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-white/[0.06] border border-white/[0.08] text-[11px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors no-underline"
                      >
                        YouTube <ExternalLink size={12} />
                      </a>
                      <button
                        type="button"
                        onClick={() => void handleEditThumbnail(item)}
                        className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-blue-500/10 border border-blue-500/20 text-[11px] font-semibold text-blue-400 hover:bg-blue-500/20 hover:text-blue-300 transition-colors"
                      >
                        Modifica copertina
                      </button>
                    </div>
                  </div>
                ))}
                {videoState.nextCursor && (
                  <button
                    type="button"
                    onClick={() => void loadVideos(videoState.nextCursor)}
                    disabled={videoState.isLoadingMore}
                    className="mt-2 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-[#9aa0aa] hover:text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50"
                  >
                    {videoState.isLoadingMore ? (
                      <span className="flex items-center gap-2"><Loader2 size={14} className="animate-spin" /> Caricamento…</span>
                    ) : (
                      "Carica altri video"
                    )}
                  </button>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
