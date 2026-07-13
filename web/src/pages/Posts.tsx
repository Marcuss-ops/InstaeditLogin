import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  CalendarClock,
  ChevronDown,
  CircleDashed,
  FileText,
  Filter,
  RefreshCw,
  Sparkles,
  Trash2,
  XCircle,
  RotateCcw,
  Send,
} from "lucide-react";
import { Nav } from "../components/Nav";
import { authedFetch, ApiError, AuthError } from "../lib/auth";
import { cn } from "../lib/utils";

type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  media_url?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
  updated_at: string;
};

type Workspace = {
  id: number;
  name: string;
  owner_id: number;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "empty" }
  | { kind: "ready"; posts: Post[] }
  | { kind: "error"; message: string };

type StatusFilter = "all" | "draft" | "queued" | "publishing" | "published" | "failed";
type WorkspaceFilter = "all" | number;

const STATUS_META: Record<string, { label: string; color: string; ring: string }> = {
  draft: { label: "Draft", color: "bg-neutral-100 text-neutral-700", ring: "ring-neutral-200" },
  queued: { label: "Scheduled", color: "bg-amber-50 text-amber-700", ring: "ring-amber-200" },
  publishing: { label: "Publishing", color: "bg-blue-50 text-blue-700", ring: "ring-blue-200" },
  published: { label: "Published", color: "bg-green-50 text-green-700", ring: "ring-green-200" },
  partially_published: {
    label: "Partial",
    color: "bg-amber-50 text-amber-700",
    ring: "ring-amber-200",
  },
  failed: { label: "Failed", color: "bg-red-50 text-red-700", ring: "ring-red-200" },
  waiting_provider: { label: "Waiting", color: "bg-neutral-100 text-neutral-700", ring: "ring-neutral-200" },
  retrying: { label: "Retrying", color: "bg-amber-50 text-amber-700", ring: "ring-amber-200" },
  dlq: { label: "Dead-letter", color: "bg-red-50 text-red-700", ring: "ring-red-200" },
};

function StatusBadge({ status }: { status: string }) {
  const meta = STATUS_META[status] ?? { label: status, color: "bg-neutral-100 text-neutral-700", ring: "ring-neutral-200" };
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-semibold ring-1",
        meta.color,
        meta.ring,
      )}
    >
      <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
      {meta.label}
    </span>
  );
}

function formatDate(iso: string | null | undefined, withTime = true): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    ...(withTime ? { hour: "2-digit", minute: "2-digit" } : {}),
  });
}

function captionPreview(s: string | undefined): string {
  if (!s) return "—";
  const stripped = s.replace(/\s+/g, " ").trim();
  return stripped.length > 90 ? stripped.slice(0, 90) + "…" : stripped;
}

function PostRow({
  post,
  workspaceName,
  onPublish,
  onCancel,
  onRetry,
  onDelete,
  busy,
}: {
  post: Post;
  workspaceName: string | undefined;
  onPublish: (p: Post) => void;
  onCancel: (p: Post) => void;
  onRetry: (p: Post) => void;
  onDelete: (p: Post) => void;
  busy: boolean;
}) {
  const [open, setOpen] = useState(false);
  const publishedOrFailed = post.status === "published" || post.status === "failed";
  const scheduled = post.status === "queued";
  const canRepublish = post.status === "draft" || publishedOrFailed;

  return (
    <div className="bg-white border border-neutral-200 rounded-xl p-5 hover:border-neutral-400 hover:shadow-[0_4px_16px_rgba(0,0,0,0.04)] transition-all">
      <div className="flex items-start gap-4">
        <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-emerald-500 to-teal-500 flex items-center justify-center text-white shrink-0">
          <FileText size={18} />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-start justify-between gap-3">
            <div className="flex-1 min-w-0">
              <h3 className="font-bold text-[15px] text-black truncate">
                {post.title || <span className="text-neutral-400 font-normal italic">Untitled</span>}
              </h3>
              <p className="text-[13px] text-neutral-500 mt-1 break-words">
                {captionPreview(post.caption)}
              </p>
            </div>
            <StatusBadge status={post.status} />
          </div>

          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 mt-3 text-[11px] text-neutral-500">
            <span className="inline-flex items-center gap-1.5">
              <span className="font-mono">#{post.id}</span>
              <span className="opacity-50">·</span>
              <span>{workspaceName ?? `Workspace ${post.workspace_id}`}</span>
            </span>
            {post.scheduled_at && (
              <span className="inline-flex items-center gap-1">
                <CalendarClock size={11} />
                {formatDate(post.scheduled_at)}
              </span>
            )}
            <span className="opacity-70">Created {formatDate(post.created_at, false)}</span>
          </div>

          {/* Action menu */}
          <div className="flex items-center justify-between mt-4 pt-3 border-t border-neutral-100">
            <div className="text-[11px] text-neutral-400">Tap an action to manage this post.</div>
            <div className="relative">
              <button
                type="button"
                onClick={() => setOpen((v) => !v)}
                disabled={busy}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-neutral-100 hover:bg-neutral-200 text-[12px] font-medium text-neutral-700 transition-colors disabled:opacity-50"
                aria-haspopup="menu"
                aria-expanded={open}
                data-testid={`post-actions-${post.id}`}
              >
                Actions <ChevronDown size={12} />
              </button>
              {open && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
                  <div
                    role="menu"
                    className="absolute right-0 mt-1 w-48 bg-white border border-neutral-200 rounded-xl shadow-lg z-20 py-1 text-[13px]"
                  >
                    {canRepublish && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onPublish(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <Send size={13} />
                        Publish now
                      </button>
                    )}
                    {scheduled && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onCancel(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <XCircle size={13} />
                        Cancel schedule
                      </button>
                    )}
                    {post.status === "failed" && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onRetry(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <RotateCcw size={13} />
                        Retry
                      </button>
                    )}
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => {
                        onDelete(post);
                        setOpen(false);
                      }}
                      className="w-full text-left px-3 py-2 hover:bg-red-50 text-red-600 inline-flex items-center gap-2 disabled:opacity-50"
                      data-testid={`post-delete-${post.id}`}
                    >
                      <Trash2 size={13} />
                      Delete
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export function Posts() {
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [workspaceFilter, setWorkspaceFilter] = useState<WorkspaceFilter>("all");
  const [busyId, setBusyId] = useState<number | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const loadAll = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const [postsResp, wsResp] = await Promise.all([
        authedFetch("/api/v1/posts", { signal: controller.signal }),
        authedFetch("/api/v1/workspaces", { signal: controller.signal }),
      ]);
      if (controller.signal.aborted) return;
      const postsData = (await postsResp.json()) as { posts: Post[] };
      const wsData = (await wsResp.json()) as { workspaces: Workspace[] };
      setWorkspaces(wsData.workspaces ?? []);
      setState({
        kind: postsData.posts && postsData.posts.length > 0 ? "ready" : "empty",
        posts: postsData.posts ?? [],
      } as FetchState);
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) return;
      if (err instanceof DOMException && err.name === "AbortError") return;
      const message = err instanceof ApiError ? err.message : "Unable to load posts.";
      setState({ kind: "error", message });
    }
  }, []);

  useEffect(() => {
    void loadAll();
    return () => abortRef.current?.abort();
  }, [loadAll]);

  const filtered =
    state.kind === "ready"
      ? state.posts.filter((p) => {
          if (statusFilter !== "all" && p.status !== statusFilter) return false;
          if (workspaceFilter !== "all" && p.workspace_id !== workspaceFilter) return false;
          return true;
        })
      : [];

  const showToast = (msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 2500);
  };

  const runAction = async (post: Post, fn: () => Promise<Response>) => {
    setBusyId(post.id);
    try {
      const resp = await fn();
      if (!resp.ok) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        showToast(`Action failed: ${data.error ?? resp.statusText}`);
        return;
      }
      showToast("Action applied.");
      await loadAll();
    } catch (err) {
      showToast(
        err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Action failed.",
      );
    } finally {
      setBusyId(null);
    }
  };

  const handlePublish = (post: Post) =>
    runAction(post, () =>
      authedFetch(`/api/v1/posts/${post.id}/publish`, { method: "POST" }),
    );
  const handleCancel = (post: Post) =>
    runAction(post, () =>
      authedFetch(`/api/v1/posts/${post.id}/cancel`, { method: "POST" }),
    );
  const handleRetry = (post: Post) =>
    runAction(post, () =>
      authedFetch(`/api/v1/posts/${post.id}/retry`, { method: "POST" }),
    );
  const handleDelete = (post: Post) => {
    if (!window.confirm(`Delete post #${post.id}? This cannot be undone.`)) return;
    void runAction(post, () => authedFetch(`/api/v1/posts/${post.id}`, { method: "DELETE" }));
  };

  const workspaceName = (id: number) => workspaces.find((w) => w.id === id)?.name;

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        <div className="flex flex-col items-center justify-center py-8">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-emerald-500 to-teal-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(16,185,129,0.25)]">
            <FileText size={26} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,38px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            Posts
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[480px]">
            Drafts, scheduled posts, and the publishing history of every workspace you own.
          </p>
          <div className="mt-5 inline-flex items-center gap-2">
            <Link
              to="/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
            >
              <Sparkles size={13} /> New post
            </Link>
          </div>
        </div>

        {/* Filter bar */}
        <div className="flex flex-wrap items-center gap-3 mb-5 pb-4 border-b border-neutral-200">
          <div className="inline-flex items-center gap-2 text-[12px] text-neutral-500">
            <Filter size={13} /> Filters
          </div>
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
            className="px-3 py-1.5 rounded-lg bg-white border border-neutral-200 text-[13px] font-medium text-neutral-700 focus:outline-none focus:ring-2 focus:ring-black/10"
            aria-label="Filter by status"
          >
            <option value="all">All statuses</option>
            <option value="draft">Drafts</option>
            <option value="queued">Scheduled</option>
            <option value="publishing">Publishing</option>
            <option value="published">Published</option>
            <option value="failed">Failed</option>
          </select>
          <select
            value={String(workspaceFilter)}
            onChange={(e) =>
              setWorkspaceFilter(e.target.value === "all" ? "all" : Number(e.target.value))
            }
            className="px-3 py-1.5 rounded-lg bg-white border border-neutral-200 text-[13px] font-medium text-neutral-700 focus:outline-none focus:ring-2 focus:ring-black/10"
            aria-label="Filter by workspace"
          >
            <option value="all">All workspaces</option>
            {workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => void loadAll()}
            className="ml-auto inline-flex items-center gap-1.5 text-[13px] text-neutral-500 hover:text-black transition-colors"
          >
            <RefreshCw size={13} />
            Refresh
          </button>
        </div>

        {/* Toast */}
        {toast && (
          <div
            role="status"
            className="fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-xl bg-black text-white text-[13px] shadow-lg animate-[fadeUp_0.3s_ease-out]"
          >
            {toast}
          </div>
        )}

        <section className="pb-12">
          {state.kind === "loading" && (
            <div className="flex flex-col items-center justify-center py-20 gap-3">
              <div className="w-10 h-10 rounded-full border-4 border-neutral-200 border-t-black animate-spin" />
              <p className="text-[14px] text-neutral-500">Loading posts…</p>
            </div>
          )}

          {state.kind === "error" && (
            <div className="bg-white border border-neutral-200 rounded-xl p-8 text-center">
              <p className="text-red-500 font-semibold text-[15px] mb-1">Couldn't load posts</p>
              <p className="text-[14px] text-neutral-500 mb-5">{state.message}</p>
              <button
                type="button"
                onClick={() => void loadAll()}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
              >
                <RefreshCw size={14} /> Retry
              </button>
            </div>
          )}

          {state.kind === "empty" && (
            <div className="bg-white border border-dashed border-neutral-300 rounded-xl p-12 text-center">
              <CircleDashed size={32} className="mx-auto text-neutral-300 mb-3" />
              <h3 className="font-bold text-[16px] text-black mb-1">No posts yet</h3>
              <p className="text-[14px] text-neutral-500 mb-5">
                Compose your first post and publish to a connected account.
              </p>
              <Link
                to="/compose"
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
              >
                <Sparkles size={14} /> Open composer
              </Link>
            </div>
          )}

          {state.kind === "ready" && filtered.length === 0 && (
            <div className="bg-white border border-dashed border-neutral-300 rounded-xl p-10 text-center">
              <p className="text-[14px] text-neutral-500 mb-2">No posts match the current filters.</p>
              <button
                type="button"
                onClick={() => {
                  setStatusFilter("all");
                  setWorkspaceFilter("all");
                }}
                className="text-[13px] text-black underline hover:no-underline"
              >
                Clear filters
              </button>
            </div>
          )}

          {state.kind === "ready" && filtered.length > 0 && (
            <div className="grid gap-3" data-testid="posts-list">
              {filtered.map((post) => (
                <PostRow
                  key={post.id}
                  post={post}
                  workspaceName={workspaceName(post.workspace_id)}
                  onPublish={handlePublish}
                  onCancel={handleCancel}
                  onRetry={handleRetry}
                  onDelete={handleDelete}
                  busy={busyId === post.id}
                />
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
