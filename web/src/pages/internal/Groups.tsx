import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ChevronDown,
  ChevronRight,
  Folder,
  FolderPlus,
  Plus,
  Trash2,
  Pencil,
  CheckCircle2,
  PauseCircle,
  RefreshCw,
  CalendarClock,
  Link2,
} from "lucide-react";
import { authedFetch, AuthError, ApiError } from "../../lib/auth";
import { fetchSession } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { Skeleton, ErrorState, EmptyState } from "../../components/feedback";

type Group = {
  id: number;
  workspace_id: number;
  parent_group_id?: number | null;
  name: string;
};

type PlatformAccount = {
  id: number;
  workspace_id?: number;
  platform: string;
  username: string;
  platform_user_id: string;
  status: string;
  created_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; groups: Group[]; accounts: PlatformAccount[]; workspaceId: number; accountsByGroup: Map<number, PlatformAccount[]> }
  | { kind: "error"; message: string };

// loadAccountsByGroup fires N parallel /api/v1/groups/{id}/accounts
// requests and returns the joined Map. Kept module-level so the
// side-effect-only Promise.all with `void` (which silently lost the
// result) is replaced by a value-returning concurrent fetch that the
// caller actually consumes. Tests rely on this returning a Map, not
// a tuple — the upstream `ListByWorkspace?include_accounts=true`
// follow-up will collapse this to one round-trip.
//
// `allAccounts` is hoisted as an explicit parameter so the search
// stays O(1) per id and the function has no leftover dead params.
async function loadAccountsByGroup(
  groups: Group[],
  allAccounts: PlatformAccount[],
  signal: AbortSignal,
): Promise<Map<number, PlatformAccount[]>> {
  if (groups.length === 0) {
    return new Map();
  }
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const accountIndex = new Map(allAccounts.map((a) => [a.id, a]));
  const respLists = await Promise.all(
    groups.map(async (g) => {
      try {
        const r = await authedFetch(`/api/v1/groups/${g.id}/accounts`, { signal });
        const d = (await r.json()) as { account_ids: number[] };
        const mapped: PlatformAccount[] = [];
        for (const id of d.account_ids ?? []) {
          const acc = accountIndex.get(id);
          if (acc) mapped.push(acc);
        }
        return [g.id, mapped] as const;
      } catch {
        return [g.id, [] as PlatformAccount[]] as const;
      }
    }),
  );
  return new Map(respLists);
}

const PLATFORM_GRADIENT: Record<string, string> = {
  facebook: "from-blue-500 to-blue-700",
  instagram: "from-pink-500 to-amber-500",
  threads: "from-zinc-700 to-zinc-900",
  tiktok: "from-fuchsia-500 to-rose-500",
  twitter: "from-sky-400 to-sky-600",
  youtube: "from-red-500 to-red-700",
  linkedin: "from-blue-600 to-indigo-700",
  google_drive: "from-emerald-500 to-emerald-700",
  "google-drive": "from-emerald-500 to-emerald-700",
};

type TreeNode = Group & { children: TreeNode[]; accounts: PlatformAccount[] };

function buildTree(
  groups: Group[],
  accountsByGroup: Map<number, PlatformAccount[]>,
): TreeNode[] {
  const map = new Map<number, TreeNode>();
  groups.forEach((g) => map.set(g.id, { ...g, children: [], accounts: [] }));
  const roots: TreeNode[] = [];
  groups.forEach((g) => {
    const node = map.get(g.id)!;
    node.accounts = accountsByGroup.get(g.id) ?? [];
    if (g.parent_group_id) {
      const parent = map.get(g.parent_group_id);
      if (parent) parent.children.push(node);
      else roots.push(node); // orphan parent (deleted) → root
    } else {
      roots.push(node);
    }
  });
  return roots;
}

export function GroupsPage() {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [selectedGroupId, setSelectedGroupId] = useState<number | null>(null);
  const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null);
  const [newGroupName, setNewGroupName] = useState("");
  const [creatingGroup, setCreatingGroup] = useState(false);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const session = await fetchSession();
      if (controller.signal.aborted) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      const meResp = await authedFetch("/api/v1/auth/me", { signal: controller.signal });
      if (controller.signal.aborted) return;
      const meData = (await meResp.json()) as { workspace_id: number };
      const workspaceId = meData.workspace_id;
      const [groupsResp, accountsResp] = await Promise.all([
        authedFetch(`/api/v1/groups/?workspace_id=${workspaceId}`, { signal: controller.signal }),
        authedFetch("/api/v1/accounts", { signal: controller.signal }),
      ]);
      if (controller.signal.aborted) return;
      const groupsData = (await groupsResp.json()) as { groups: Group[] };
      const accountsData = (await accountsResp.json()) as { accounts: PlatformAccount[] };
      // Bulk-load group accounts in parallel so the tree can render
      // each node's chip list without an extra fetch on group-click.
      // Returned as a Map<groupID, PlatformAccount[]> — passed to
      // buildTree so TreeNode.accounts is populated for every node.
      const accountsByGroup = await loadAccountsByGroup(
        groupsData.groups ?? [],
        accountsData.accounts ?? [],
        controller.signal,
      );
      setState({
        kind: "ready",
        groups: groupsData.groups ?? [],
        accounts: accountsData.accounts ?? [],
        workspaceId,
        accountsByGroup,
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof ApiError ? err.message : "Unable to load groups.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  const tree = useMemo(() => {
    if (state.kind !== "ready") return [];
    return buildTree(state.groups, state.accountsByGroup);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const selectedGroup = useMemo(() => {
    if (state.kind !== "ready" || selectedGroupId == null) return null;
    const findNode = (nodes: TreeNode[]): TreeNode | null => {
      for (const n of nodes) {
        if (n.id === selectedGroupId) return n;
        const child = findNode(n.children);
        if (child) return child;
      }
      return null;
    };
    return findNode(tree);
  }, [state, tree, selectedGroupId]);

  const selectedAccount = useMemo(() => {
    if (state.kind !== "ready" || selectedAccountId == null) return null;
    return state.accounts.find((a) => a.id === selectedAccountId) ?? null;
  }, [state, selectedAccountId]);

  const handleCreateGroup = useCallback(async (parentId?: number) => {
    if (!newGroupName.trim() || state.kind !== "ready") return;
    setCreatingGroup(true);
    try {
      const body = {
        workspace_id: state.workspaceId,
        parent_group_id: parentId ?? null,
        name: newGroupName.trim(),
      };
      await authedFetch("/api/v1/groups/", {
        method: "POST",
        body: JSON.stringify(body),
      });
      setNewGroupName("");
      await load();
    } finally {
      setCreatingGroup(false);
    }
  }, [newGroupName, state, load]);

  return (
    <div className="min-h-full p-4 sm:p-6 lg:p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between mb-6">
          <div>
            <h1 className="text-[24px] sm:text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Folder size={28} className="text-white/40" />
              Groups
            </h1>
            <p className="text-[14px] sm:text-[15px] text-[#9aa0aa] mt-1">
              Organize your social accounts into folders and sub-folders.
              Click a group to see its accounts, click an account for details and
              quick actions.
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
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* Tree */}
          <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[14px] font-bold text-white uppercase tracking-wider">Folders</h2>
            </div>

            {/* New group (root) */}
            <div className="flex items-center gap-2 mb-4">
              <input
                type="text"
                value={newGroupName}
                onChange={(e) => setNewGroupName(e.target.value)}
                placeholder="New folder name..."
                className="flex-1 px-3 py-2 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa] focus:outline-none focus:ring-2 focus:ring-violet-500/40"
                aria-label="New group name"
              />
              <button
                type="button"
                onClick={() => void handleCreateGroup()}
                disabled={!newGroupName.trim() || creatingGroup}
                className="inline-flex items-center gap-1 px-3 py-2 rounded-lg bg-white text-black text-[12px] font-semibold disabled:opacity-50 disabled:cursor-not-allowed hover:bg-zinc-100 transition-colors"
              >
                <Plus size={14} /> Add
              </button>
            </div>

            {state.kind === "loading" && (
              <div className="space-y-2">
                <Skeleton variant="card" height={28} />
                <Skeleton variant="card" height={28} />
                <Skeleton variant="card" height={28} />
              </div>
            )}

            {state.kind === "error" && (
              <ErrorState
                title="Couldn't load groups"
                message={state.message}
                onRetry={() => void load()}
                className="bg-transparent border-0 p-0"
              />
            )}

            {state.kind === "ready" && tree.length === 0 && (
              <EmptyState
                title="No folders yet"
                description="Add your first folder to start organizing your accounts."
                icon={<FolderPlus size={28} />}
                className="bg-transparent border-0 p-0"
              />
            )}

            {state.kind === "ready" && tree.length > 0 && (
              <TreeView
                nodes={tree}
                selectedGroupId={selectedGroupId}
                onSelect={(id) => {
                  setSelectedGroupId(id);
                  setSelectedAccountId(null);
                }}
              />
            )}
          </div>

          {/* Right pane: selected node detail (group OR account). */}
          <div className="lg:col-span-2 surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5 min-h-[300px]">
            {selectedAccount ? (
              <AccountDetailPanel
                account={selectedAccount}
                onClose={() => setSelectedAccountId(null)}
                onUpdated={() => void load()}
              />
            ) : selectedGroup ? (
              <GroupDetailPanel
                group={selectedGroup}
                accounts={state.kind === "ready" ? state.accounts : []}
                onPickAccount={(id) => setSelectedAccountId(id)}
                onCreateSubgroup={(name) => {
                  if (!name.trim()) return;
                  setNewGroupName(name);
                  void handleCreateGroup(selectedGroup.id);
                }}
                onDeleteGroup={async () => {
                  if (!window.confirm(`Delete folder "${selectedGroup.name}"? Sub-folders and account links will be removed.`)) return;
                  try {
                    await authedFetch(`/api/v1/groups/${selectedGroup.id}`, { method: "DELETE" });
                    setSelectedGroupId(null);
                    await load();
                  } catch {
                    /* toasted by authedFetch */
                  }
                }}
                onSetGroupAccounts={async (accountIds) => {
                  try {
                    await authedFetch(`/api/v1/groups/${selectedGroup.id}/accounts`, {
                      method: "PUT",
                      body: JSON.stringify({ account_ids: accountIds }),
                    });
                    await load();
                  } catch {
                    /* toasted by authedFetch */
                  }
                }}
              />
            ) : (
              <div className="flex h-full min-h-[260px] items-center justify-center text-center text-[#9aa0aa] text-[14px]">
                <div>
                  <Folder size={32} className="mx-auto opacity-50 mb-3" />
                  <p className="max-w-md">Select a folder to view its accounts,</p>
                  <p>or click an account for quick actions.</p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------- Tree

function TreeView({
  nodes,
  selectedGroupId,
  onSelect,
  depth = 0,
}: {
  nodes: TreeNode[];
  selectedGroupId: number | null;
  onSelect: (id: number) => void;
  depth?: number;
}) {
  return (
    <ul className="space-y-1">
      {nodes.map((n) => (
        <TreeNodeRow
          key={n.id}
          node={n}
          selected={selectedGroupId === n.id}
          selectedGroupId={selectedGroupId}
          onSelect={onSelect}
          depth={depth}
        />
      ))}
    </ul>
  );
}

function TreeNodeRow({
  node,
  selected,
  selectedGroupId,
  onSelect,
  depth,
}: {
  node: TreeNode;
  selected: boolean;
  selectedGroupId: number | null;
  onSelect: (id: number) => void;
  depth: number;
}) {
  const [open, setOpen] = useState(true);
  const hasChildren = node.children.length > 0;
  const hasAccounts = node.accounts.length > 0;
  // The row is one button, the chevron (when present) is a SIBLING
  // button. Nested <button> elements are invalid HTML and behave
  // unpredictably across browsers; rendering them adjacently keeps
  // both keyboard-reachable without nesting a button inside a button.
  return (
    <li className="flex items-stretch gap-1" style={{ paddingLeft: `${depth * 12}px` }}>
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(node.id)}
        className={cn(
          "flex-1 flex items-center gap-1.5 px-2 py-1.5 rounded-lg text-[13px] text-left transition-colors",
          selected
            ? "bg-violet-500/15 text-white border border-violet-500/30"
            : "text-[#e8e8ef] hover:bg-white/[0.06] border border-transparent",
        )}
      >
        <span className="w-4 inline-flex items-center justify-center text-[#9aa0aa]">
          {(hasChildren || hasAccounts) ? (open ? <ChevronDown size={12} /> : <ChevronRight size={12} />) : null}
        </span>
        <Folder size={14} className="text-amber-300/80 shrink-0" />
        <span className="flex-1 truncate font-medium">{node.name}</span>
        {(hasAccounts || hasChildren) && (
          <span className="text-[10px] tabular-nums text-[#9aa0aa]">
            {hasAccounts ? node.accounts.length : ""}
            {hasAccounts && hasChildren ? " · " : ""}
            {hasChildren ? `${countDescendants(node)} sub` : ""}
          </span>
        )}
      </button>
      {(hasChildren || hasAccounts) && (
        <button
          type="button"
          aria-label={open ? "Collapse" : "Expand"}
          onClick={() => setOpen((v) => !v)}
          className="px-1.5 rounded-md hover:bg-white/[0.08] text-[#9aa0aa]"
        >
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </button>
      )}
      {open && hasChildren && (
        <div className="basis-full mt-1">
          <TreeView
            nodes={node.children}
            selectedGroupId={selectedGroupId}
            onSelect={onSelect}
            depth={depth + 1}
          />
        </div>
      )}
    </li>
  );
}

function countDescendants(n: TreeNode): number {
  return n.children.length + n.children.reduce((acc, c) => acc + countDescendants(c), 0);
}

// ---------------------------------------------------------------------- Group detail

function GroupDetailPanel({
  group,
  accounts,
  onPickAccount,
  onCreateSubgroup,
  onDeleteGroup,
  onSetGroupAccounts,
}: {
  group: TreeNode;
  accounts: PlatformAccount[];
  onPickAccount: (id: number) => void;
  onCreateSubgroup: (name: string) => void;
  onDeleteGroup: () => void;
  onSetGroupAccounts: (ids: number[]) => void;
}) {
  const [subName, setSubName] = useState("");
  const [draggingAccountId, setDraggingAccountId] = useState<number | null>(null);
  const availableAccounts = accounts.filter((a) => !group.accounts.find((ga) => ga.id === a.id));
  return (
    <div>
      <div className="flex items-start justify-between gap-3 mb-5">
        <div>
          <h2 className="text-[18px] font-bold text-white flex items-center gap-2">
            <Folder size={20} className="text-amber-300/80" />
            {group.name}
          </h2>
          <p className="text-[12px] text-[#9aa0aa] mt-0.5">
            {group.accounts.length} accounts · {group.children.length} sub-folders
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onDeleteGroup}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[12px] font-medium text-red-300 hover:bg-red-500/[0.12] hover:border-red-500/40 transition-colors"
            aria-label="Delete folder"
          >
            <Trash2 size={13} /> Delete
          </button>
        </div>
      </div>

      {/* Add sub-group */}
      <div className="flex items-center gap-2 mb-5">
        <input
          type="text"
          value={subName}
          onChange={(e) => setSubName(e.target.value)}
          placeholder="New sub-folder name..."
          className="flex-1 px-3 py-2 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa] focus:outline-none focus:ring-2 focus:ring-violet-500/40"
          aria-label="New sub-group name"
        />
        <button
          type="button"
          onClick={() => {
            onCreateSubgroup(subName);
            setSubName("");
          }}
          disabled={!subName.trim()}
          className="inline-flex items-center gap-1 px-3 py-2 rounded-lg bg-white text-black text-[12px] font-semibold disabled:opacity-50 disabled:cursor-not-allowed hover:bg-zinc-100 transition-colors"
        >
          <Plus size={14} /> Add
        </button>
      </div>

      {/* Current accounts in this group */}
      <div className="mb-6">
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa] mb-2">
          Accounts in this folder ({group.accounts.length})
        </h3>
        {group.accounts.length === 0 ? (
          <p className="text-[12px] text-[#9aa0aa] italic">No accounts yet. Drag one in from below.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {group.accounts.map((a) => (
              <AccountChip key={a.id} account={a} onClick={() => onPickAccount(a.id)} />
            ))}
          </div>
        )}
      </div>

      {/* Available accounts (drag-into-this-group) */}
      <div>
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa] mb-2">
          Available accounts ({availableAccounts.length})
        </h3>
        <div
          className={cn(
            "min-h-[80px] p-2 rounded-xl border border-dashed border-white/[0.12] bg-white/[0.02]",
            draggingAccountId != null && "ring-2 ring-emerald-500/40 bg-emerald-500/[0.04]",
          )}
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("text/plain")) {
              e.preventDefault();
              e.dataTransfer.dropEffect = "move";
            }
          }}
          onDrop={(e) => {
            e.preventDefault();
            const id = Number(e.dataTransfer.getData("text/plain"));
            setDraggingAccountId(null);
            if (!Number.isFinite(id) || id <= 0) return;
            if (group.accounts.find((ga) => ga.id === id)) return;
            onSetGroupAccounts([...group.accounts.map((a) => a.id), id]);
          }}
        >
          {availableAccounts.length === 0 ? (
            <p className="text-[12px] text-center text-[#9aa0aa] italic">
              Every account is already in this folder. Connect more from the Linking page.
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {availableAccounts.map((a) => (
                <div
                  key={a.id}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData("text/plain", String(a.id));
                    e.dataTransfer.effectAllowed = "move";
                    setDraggingAccountId(a.id);
                  }}
                  onDragEnd={() => setDraggingAccountId(null)}
                  className="cursor-grab active:cursor-grabbing"
                >
                  <AccountChip account={a} onClick={() => onPickAccount(a.id)} subtle />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------- Account chip + detail

function AccountChip({
  account,
  onClick,
  subtle,
}: {
  account: PlatformAccount;
  onClick: () => void;
  subtle?: boolean;
}) {
  const grad = PLATFORM_GRADIENT[account.platform] ?? "from-zinc-500 to-zinc-700";
  const StatusIcon =
    account.status === "active" ? CheckCircle2 :
    account.status === "reauth_required" ? PauseCircle :
    PauseCircle;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex items-center gap-2 px-2.5 py-2 rounded-lg border text-left transition-colors w-full",
        subtle
          ? "bg-white/[0.04] border-white/[0.08] text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white"
          : "bg-white/[0.06] border-white/[0.16] text-white hover:bg-white/[0.10]",
      )}
    >
      <div
        className={cn(
          "w-8 h-8 rounded-lg bg-gradient-to-br flex items-center justify-center text-white text-[11px] font-bold shrink-0",
          grad,
        )}
      >
        {(account.platform[0] ?? "?").toUpperCase()}
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-[12px] font-semibold truncate">{account.username || account.platform_user_id}</p>
        <p className="text-[10px] text-[#9aa0aa] truncate">{account.platform}</p>
      </div>
      <StatusIcon
        size={14}
        className={cn(
          "shrink-0",
          account.status === "active" ? "text-emerald-400" : "text-amber-400",
        )}
      />
    </button>
  );
}

function AccountDetailPanel({
  account,
  onClose,
  onUpdated,
}: {
  account: PlatformAccount;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const [busy, setBusy] = useState<null | "reconnect" | "validate" | "remove">(null);
  const [details, setDetails] = useState<{ user_id?: number; posts?: { queued: number; published: number; failed: number } } | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [accountsResp, postsResp] = await Promise.all([
          authedFetch(`/api/v1/accounts/${account.id}`),
          authedFetch("/api/v1/posts"),
        ]);
        if (cancelled) return;
        const acct = (await accountsResp.json()) as PlatformAccount & { user_id: number };
        const posts = (await postsResp.json()) as { posts: Array<{ status: string }> };
        const summary = { queued: 0, published: 0, failed: 0 };
        for (const p of posts.posts ?? []) {
          if (p.status === "queued") summary.queued += 1;
          else if (p.status === "published") summary.published += 1;
          else if (p.status === "failed") summary.failed += 1;
        }
        setDetails({ user_id: acct.user_id, posts: summary });
      } catch {
        // optional details — silently fall back to username only
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [account.id]);

  const grad = PLATFORM_GRADIENT[account.platform] ?? "from-zinc-500 to-zinc-700";
  const StatusIcon =
    account.status === "active" ? CheckCircle2 :
    PauseCircle;

  const runAction = async (
    action: "reconnect" | "validate" | "remove",
    method: string,
    endpoint: string,
    body?: string,
  ) => {
    if (action === "remove" && !window.confirm(`Disconnect ${account.platform} @${account.username}? This will cancel scheduled posts targeting this account.`)) {
      return;
    }
    setBusy(action);
    try {
      await authedFetch(endpoint, { method, body });
      onUpdated();
      if (action === "remove") onClose();
    } finally {
      setBusy(null);
    }
  };

  return (
    <div>
      <div className="flex items-start gap-4 mb-6">
        <div
          className={cn(
            "w-16 h-16 rounded-xl bg-gradient-to-br flex items-center justify-center text-white text-[18px] font-extrabold shrink-0",
            grad,
          )}
        >
          {(account.platform[0] ?? "?").toUpperCase()}
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-[20px] font-extrabold tracking-[-0.01em] text-white flex items-center gap-2 flex-wrap">
            {account.username || account.platform_user_id}
            <span className="text-[11px] font-medium uppercase tracking-wider text-[#9aa0aa]">
              {account.platform}
            </span>
          </h2>
          <p className="text-[12px] text-[#9aa0aa] mt-1 flex items-center gap-2">
            <StatusIcon size={14} className={account.status === "active" ? "text-emerald-400" : "text-amber-400"} />
            {account.status.replace(/_/g, " ")}
            {details?.user_id ? <> · ID #{details.user_id}</> : null}
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="px-3 py-1.5 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[12px] font-medium text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white transition-colors"
        >
          Back
        </button>
      </div>

      {/* Quick stats */}
      <div className="grid grid-cols-3 gap-3 mb-6">
        <StatMini icon={CalendarClock} label="Workspace queued" value={details?.posts?.queued ?? "—"} accent="text-amber-300" />
        <StatMini icon={CheckCircle2} label="Workspace published" value={details?.posts?.published ?? "—"} accent="text-emerald-300" />
        <StatMini icon={PauseCircle} label="Workspace failed" value={details?.posts?.failed ?? "—"} accent="text-red-300" />
      </div>

      {/* Actions */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <ActionTile
          icon={RefreshCw}
          label="Reconnect"
          description="Re-run the OAuth flow to refresh tokens."
          onClick={() => void runAction("reconnect", "POST", `/api/v1/accounts/${account.id}/reconnect`)}
          busy={busy === "reconnect"}
        />
        <ActionTile
          icon={Pencil}
          label="Validate"
          description="Test that the stored tokens still work."
          onClick={() => void runAction("validate", "POST", `/api/v1/accounts/${account.id}/validate`)}
          busy={busy === "validate"}
        />
        <ActionTile
          icon={Trash2}
          label="Disconnect"
          description="Removes this account and its tokens."
          onClick={() => void runAction("remove", "DELETE", `/api/v1/accounts/${account.id}`)}
          busy={busy === "remove"}
          danger
        />
      </div>

      <div className="mt-6 flex items-center gap-2 text-[12px] text-[#9aa0aa]">
        <Link2 size={14} />
        Quick jump:
        <Link className="text-white underline hover:no-underline" to="/app/posts">All posts</Link>
        <span className="opacity-50">·</span>
        <Link className="text-white underline hover:no-underline" to="/app/compose">Compose new post</Link>
      </div>
    </div>
  );
}

function StatMini({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: React.ElementType;
  label: string;
  value: number | string;
  accent: string;
}) {
  return (
    <div className="bg-white/[0.04] border border-white/[0.08] rounded-xl p-3">
      <div className="flex items-center justify-between">
        <p className="text-[10px] font-bold uppercase tracking-wider text-[#9aa0aa]">{label}</p>
        <Icon size={14} className={accent} />
      </div>
      <p className={cn("text-[24px] font-extrabold tracking-tight tabular-nums mt-1", accent)}>{value}</p>
    </div>
  );
}

function ActionTile({
  icon: Icon,
  label,
  description,
  onClick,
  busy,
  danger,
}: {
  icon: React.ElementType;
  label: string;
  description: string;
  onClick: () => void;
  busy: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className={cn(
        "text-left p-4 rounded-xl border transition-colors",
        danger
          ? "bg-red-500/[0.08] border-red-500/30 hover:bg-red-500/[0.16] hover:border-red-500/50"
          : "bg-white/[0.06] border-white/[0.12] hover:bg-white/[0.10] hover:border-white/[0.20]",
        busy && "opacity-60 cursor-progress",
      )}
    >
      <div className="flex items-center gap-2 mb-1">
        <Icon size={16} className={danger ? "text-red-300" : "text-white"} />
        <span className={cn("text-[14px] font-bold", danger ? "text-red-200" : "text-white")}>
          {label}
        </span>
        {busy && <RefreshCw size={12} className="animate-spin text-[#9aa0aa] ml-auto" />}
      </div>
      <p className="text-[12px] text-[#9aa0aa] leading-snug">{description}</p>
    </button>
  );
}
