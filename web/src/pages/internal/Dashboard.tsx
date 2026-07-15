import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  LayoutDashboard,
  FileText,
  Link2,
  Sparkles,
  ArrowRight,
  RefreshCw,
  CheckCircle2,
  Clock,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { getProvider, type ProviderId } from "../../lib/providers";
import { Skeleton, ErrorState } from "../../components/feedback";

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  username: string;
  created_at: string;
};

type Post = {
  id: number;
  status: string;
  scheduled_at?: string | null;
};

type DashboardData = {
  accounts: PlatformAccount[];
  posts: Post[];
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: DashboardData }
  | { kind: "error"; message: string };

function StatCard({
  label,
  value,
  icon: Icon,
  to,
}: {
  label: string;
  value: number;
  icon: React.ElementType;
  to: string;
}) {
  return (
    <Link
      to={to}
      className="group bg-white border border-neutral-200 rounded-2xl p-5 hover:border-neutral-400 hover:shadow-[0_8px_24px_rgba(0,0,0,0.05)] transition-all no-underline"
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-neutral-500 mb-1">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-black">{value}</p>
        </div>
        <div className="w-10 h-10 rounded-xl bg-neutral-100 flex items-center justify-center text-neutral-600 group-hover:bg-black group-hover:text-white transition-colors">
          <Icon size={20} />
        </div>
      </div>
    </Link>
  );
}

export function InternalDashboard() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const [accountsResp, postsResp] = await Promise.all([
        authedFetch("/api/v1/accounts", { signal: controller.signal }),
        authedFetch("/api/v1/posts", { signal: controller.signal }),
      ]);
      if (controller.signal.aborted) return;
      const accountsData = (await accountsResp.json()) as { accounts: PlatformAccount[] };
      const postsData = (await postsResp.json()) as { posts: Post[] };
      setState({
        kind: "ready",
        data: {
          accounts: accountsData.accounts ?? [],
          posts: postsData.posts ?? [],
        },
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load dashboard.";
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
      void load();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [load, navigate]);

  const stats =
    state.kind === "ready"
      ? {
          connected: state.data.accounts.length,
          posts: state.data.posts.length,
          published: state.data.posts.filter((p) => p.status === "published").length,
          scheduled: state.data.posts.filter((p) => p.status === "queued").length,
        }
      : null;

  return (
    <div className="min-h-full p-8">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-black flex items-center gap-3">
              <LayoutDashboard size={28} className="text-neutral-400" />
              Dashboard
            </h1>
            <p className="text-[15px] text-neutral-500 mt-1">
              Overview of your connected accounts and publishing activity.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white border border-neutral-200 text-[13px] font-semibold text-neutral-700 hover:border-neutral-400 transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
            <Link
              to="/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
            >
              <Sparkles size={14} /> New post
            </Link>
          </div>
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load dashboard"
            message={state.message}
            onRetry={() => void load()}
            className="mb-8"
          />
        )}

        {state.kind === "ready" && stats && (
          <>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
              <StatCard label="Connected accounts" value={stats.connected} icon={Link2} to="/app/linking" />
              <StatCard label="Total posts" value={stats.posts} icon={FileText} to="/app/posts" />
              <StatCard label="Published" value={stats.published} icon={CheckCircle2} to="/app/posts" />
              <StatCard label="Scheduled" value={stats.scheduled} icon={Clock} to="/app/posts" />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              <div className="lg:col-span-2 bg-white border border-neutral-200 rounded-2xl p-6">
                <div className="flex items-center justify-between mb-4">
                  <h2 className="text-[16px] font-bold text-black">Connected accounts</h2>
                  <Link
                    to="/app/linking"
                    className="inline-flex items-center gap-1 text-[13px] font-medium text-neutral-500 hover:text-black transition-colors no-underline"
                  >
                    Manage <ArrowRight size={14} />
                  </Link>
                </div>
                {state.data.accounts.length === 0 ? (
                  <p className="text-[14px] text-neutral-500">No accounts connected yet.</p>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {state.data.accounts.map((account) => {
                      const provider = getProvider(account.platform);
                      if (!provider) return null;
                      return (
                        <div
                          key={account.id}
                          className="flex items-center gap-3 p-3 rounded-xl border border-neutral-100 bg-neutral-50/50"
                        >
                          <div
                            className={`w-10 h-10 rounded-xl bg-gradient-to-br ${provider.color} flex items-center justify-center text-white shrink-0`}
                          >
                            {provider.icon}
                          </div>
                          <div className="min-w-0">
                            <p className="text-[13px] font-semibold text-black truncate">
                              {provider.name}
                            </p>
                            <p className="text-[12px] text-neutral-500 truncate">
                              @{account.username || "—"}
                            </p>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              <div className="bg-white border border-neutral-200 rounded-2xl p-6">
                <h2 className="text-[16px] font-bold text-black mb-4">Quick actions</h2>
                <div className="space-y-2">
                  <Link
                    to="/compose"
                    className="flex items-center gap-3 p-3 rounded-xl bg-neutral-50 hover:bg-neutral-100 transition-colors no-underline text-black"
                  >
                    <Sparkles size={18} className="text-neutral-500" />
                    <span className="text-[14px] font-medium">Create a post</span>
                  </Link>
                  <Link
                    to="/app/linking"
                    className="flex items-center gap-3 p-3 rounded-xl bg-neutral-50 hover:bg-neutral-100 transition-colors no-underline text-black"
                  >
                    <Link2 size={18} className="text-neutral-500" />
                    <span className="text-[14px] font-medium">Connect accounts</span>
                  </Link>
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
