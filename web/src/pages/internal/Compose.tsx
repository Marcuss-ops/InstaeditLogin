import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { PenSquare, Send, Save, AlertCircle, ArrowLeft } from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import { getProvider } from "../../lib/providers";
import { cn } from "../../lib/utils";

type Workspace = {
  id: number;
  name: string;
};

type PlatformAccount = {
  id: number;
  platform: string;
  username: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; workspaces: Workspace[]; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

export function InternalCompose() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const [workspaceId, setWorkspaceId] = useState<number | "">("");
  const [title, setTitle] = useState("");
  const [caption, setCaption] = useState("");
  const [scheduledAt, setScheduledAt] = useState("");
  const [status, setStatus] = useState<"draft" | "queued">("draft");
  const [selectedAccounts, setSelectedAccounts] = useState<Set<number>>(new Set());

  const loadData = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const [wsResp, accResp] = await Promise.all([
        authedFetch("/api/v1/workspaces", { signal: controller.signal }),
        authedFetch("/api/v1/accounts", { signal: controller.signal }),
      ]);
      if (controller.signal.aborted) return;

      const wsData = (await wsResp.json()) as { workspaces: Workspace[] };
      const accData = (await accResp.json()) as { accounts: PlatformAccount[] };
      const workspaces = wsData.workspaces ?? [];
      const accounts = accData.accounts ?? [];

      setState({ kind: "ready", workspaces, accounts });
      if (workspaces.length === 1) {
        setWorkspaceId(workspaces[0].id);
      }
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load compose dependencies.";
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
      void loadData();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadData, navigate]);

  const toggleAccount = (id: number) => {
    setSelectedAccounts((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (workspaceId === "" || selectedAccounts.size === 0 || !title.trim()) return;

    setIsSubmitting(true);
    try {
      const payload = {
        workspace_id: Number(workspaceId),
        content: { title: title.trim(), caption: caption.trim() || undefined },
        status,
        scheduled_at: scheduledAt ? new Date(scheduledAt).toISOString() : undefined,
        targets: Array.from(selectedAccounts).map((id) => ({ platform_account_id: id })),
      };

      await authedFetch("/api/v1/posts", {
        method: "POST",
        body: JSON.stringify(payload),
      });

      navigate("/app/posts");
    } catch {
      // errors are toasted by authedFetch
      setIsSubmitting(false);
    }
  };

  if (state.kind === "loading") {
    return (
      <div className="min-h-full p-8">
        <div className="max-w-3xl mx-auto grid gap-6">
          <Skeleton variant="card" height={80} />
          <Skeleton variant="card" height={400} />
        </div>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="min-h-full p-8">
        <div className="max-w-3xl mx-auto">
          <ErrorState title="Couldn't load compose" message={state.message} onRetry={() => void loadData()} />
        </div>
      </div>
    );
  }

  const { workspaces, accounts } = state;
  const isFormValid = workspaceId !== "" && title.trim() !== "" && selectedAccounts.size > 0;

  return (
    <div className="min-h-full p-8">
      <div className="max-w-3xl mx-auto">
        <Link
          to="/app/posts"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-neutral-500 hover:text-black transition-colors no-underline mb-4"
        >
          <ArrowLeft size={14} /> Back to posts
        </Link>

        <div className="mb-8">
          <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-black flex items-center gap-3">
            <PenSquare size={28} className="text-neutral-400" />
            Compose
          </h1>
          <p className="text-[15px] text-neutral-500 mt-1">
            Create and schedule a post across your connected accounts.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-6">
          <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-sm space-y-6">
            <div className="space-y-4">
              <div>
                <label htmlFor="workspace" className="block text-[13px] font-semibold text-neutral-700 mb-1.5">
                  Workspace
                </label>
                <select
                  id="workspace"
                  value={workspaceId}
                  onChange={(e) => setWorkspaceId(e.target.value === "" ? "" : Number(e.target.value))}
                  className="w-full px-3 py-2 bg-white border border-neutral-200 rounded-xl text-[14px] text-black focus:outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 transition-all"
                >
                  <option value="" disabled>
                    Select a workspace...
                  </option>
                  {workspaces.map((ws) => (
                    <option key={ws.id} value={ws.id}>
                      {ws.name}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <span className="block text-[13px] font-semibold text-neutral-700 mb-1.5">Target accounts</span>
                {accounts.length === 0 ? (
                  <div className="text-[13px] text-red-600 flex items-center gap-2">
                    <AlertCircle size={14} /> You have no linked accounts. Please link one first.
                  </div>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {accounts.map((acc) => {
                      const selected = selectedAccounts.has(acc.id);
                      const provider = getProvider(acc.platform);
                      return (
                        <button
                          key={acc.id}
                          type="button"
                          onClick={() => toggleAccount(acc.id)}
                          className={cn(
                            "px-3 py-1.5 rounded-lg border text-[13px] font-medium transition-colors",
                            selected
                              ? "bg-black text-white border-black"
                              : "bg-white text-neutral-600 border-neutral-200 hover:border-neutral-400",
                          )}
                        >
                          {provider?.name ?? acc.platform} (@{acc.username})
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            <hr className="border-neutral-100" />

            <div className="space-y-4">
              <div>
                <label htmlFor="title" className="block text-[13px] font-semibold text-neutral-700 mb-1.5">
                  Title
                </label>
                <input
                  id="title"
                  type="text"
                  placeholder="Internal title for your reference"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  className="w-full px-3 py-2 bg-white border border-neutral-200 rounded-xl text-[14px] text-black focus:outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 transition-all"
                />
              </div>

              <div>
                <label htmlFor="caption" className="block text-[13px] font-semibold text-neutral-700 mb-1.5">
                  Caption
                </label>
                <textarea
                  id="caption"
                  rows={5}
                  placeholder="What's on your mind?"
                  value={caption}
                  onChange={(e) => setCaption(e.target.value)}
                  className="w-full px-3 py-2 bg-white border border-neutral-200 rounded-xl text-[14px] text-black focus:outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 transition-all resize-y"
                />
              </div>
            </div>

            <hr className="border-neutral-100" />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label htmlFor="scheduledAt" className="block text-[13px] font-semibold text-neutral-700 mb-1.5">
                  Schedule (optional)
                </label>
                <input
                  id="scheduledAt"
                  type="datetime-local"
                  value={scheduledAt}
                  onChange={(e) => {
                    setScheduledAt(e.target.value);
                    if (e.target.value) setStatus("queued");
                    else setStatus("draft");
                  }}
                  className="w-full px-3 py-2 bg-white border border-neutral-200 rounded-xl text-[14px] text-black focus:outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 transition-all"
                />
              </div>

              <div>
                <label htmlFor="status" className="block text-[13px] font-semibold text-neutral-700 mb-1.5">
                  Initial status
                </label>
                <select
                  id="status"
                  value={status}
                  onChange={(e) => setStatus(e.target.value as "draft" | "queued")}
                  className="w-full px-3 py-2 bg-white border border-neutral-200 rounded-xl text-[14px] text-black focus:outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 transition-all"
                >
                  <option value="draft">Save as draft</option>
                  <option value="queued">Queue for scheduling</option>
                </select>
              </div>
            </div>
          </div>

          <div className="flex items-center justify-end gap-3">
            <Link
              to="/app/posts"
              className="px-4 py-2 text-[14px] font-medium text-neutral-500 hover:text-black transition-colors no-underline"
            >
              Cancel
            </Link>
            <button
              type="submit"
              disabled={!isFormValid || isSubmitting}
              className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {status === "draft" ? <Save size={16} /> : <Send size={16} />}
              {status === "draft" ? "Save draft" : "Schedule post"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
