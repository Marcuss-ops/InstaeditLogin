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
  LockKeyhole,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { getProvider, type ProviderId } from "../../lib/providers";
import { Skeleton, ErrorState } from "../../components/feedback";
import { listChannelContent } from "../../features/channels/api/channelContentApi";

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
  privateCountMap: Map<number, number>;
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
      const privateCountEntries = await Promise.all(
        (accountsData.accounts ?? [])
          .filter((account) => account.platform === "youtube")
          .map(async (account) => {
            let count = 0;
            let cursor: string | undefined;
            do {
              const page = await listChannelContent({
                accountId: account.id,
                privacy: "private",
                limit: 50,
                cursor,
                signal: controller.signal,
              });
              count += page.items.length;
              cursor = page.next_cursor;
            } while (cursor);
            return [account.id, count] as const;
          }),
      );
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
          privateCountMap: new Map(privateCountEntries),
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
      privateCount: number;
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
          privateCount: state.data.privateCountMap.get(a.id) ?? 0,
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
                      <Video size={20} className="text-white/60" />
                      Canali YouTube
                    </h2>
                    <p className="text-[13px] text-[#9aa0aa] mt-0.5">
                      Apri il calendario o i video privati del canale.
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

// AccountProgrammatoCard keeps the two channel actions together so a
// dashboard channel never needs two separate cards for its two views.
function AccountProgrammatoCard({
  entry,
}: {
  entry: {
    account: PlatformAccount;
    count: number;
    nextAt: string | null;
    privateCount: number;
  };
}) {
  const provider = getProvider(entry.account.platform);

  return (
    <div
      className="group surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl overflow-hidden hover:border-white/[0.30] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)] transition-all"
      data-testid={`dash-programmati-card-${entry.account.id}`}
    >
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
        <div className="grid grid-cols-2 gap-2">
          <Link
            to={`/app/uploads/calendar?account_id=${entry.account.id}`}
            className="flex items-center justify-between gap-2 rounded-xl border border-white/[0.08] bg-white/[0.04] px-3 py-2.5 text-[12px] font-semibold text-[#c8cbd4] hover:bg-white/[0.09] hover:text-white transition-colors no-underline"
          >
            <span className="inline-flex items-center gap-1.5"><CalendarClock size={14} /> Programmati</span>
            <span className="text-white tabular-nums">{entry.count}</span>
          </Link>
          <Link
            to={`/app/dashboard-channels/${entry.account.id}?privacy=private`}
            className="flex items-center justify-between gap-2 rounded-xl border border-white/[0.08] bg-white/[0.04] px-3 py-2.5 text-[12px] font-semibold text-[#c8cbd4] hover:bg-white/[0.09] hover:text-white transition-colors no-underline"
          >
            <span className="inline-flex items-center gap-1.5"><LockKeyhole size={14} /> Privati</span>
            <span className="text-white tabular-nums">{entry.privateCount}</span>
          </Link>
        </div>
      </div>
    </div>
  );
}
