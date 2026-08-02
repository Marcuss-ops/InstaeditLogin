import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  LayoutDashboard,
  Link2,
  ArrowRight,
  Clock,
  Folder,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { type ProviderId } from "../../lib/providers";
import { Skeleton, ErrorState } from "../../components/feedback";
import type { Group } from "./groupsTypes";
import { isPublishableAccount } from "../../types/uploads";

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  username: string;
  created_at: string;
  status: string;
  metadata?: Record<string, unknown>;
};

type GroupSummary = {
  group: Group;
  accountIds: number[];
  accounts: PlatformAccount[];
  scheduled: number;
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
  privateVideos: number;
  // Per-account pending count + earliest scheduled_at, derived from
  // GET /api/v1/uploads/counts. The dashboard widget renders from
  // this map; the calendar page hits /uploads/by-account separately.
  countMap: Map<number, AccountProgrammatoCount>;
  groupSummaries: GroupSummary[];
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
  const [createGroupOpen, setCreateGroupOpen] = useState(false);
  const [newGroupName, setNewGroupName] = useState("");
  const [creatingGroup, setCreatingGroup] = useState(false);
  const [draggedAccountId, setDraggedAccountId] = useState<number | null>(null);
  const [savingDrop, setSavingDrop] = useState<number | null>(null);

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
      const youtubeAccounts = (accountsData.accounts ?? []).filter((account) => account.platform === "youtube");
      const privateVideoCounts = await Promise.all(
        youtubeAccounts.map(async (account) => {
          try {
            const response = await authedFetch(`/api/v1/accounts/${account.id}/content?limit=50&privacy=private`, { signal: controller.signal });
            const data = (await response.json()) as { items?: Array<{ privacy?: string }> };
            return (data.items ?? []).filter((item) => item.privacy === "private").length;
          } catch {
            return 0;
          }
        }),
      );
      let groupSummaries: GroupSummary[] = [];
      try {
        // The aggregate endpoint resolves the active workspace from the
        // authenticated identity, so the dashboard does not need a separate
        // workspace lookup before loading groups and memberships.
        const groupsResp = await authedFetch("/api/v1/groups/aggregate", { signal: controller.signal });
        const groupsData = (await groupsResp.json()) as {
          groups: Array<Group & { account_ids?: number[] }>;
        };
        const accountIndex = new Map((accountsData.accounts ?? []).map((account) => [account.id, account]));
        const directMemberships = new Map(
          (groupsData.groups ?? []).map((group) => [group.id, group.account_ids ?? []] as const),
        );
        const children = new Map<number, Group[]>();
        for (const group of groupsData.groups ?? []) {
          if (group.parent_group_id != null) {
            const list = children.get(group.parent_group_id) ?? [];
            list.push(group);
            children.set(group.parent_group_id, list);
          }
        }
        const collect = (group: Group): number[] => {
          const ids = new Set(directMemberships.get(group.id) ?? []);
          for (const child of children.get(group.id) ?? []) collect(child).forEach((id) => ids.add(id));
          return [...ids];
        };
        groupSummaries = (groupsData.groups ?? [])
          .filter((group) => group.parent_group_id == null)
          .map((group) => {
            const accountIds = collect(group);
            const groupAccounts = accountIds.map((id) => accountIndex.get(id)).filter((account): account is PlatformAccount => account != null && isPublishableAccount(account));
            return {
              group,
              accountIds,
              accounts: groupAccounts,
              scheduled: accountIds.reduce((sum, id) => sum + (countsData.counts?.find((count) => count.account_id === id)?.count ?? 0), 0),
            };
          });
      } catch {
        // Groups are an optional dashboard projection; account cards still work if unavailable.
      }
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
          privateVideos: privateVideoCounts.reduce((sum, count) => sum + count, 0),
          groupSummaries,
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
          queuedUploads: state.data.privateVideos,
        }
      : null;

  const createGroup = async () => {
    if (!newGroupName.trim() || creatingGroup) return;
    setCreatingGroup(true);
    try {
      const me = await authedFetch("/api/v1/auth/me");
      const { workspace_id: workspaceId } = await me.json() as { workspace_id: number };
      const response = await authedFetch("/api/v1/groups/", { method: "POST", body: JSON.stringify({ workspace_id: workspaceId, name: newGroupName.trim() }) });
      const group = await response.json() as { id: number };
      setCreateGroupOpen(false);
      setNewGroupName("");
      navigate(`/app/groups/${group.id}`);
    } finally {
      setCreatingGroup(false);
    }
  };

  const addAccountToGroup = async (accountId: number, groupId: number) => {
    if (savingDrop != null || state.kind !== "ready") return;
    const summary = state.data.groupSummaries.find((item) => item.group.id === groupId);
    if (!summary || summary.accountIds.includes(accountId)) return;
    setSavingDrop(groupId);
    try {
      const currentResponse = await authedFetch(`/api/v1/groups/${groupId}/accounts`);
      const current = await currentResponse.json() as { account_ids?: number[] };
      const currentIds = current.account_ids ?? [];
      if (currentIds.includes(accountId)) return;
      await authedFetch(`/api/v1/groups/${groupId}/accounts`, {
        method: "PUT",
        body: JSON.stringify({ account_ids: [...currentIds, accountId] }),
      });
      await load();
    } finally {
      setSavingDrop(null);
      setDraggedAccountId(null);
    }
  };

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
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-8">
              <StatCard label="Connected accounts" value={stats.connected} icon={Link2} to="/app/linking" />
              <StatCard label="Video privati da pubblicare" value={state.data.privateVideos} icon={Clock} to="/app/calendar?tab=videos" />
            </div>

            {(
              <section className="mb-8" data-testid="dashboard-groups">
                <div className="flex items-center justify-between mb-4">
                  <div>
                    <h2 className="text-[18px] font-extrabold tracking-tight text-white flex items-center gap-2">
                      <Folder size={20} className="text-amber-300/80" />
                      Groups
                    </h2>
                    <p className="text-[13px] text-[#9aa0aa] mt-0.5">Apri un gruppo per vedere e gestire i canali che contiene.</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <button type="button" onClick={() => setCreateGroupOpen(true)} className="rounded-xl bg-violet-500 px-3 py-2 text-[12px] font-semibold text-white hover:bg-violet-400">+ Crea gruppo</button>
                    <Link to="/app/groups" className="text-[13px] font-medium text-[#9aa0aa] hover:text-white no-underline">Gestisci <ArrowRight size={14} className="inline" /></Link>
                  </div>
                </div>
                {createGroupOpen && (
                  <div className="mb-4 flex flex-col sm:flex-row gap-2 rounded-xl border border-violet-400/30 bg-violet-500/[0.08] p-3">
                    <input autoFocus value={newGroupName} onChange={(event) => setNewGroupName(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void createGroup(); }} placeholder="Nome gruppo, es. WWE" className="flex-1 rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-[13px] text-white outline-none" />
                    <button type="button" disabled={!newGroupName.trim() || creatingGroup} onClick={() => void createGroup()} className="rounded-lg bg-white px-3 py-2 text-[12px] font-semibold text-black disabled:opacity-50">Crea e scegli canali</button>
                    <button type="button" onClick={() => setCreateGroupOpen(false)} className="rounded-lg border border-white/[0.10] px-3 py-2 text-[12px] text-[#c8cbd4]">Annulla</button>
                  </div>
                )}
                {state.data.groupSummaries.length === 0 ? (
                  <div className="rounded-2xl border border-dashed border-white/[0.12] bg-white/[0.02] p-8 text-center text-[13px] text-[#9aa0aa]">
                    Nessun gruppo creato. Premi “Crea gruppo” per iniziare a organizzare i canali validi.
                  </div>
                ) : <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                  {state.data.groupSummaries.map((summary) => (
                    <Link
                      key={summary.group.id}
                      to={`/app/groups/${summary.group.id}`}
                      onDragOver={(event) => {
                        if (draggedAccountId != null) {
                          event.preventDefault();
                          event.dataTransfer.dropEffect = "copy";
                        }
                      }}
                      onDrop={(event) => {
                        event.preventDefault();
                        const accountId = Number(event.dataTransfer.getData("application/x-instaedit-account"));
                        if (Number.isFinite(accountId) && accountId > 0) void addAccountToGroup(accountId, summary.group.id);
                      }}
                      className={`surface-card bg-[#1f1f2e] border rounded-2xl p-5 hover:border-violet-400/50 hover:bg-[#252538] transition-all no-underline ${draggedAccountId != null ? "border-dashed border-violet-400/60 ring-1 ring-violet-400/20" : "border-white/[0.12]"}`}
                    >
                      <div className="flex items-center gap-3 mb-3">
                        <div className="w-10 h-10 rounded-xl bg-amber-400/15 text-amber-300 flex items-center justify-center"><Folder size={19} /></div>
                        <div className="min-w-0"><p className="text-[15px] font-bold text-white truncate">{summary.group.name}</p><p className="text-[12px] text-[#9aa0aa]">{summary.accounts.length} canali{savingDrop === summary.group.id ? " · salvataggio…" : " · trascina qui"}</p></div>
                        <ArrowRight size={16} className="ml-auto text-[#9aa0aa]" />
                      </div>
                      <div className="rounded-lg bg-white/[0.04] px-3 py-2 text-[12px] text-[#c8cbd4]">Programmati <b className="text-white">{summary.scheduled}</b></div>
                    </Link>
                  ))}
                </div>}
              </section>
            )}

            {(() => {
              const groupedIds = new Set(state.data.groupSummaries.flatMap((summary) => summary.accountIds));
              const availableAccounts = state.data.accounts.filter((account) => isPublishableAccount(account) && account.platform !== "google-drive" && !groupedIds.has(account.id));
              return (
                <section className="mb-8" data-testid="dashboard-available-accounts">
                  <div className="flex items-center justify-between mb-4">
                    <div>
                      <h2 className="text-[18px] font-extrabold tracking-tight text-white">Account disponibili</h2>
                      <p className="text-[13px] text-[#9aa0aa] mt-0.5">Canali attivi non ancora assegnati a un gruppo.</p>
                    </div>
                    <Link to="/app/groups" className="text-[13px] font-medium text-violet-300 hover:text-white no-underline">Gestisci gruppi <ArrowRight size={14} className="inline" /></Link>
                  </div>
                  {availableAccounts.length === 0 ? (
                    <div className="rounded-2xl border border-dashed border-white/[0.12] bg-white/[0.02] p-6 text-center text-[13px] text-[#9aa0aa]">Tutti i canali attivi sono già organizzati in un gruppo.</div>
                  ) : (
                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                      {availableAccounts.map((account) => (
                        <Link
                          key={account.id}
                          to={`/app/dashboard-channels/${account.id}`}
                          draggable
                          onDragStart={(event) => {
                            event.dataTransfer.setData("application/x-instaedit-account", String(account.id));
                            event.dataTransfer.effectAllowed = "copy";
                            setDraggedAccountId(account.id);
                          }}
                          onDragEnd={() => setDraggedAccountId(null)}
                          className="flex cursor-grab items-center gap-3 rounded-xl border border-white/[0.10] bg-white/[0.03] p-3 no-underline hover:border-violet-400/50 hover:bg-white/[0.06] active:cursor-grabbing transition-colors"
                        >
                          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-violet-500/15 text-[12px] font-bold text-violet-200">{account.platform.slice(0, 1).toUpperCase()}</div>
                          <div className="min-w-0"><p className="truncate text-[13px] font-semibold text-white">{account.username || `Account ${account.id}`}</p><p className="text-[11px] text-[#9aa0aa]">{account.platform} · trascina nel gruppo</p></div>
                        </Link>
                      ))}
                    </div>
                  )}
                </section>
              );
            })()}

            <div className="h-4" />
          </>
        )}
      </div>
    </div>
  );
}
