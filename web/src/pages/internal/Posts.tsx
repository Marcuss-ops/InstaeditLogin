import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  FileText,
  Filter,
  RefreshCw,
  Sparkles,
  CalendarClock,
  Send,
  XCircle,
  RotateCcw,
  Trash2,
  ChevronDown,
} from "lucide-react";
import { authedFetch, ApiError, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState, EmptyState } from "../../components/feedback";
import { cn } from "../../lib/utils";

type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
};

type Workspace = {
  id: number;
  name: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "empty" }
  | { kind: "ready"; posts: Post[] }
  | { kind: "error"; message: string };

type StatusFilter = "all" | "draft" | "queued" | "publishing" | "published" | "failed";

const STATUS_META: Record<string, { label: string; color: string; ring: string }> = {
  draft: { label: "Draft", color: "bg-neutral-100 text-neutral-700", ring: "ring-neutral-200" },
  queued: { label: "Scheduled", color: "bg-amber-50 text-amber-700", ring: "ring-amber-200" },
  publishing: { label: "Publishing", color: "bg-blue-50 text-blue-700", ring: "ring-blue-200" },
  published: { label: "Published", color: "bg-green-50 text-green-700", ring: "ring-green-200" },
  failed: { label: "Failed", color: "bg-red-50 text-red-700", ring: "ring-red-200" },
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

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function captionPreview(s: string | undefined): string {
  if (!s) return "—";
  const stripped = s.replace(/\s+/g, " ").trim();
  return stripped.length > 120 ? stripped.slice(0, 120) + "…" : stripped;
}

export function InternalPosts() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [busyId, setBusyId] = useState<number | null>(null);
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
      const posts = postsData.posts ?? [];
      setWorkspaces(wsData.workspaces ?? []);
      setState(posts.length === 0 ? { kind: "empty" } : { kind: "ready", posts });
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
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void loadAll();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAll, navigate]);

  const filtered =
    state.kind === "ready"
      ? state.posts.filter((p) => (statusFilter === "all" ? true : p.status === statusFilter))
      : [];

  const runAction = async (post: Post, method: string, endpoint: string) => {
    setBusyId(post.id);
    try {
      await authedFetch(endpoint, { method });
      await loadAll();
    } catch (err) {
      // errors are toasted by authedFetch
    } finally {
      setBusyId(null);
    }
  };

  const handlePublish = (post: Post) => runAction(post, "POST", `/api/v1/posts/${post.id}/publish`);
  const handleCancel = (post: Post) => runAction(post, "POST", `/api/v1/posts/${post.id}/cancel`);
  const handleRetry = (post: Post) => runAction(post, "POST", `/api/v1/posts/${post.id}/retry`);
  const handleDelete = (post: Post) => {
    if (!window.confirm(`Delete post #${post.id}? This cannot be undone.`)) return;
    void runAction(post, "DELETE", `/api/v1/posts/${post.id}`);
  };

  const workspaceName = (id: number) => workspaces.find((w) => w.id === id)?.name;

  return (
    <div className="min-h-full p-8">
      <div className="max-w-5xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-black flex items-center gap-3">
              <FileText size={28} className="text-neutral-400" />
              Posts
            </h1>
            <p className="text-[15px] text-neutral-500 mt-1">
              Drafts, scheduled posts, and publishing history.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void loadAll()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white border border-neutral-200 text-[13px] font-semibold text-neutral-700 hover:border-neutral-400 transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
            <Link
              to="/app/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
            >
              <Sparkles size={14} /> New post
            </Link>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3 mb-6 pb-4 border-b border-neutral-200">
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
        </div>

        {state.kind === "loading" && (
          <div className="grid gap-3">
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState title="Couldn't load posts" message={state.message} onRetry={() => void loadAll()} />
        )}

        {state.kind === "empty" && (
          <EmptyState
            title="No posts yet"
            description="Compose your first post and publish to a connected account."
            icon={<FileText size={32} />}
            cta={
              <Link
                to="/app/compose"
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
              >
                <Sparkles size={14} /> Create post
              </Link>
            }
          />
        )}

        {state.kind === "ready" && filtered.length === 0 && (
          <div className="bg-white border border-dashed border-neutral-300 rounded-xl p-10 text-center">
            <p className="text-[14px] text-neutral-500 mb-2">No posts match the current filters.</p>
            <button
              type="button"
              onClick={() => setStatusFilter("all")}
              className="text-[13px] text-black underline hover:no-underline"
            >
              Clear filters
            </button>
          </div>
        )}

        {state.kind === "ready" && filtered.length > 0 && (
          <div className="grid gap-3">
            {filtered.map((post) => (
              <PostRow
                key={post.id}
                post={post}
                workspaceName={workspaceName(post.workspace_id)}
                busy={busyId === post.id}
                onPublish={handlePublish}
                onCancel={handleCancel}
                onRetry={handleRetry}
                onDelete={handleDelete}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function PostRow({
  post,
  workspaceName,
  busy,
  onPublish,
  onCancel,
  onRetry,
  onDelete,
}: {
  post: Post;
  workspaceName: string | undefined;
  busy: boolean;
  onPublish: (p: Post) => void;
  onCancel: (p: Post) => void;
  onRetry: (p: Post) => void;
  onDelete: (p: Post) => void;
}) {
  const [open, setOpen] = useState(false);
  const canPublish = post.status === "draft" || post.status === "published" || post.status === "failed";
  const canCancel = post.status === "queued";
  const canRetry = post.status === "failed";

  return (
    <div className="bg-white border border-neutral-200 rounded-2xl p-5 hover:border-neutral-400 hover:shadow-[0_4px_16px_rgba(0,0,0,0.04)] transition-all">
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
              <p className="text-[13px] text-neutral-500 mt-1 break-words">{captionPreview(post.caption)}</p>
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
            <span className="opacity-70">Created {formatDate(post.created_at)}</span>
          </div>

          <div className="flex items-center justify-between mt-4 pt-3 border-t border-neutral-100">
            <div className="text-[11px] text-neutral-400">Manage this post.</div>
            <div className="relative">
              <button
                type="button"
                onClick={() => setOpen((v) => !v)}
                disabled={busy}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-neutral-100 hover:bg-neutral-200 text-[12px] font-medium text-neutral-700 transition-colors disabled:opacity-50"
              >
                Actions <ChevronDown size={12} />
              </button>
              {open && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
                  <div className="absolute right-0 mt-1 w-48 bg-white border border-neutral-200 rounded-xl shadow-lg z-20 py-1 text-[13px]">
                    {canPublish && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onPublish(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <Send size={13} /> Publish now
                      </button>
                    )}
                    {canCancel && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onCancel(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <XCircle size={13} /> Cancel schedule
                      </button>
                    )}
                    {canRetry && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          onRetry(post);
                          setOpen(false);
                        }}
                        className="w-full text-left px-3 py-2 hover:bg-neutral-50 inline-flex items-center gap-2 disabled:opacity-50"
                      >
                        <RotateCcw size={13} /> Retry
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
                    >
                      <Trash2 size={13} /> Delete
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
