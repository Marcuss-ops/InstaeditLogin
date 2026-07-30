import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  CalendarClock,
  Video,
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

// UploadJob was once used by /uploads?status=pending&limit=200 fetch.
// After the rollout to /uploads/counts the dashboard reads per-account
// aggregates from `countMap` and never needs the row list. Removed the
// type to keep noUnusedLocals clean; reintroduce alongside any future
// "list pending uploads" surface.

type AccountProgrammatoCount = {
  count: number;
  nextAt: string | null;
};

type DashboardData = {
  accounts: PlatformAccount[];
  posts: Post[];
  // totalUploads is the DISTINCT count of pending upload_jobs from
  // /uploads/counts — multi-target rows count ONCE (instead of once
  // per target). This is the source for the "Pending uploads" stat.
  totalUploads: number;
  // Per-account pending count + earliest scheduled_at, derived from
  // GET /api/v1/uploads/counts. The dashboard widget renders from
  // this map; the calendar page hits /uploads/by-account separately.
  countMap: Map<number, AccountProgrammatoCount>;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; data: DashboardData }
  | { kind: "error"; message: string };

// Platforms whose accounts can RECEIVE posts. google-drive is excluded:
// it's a SOURCE (we read from it), not a destination. Showing it on the
// "Programmati" widget would surface an empty calendar because Drive
// accounts never appear in upload_jobs.targets.
const PUBLISHABLE_PLATFORMS = new Set<ProviderId>([
  "facebook",
  "instagram",
  "threads",
  "tiktok",
  "twitter",
  "youtube",
  "linkedin",
]);

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
      className="group surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5 hover:border-white/[0.24] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)] transition-all no-underline block"
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-white">{value}</p>
        </div>
        <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa] group-hover:bg-white group-hover:text-[#030308] transition-colors">
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
      const [accountsResp, postsResp, countsResp] = await Promise.all([
        authedFetch("/api/v1/accounts", { signal: controller.signal }),
        authedFetch("/api/v1/posts", { signal: controller.signal }),
        // /uploads/counts is the cheap GROUP BY per-target aggregate
        // (single query, no row cap). The widget only needs the per-account
        // N + next publish date, not the full upload list. The calendar
        // page that opens on click hits /uploads/by-account for the
        // per-day buckets. Avoids the O(200) payload the previous
        // /uploads?status=pending&limit=200 fetch required.
        authedFetch("/api/v1/uploads/counts", {
          signal: controller.signal,
        }),
      ]);
      if (controller.signal.aborted) return;
      const accountsData = (await accountsResp.json()) as { accounts: PlatformAccount[] };
      const postsData = (await postsResp.json()) as { posts: Post[] };
      const countsData = (await countsResp.json()) as {
        counts: Array<{
          account_id: number;
          count: number;
          next_publish_at: string | null;
        }>;
        total_uploads: number;
      };
      // Project the count-rollup into a Map<account_id, count + nextAt>
      // so the per-account widget can O(1)-look-up instead of doing an
      // inner N×M loop on a fetched upload list.
      const countMap = new Map<
        number,
        { count: number; nextAt: string | null }
      >();
      for (const c of countsData.counts ?? []) {
        countMap.set(c.account_id, {
          count: c.count,
          nextAt: c.next_publish_at ?? null,
        });
      }
      setState({
        kind: "ready",
        data: {
          accounts: accountsData.accounts ?? [],
          posts: postsData.posts ?? [],
          countMap,
          totalUploads: countsData.total_uploads ?? 0,
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
          // totalUploads comes from /uploads/counts (DISTINCT rows, not
          // per-target expansions) so multi-target uploads count once
          // even when the JSONB targets array fans out across accounts.
          queuedUploads: state.data.totalUploads,
        }
      : null;

  // Filter to publishable accounts (excludes google-drive) and sort by
  // pending count DESC so the most-active account surfaces first. The
  // countMap (sourced from /uploads/counts) is already an O(1) lookup,
  // so no nested loop — unlike the previous /uploads?limit=200 path.
  const programByAccount = useMemo(() => {
    if (state.kind !== "ready") return [] as Array<{
      account: PlatformAccount;
      count: number;
      nextAt: string | null;
    }>;
    return state.data.accounts
      .filter((a) => PUBLISHABLE_PLATFORMS.has(a.platform))
      .map((a) => {
        const bucket = state.kind === "ready"
          ? (state.data.countMap.get(a.id) ?? { count: 0, nextAt: null })
          : { count: 0, nextAt: null };
        return {
          account: a,
          count: bucket.count,
          nextAt: bucket.nextAt,
        };
      })
      .sort((a, b) => b.count - a.count || a.account.id - b.account.id);
  }, [state]);

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <LayoutDashboard size={28} className="text-white/40" />
              Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Overview of your connected accounts and publishing activity.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
            <Link
              to="/app/compose"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
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
            className="mb-8 bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && stats && (
          <>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
              <StatCard label="Connected accounts" value={stats.connected} icon={Link2} to="/app/linking" />
              <StatCard label="Total posts" value={stats.posts} icon={FileText} to="/app/posts" />
              <StatCard label="Published" value={stats.published} icon={CheckCircle2} to="/app/posts" />
              <StatCard label="Pending uploads" value={stats.queuedUploads} icon={Clock} to="/app/uploads/calendar" />
            </div>

            {programByAccount.length > 0 && (
              <section className="mb-8">
                <div className="flex items-center justify-between mb-4">
                  <div>
                    <h2 className="text-[18px] font-extrabold tracking-tight text-white flex items-center gap-2">
                      <CalendarClock size={20} className="text-white/60" />
                      Programmati
                    </h2>
                    <p className="text-[13px] text-[#9aa0aa] mt-0.5">
                      Scheduled uploads per account. Click an account to open
                      its calendar and reschedule by drag-and-drop.
                    </p>
                  </div>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                  {programByAccount.map((entry) => (
                    <AccountProgrammatoCard key={entry.account.id} entry={entry} />
                  ))}
                </div>
              </section>
            )}

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              <div className="lg:col-span-2 surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
                <div className="flex items-center justify-between mb-4">
                  <h2 className="text-[16px] font-bold text-white">Connected accounts</h2>
                  <Link
                    to="/app/linking"
                    className="inline-flex items-center gap-1 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline"
                  >
                    Manage <ArrowRight size={14} />
                  </Link>
                </div>
                {state.data.accounts.length === 0 ? (
                  <p className="text-[14px] text-[#9aa0aa]">No accounts connected yet.</p>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {state.data.accounts.map((account) => {
                      const provider = getProvider(account.platform);
                      if (!provider) return null;
                      return (
                        <div
                          key={account.id}
                          className="flex items-center gap-3 p-3 rounded-xl border border-white/[0.08] bg-white/[0.02]"
                        >
                          <div
                            className={`w-10 h-10 rounded-xl bg-gradient-to-br ${provider.color} flex items-center justify-center text-white shrink-0`}
                          >
                            {provider.icon}
                          </div>
                          <div className="min-w-0">
                            <p className="text-[13px] font-semibold text-white truncate">
                              {provider.name}
                            </p>
                            <p className="text-[12px] text-[#9aa0aa] truncate">
                              @{account.username || "—"}
                            </p>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
                <h2 className="text-[16px] font-bold text-white mb-4">Quick actions</h2>
                <div className="space-y-2">
                  <Link
                    to="/app/compose"
                    className="flex items-center gap-3 p-3 rounded-xl bg-white/[0.04] hover:bg-white/[0.08] border border-white/[0.06] transition-colors no-underline text-white"
                  >
                    <Sparkles size={18} className="text-[#9aa0aa]" />
                    <span className="text-[14px] font-medium">Create a post</span>
                  </Link>
                  <Link
                    to="/app/linking"
                    className="flex items-center gap-3 p-3 rounded-xl bg-white/[0.04] hover:bg-white/[0.08] border border-white/[0.06] transition-colors no-underline text-white"
                  >
                    <Link2 size={18} className="text-[#9aa0aa]" />
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

// AccountProgrammatoCard is the per-account "high bar" card on the
// dashboard. Visually:
//   ┌─────────────────────────────────────────┐
//   │ ╔═════════════════════════════════════╗ │   ← gradient high-bar
//   │ ║  N programmati                       ║ │     (purple/emerald)
//   │ ╚═════════════════════════════════════╝ │
//   │ [icon] Facebook                         │
//   │         @username                       │
//   │                                         │
//   │ Next: Wed 17 Apr, 14:30                 │
//   │ Apri calendario →                        │
//   └─────────────────────────────────────────┘
//
// The high-bar color shifts based on count: 0 = neutral, 1-3 = soft
// amber, 4+ = vivid emerald — gives at-a-glance reading of queue
// density without needing to read numbers.
function AccountProgrammatoCard({
  entry,
}: {
  entry: {
    account: PlatformAccount;
    count: number;
    nextAt: string | null;
  };
}) {
  const provider = getProvider(entry.account.platform);
  const barClasses =
    entry.count === 0
      ? "bg-white/[0.04] text-[#9aa0aa]"
      : entry.count < 4
        ? "bg-amber-500/[0.10] text-amber-300 border-b border-amber-500/[0.25]"
        : "bg-gradient-to-r from-emerald-500/[0.16] via-violet-500/[0.12] to-blue-500/[0.10] text-white border-b border-emerald-500/[0.30]";

  const nextLabel = useMemo(() => {
    if (!entry.nextAt) return null;
    const d = new Date(entry.nextAt);
    if (Number.isNaN(d.getTime())) return null;
    return d.toLocaleString(undefined, {
      weekday: "short",
      day: "numeric",
      month: "short",
      hour: "2-digit",
      minute: "2-digit",
    });
  }, [entry.nextAt]);

  return (
    <Link
      to={`/app/uploads/calendar?account_id=${entry.account.id}`}
      className="group block surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl overflow-hidden hover:border-white/[0.30] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)] transition-all no-underline"
      data-testid={`dash-programmati-card-${entry.account.id}`}
    >
      <div className={barClasses + " px-5 py-3"}>
        <div className="flex items-center justify-between">
          <span className="text-[12px] font-bold uppercase tracking-wider">
            Programmati
          </span>
          <span className="text-[20px] font-extrabold tabular-nums">
            {entry.count}
          </span>
        </div>
      </div>
      <div className="px-5 py-4">
        <div className="flex items-center gap-3 mb-3">
          {provider ? (
            <div
              className={`w-10 h-10 rounded-xl bg-gradient-to-br ${provider.color} flex items-center justify-center text-white shrink-0`}
            >
              {provider.icon}
            </div>
          ) : (
            <div className="w-10 h-10 rounded-xl bg-white/[0.06] flex items-center justify-center text-white/40 shrink-0">
              <Video size={18} />
            </div>
          )}
          <div className="min-w-0">
            <p className="text-[14px] font-bold text-white truncate">
              {provider?.name ?? entry.account.platform}
            </p>
            <p className="text-[12px] text-[#9aa0aa] truncate">
              @{entry.account.username || "—"}
            </p>
          </div>
        </div>
        {nextLabel ? (
          <div className="flex items-center gap-1.5 text-[12px] text-[#9aa0aa] mb-3">
            <Clock size={11} />
            <span>Next: {nextLabel}</span>
          </div>
        ) : (
          <div className="text-[12px] text-[#9aa0aa] italic mb-3">
            Nothing scheduled yet.
          </div>
        )}
        <div className="flex items-center justify-between text-[12px] text-[#9aa0aa] group-hover:text-white transition-colors">
          <span>
            {entry.count === 0
              ? "Apri calendario →"
              : `${entry.count === 1 ? "1 video" : `${entry.count} videos`} in coda`}
          </span>
          <ArrowRight size={14} />
        </div>
      </div>
    </Link>
  );
}
