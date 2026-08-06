import { useState, type DragEvent } from "react";
import {
  Folder,
  FolderPlus,
  GripVertical,
  Inbox,
  Plus,
  RefreshCw,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { authedFetch } from "../../lib/auth";
import { ErrorState, EmptyState, Skeleton } from "../../components/feedback";
import { useGroupsData } from "./useGroupsData";
import { TreeView } from "./GroupsTreeView";
import { AccountDetailPanel, GroupDetailPanel } from "./GroupsDetailPanels";
import {
  PLATFORM_GRADIENT,
  type PlatformAccount,
  type TreeNode,
} from "./groupsTypes";
import { cn } from "../../lib/utils";

export function GroupsPage() {
  const navigate = useNavigate();
  const {
    state,
    selectedGroupId,
    setSelectedGroupId,
    setSelectedAccountId,
    newGroupName,
    setNewGroupName,
    creatingGroup,
    busyAccountId,
    load,
    handleCreateGroup,
    assignAccountToGroup,
    ungroupedAccounts,
    tree,
    selectedGroup,
    selectedAccount,
  } = useGroupsData();

  // Drag & drop state: which channel is being dragged and which group chip
  // it is currently hovering over (for the highlight ring).
  const [draggedAccountId, setDraggedAccountId] = useState<number | null>(null);
  const [dragOverGroupId, setDragOverGroupId] = useState<number | null>(null);

  const allGroups = flattenTree(tree);

  const handleDropOnGroup = (event: DragEvent, groupId: number) => {
    event.preventDefault();
    setDragOverGroupId(null);
    const accountId = Number(event.dataTransfer.getData("text/plain"));
    setDraggedAccountId((current) => (current === accountId ? null : current));
    if (!Number.isFinite(accountId) || accountId <= 0) return;
    void assignAccountToGroup(accountId, groupId);
  };

  return (
    <div className="min-h-full w-full bg-[#030308] p-4 text-[#e8e8ef] sm:p-6 lg:p-8">
      <div className="mx-auto w-full max-w-[1600px]">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between mb-6">
          <div>
            <h1 className="text-[24px] sm:text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Folder size={28} className="text-white/40" />
              Groups
            </h1>
            <p className="text-[14px] sm:text-[15px] text-[#9aa0aa] mt-1">
              Organize your social accounts into folders and sub-folders.
              Click a group to see its accounts, click an account for details and
              quick actions. Drag an ungrouped channel onto a folder to assign it.
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

        <div className="mb-6 flex flex-col gap-3 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-3 sm:flex-row sm:items-center">
          <div className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
            {state.kind === "loading" && <span className="text-[12px] text-[#9aa0aa]">Caricamento gruppi…</span>}
            {state.kind === "error" && <span className="text-[12px] text-red-300">{state.message}</span>}
            {state.kind === "ready" && tree.length === 0 && <span className="text-[12px] text-[#9aa0aa]">Nessun gruppo creato.</span>}
            {allGroups.map((node) => (
              <button
                key={node.id}
                type="button"
                onClick={() => { setSelectedGroupId(node.id); setSelectedAccountId(null); }}
                onDragOver={(event) => {
                  event.preventDefault();
                  event.dataTransfer.dropEffect = "move";
                  if (draggedAccountId != null) setDragOverGroupId(node.id);
                }}
                onDragLeave={(event) => {
                  if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                    setDragOverGroupId((current) => (current === node.id ? null : current));
                  }
                }}
                onDrop={(event) => handleDropOnGroup(event, node.id)}
                className={cn(
                  "shrink-0 rounded-xl px-3 py-2 text-left transition-all",
                  selectedGroupId === node.id ? "bg-white text-black shadow-lg scale-105" : "text-white/45 hover:bg-white/[0.06] hover:text-white",
                  dragOverGroupId === node.id && "ring-2 ring-amber-300/80 bg-amber-300/10",
                )}
              >
                <span className={`block truncate ${selectedGroupId === node.id ? "text-[17px] font-bold" : "text-[13px] font-medium"}`}>{node.name}</span>
                <span className={`block text-[10px] ${selectedGroupId === node.id ? "text-black/55" : "text-white/30"}`}>{node.accounts.length} canali</span>
              </button>
            ))}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <input type="text" value={newGroupName} onChange={(e) => setNewGroupName(e.target.value)} placeholder="Nuovo gruppo" className="w-32 rounded-lg border border-white/[0.10] bg-black/20 px-2.5 py-2 text-[12px] text-white outline-none" aria-label="Nuovo gruppo" />
            <button type="button" onClick={() => void handleCreateGroup()} disabled={!newGroupName.trim() || creatingGroup} className="rounded-lg bg-white px-3 py-2 text-[12px] font-semibold text-black disabled:opacity-50"><Plus size={13} className="inline" /> Crea</button>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-6">
          <div className="hidden">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[14px] font-bold text-white uppercase tracking-wider">Folders</h2>
            </div>

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

          <div
            className="w-full min-w-0 rounded-2xl border border-white/[0.08] bg-[#0b0c12] p-5 shadow-[0_18px_60px_rgba(0,0,0,0.18)] min-h-[300px] transition-colors"
            onDragOver={selectedGroup && draggedAccountId != null ? (event) => {
              event.preventDefault();
              event.dataTransfer.dropEffect = "move";
              setDragOverGroupId(selectedGroup.id);
            } : undefined}
            onDragLeave={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                setDragOverGroupId(null);
              }
            }}
            onDrop={selectedGroup ? (event) => handleDropOnGroup(event, selectedGroup.id) : undefined}
          >
            {draggedAccountId != null && selectedGroup && dragOverGroupId === selectedGroup.id && (
              <div className="mb-4 rounded-xl border border-dashed border-amber-300/60 bg-amber-300/10 px-4 py-2 text-[12px] font-semibold text-amber-200">
                Rilascia per aggiungere il canale a «{selectedGroup.name}»
              </div>
            )}
            {selectedAccount ? (
              <AccountDetailPanel
                account={selectedAccount}
                onClose={() => setSelectedAccountId(null)}
                onUpdated={() => void load()}
              />
            ) : selectedGroup ? (
              <GroupDetailPanel
                group={selectedGroup}
                onPickAccount={(id) => {
                  setSelectedAccountId(id);
                  navigate(`/app/dashboard-channels/${id}`);
                }}
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
                onSaved={() => load()}
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

        {state.kind === "ready" && ungroupedAccounts.length > 0 && (
          <UngroupedChannelsSection
            accounts={ungroupedAccounts}
            groups={allGroups}
            busyAccountId={busyAccountId}
            draggedAccountId={draggedAccountId}
            onDragStart={setDraggedAccountId}
            onDragEnd={() => {
              setDraggedAccountId(null);
              setDragOverGroupId(null);
            }}
            onAssign={(accountId, groupId) => void assignAccountToGroup(accountId, groupId)}
          />
        )}
      </div>
    </div>
  );
}

function UngroupedChannelsSection({
  accounts,
  groups,
  busyAccountId,
  draggedAccountId,
  onDragStart,
  onDragEnd,
  onAssign,
}: {
  accounts: PlatformAccount[];
  groups: TreeNode[];
  busyAccountId: number | null;
  draggedAccountId: number | null;
  onDragStart: (accountId: number | null) => void;
  onDragEnd: () => void;
  onAssign: (accountId: number, groupId: number) => void;
}) {
  const dragging = draggedAccountId != null;
  return (
    <section
      className="mt-6 rounded-2xl border border-white/[0.08] bg-[#0b0c12] p-5 shadow-[0_18px_60px_rgba(0,0,0,0.18)]"
      data-testid="ungrouped-section"
    >
      <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <h2 className="flex items-center gap-2 text-[14px] font-bold uppercase tracking-wider text-white">
          <Inbox size={16} className="text-white/40" />
          Canali senza gruppo
          <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[11px] font-semibold tabular-nums text-[#9aa0aa]">
            {accounts.length}
          </span>
        </h2>
        <p
          className={cn(
            "text-[12px] transition-colors",
            dragging ? "text-amber-300" : "text-[#9aa0aa]",
          )}
        >
          {dragging
            ? "Rilascia su una cartella qui sopra per assegnare il canale."
            : "Trascina un canale su una cartella per assegnarlo, oppure usa il menu a tendina."}
        </p>
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {accounts.map((account) => (
          <UngroupedChannelCard
            key={account.id}
            account={account}
            groups={groups}
            busy={busyAccountId === account.id}
            dragging={draggedAccountId === account.id}
            onDragStart={() => onDragStart(account.id)}
            onDragEnd={onDragEnd}
            onAssign={onAssign}
          />
        ))}
      </div>
    </section>
  );
}

function UngroupedChannelCard({
  account,
  groups,
  busy,
  dragging,
  onDragStart,
  onDragEnd,
  onAssign,
}: {
  account: PlatformAccount;
  groups: TreeNode[];
  busy: boolean;
  dragging: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onAssign: (accountId: number, groupId: number) => void;
}) {
  const [selectedGroup, setSelectedGroup] = useState("");
  const grad = PLATFORM_GRADIENT[account.platform] ?? "from-zinc-500 to-zinc-700";
  const label = account.username || account.platform_user_id || `canale #${account.id}`;
  return (
    <div
      draggable={!busy}
      onDragStart={(event) => {
        event.dataTransfer.setData("text/plain", String(account.id));
        event.dataTransfer.effectAllowed = "move";
        onDragStart();
      }}
      onDragEnd={onDragEnd}
      data-account-id={account.id}
      className={cn(
        "flex items-center gap-2 rounded-xl border border-white/[0.10] bg-white/[0.04] p-2.5 transition-all",
        busy && "opacity-60 pointer-events-none",
        dragging && "opacity-40 border-amber-300/50 scale-[0.98]",
        !busy && "hover:border-white/[0.20] hover:bg-white/[0.07]",
      )}
    >
      <GripVertical
        size={14}
        className={cn("shrink-0 text-[#9aa0aa]", !busy && "cursor-grab active:cursor-grabbing")}
      />
      <div
        className={cn(
          "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br text-[13px] font-extrabold text-white",
          grad,
        )}
      >
        {(account.platform[0] ?? "?").toUpperCase()}
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[12px] font-semibold text-white">{label}</p>
        <p className="truncate text-[10px] uppercase tracking-wider text-[#9aa0aa]">{account.platform}</p>
      </div>
      {busy ? (
        <RefreshCw size={13} className="animate-spin text-amber-300" aria-label="Assegnazione in corso" />
      ) : (
        <select
          aria-label={`Assegna ${label} a una cartella`}
          value={selectedGroup}
          onChange={(event) => {
            const groupId = Number(event.target.value);
            setSelectedGroup("");
            if (groupId > 0) onAssign(account.id, groupId);
          }}
          className="max-w-[130px] rounded-md border border-white/[0.10] bg-black/30 px-1.5 py-1 text-[10px] text-[#c8cbd4] outline-none"
        >
          <option value="">Cartella…</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>{group.name}</option>
          ))}
        </select>
      )}
    </div>
  );
}

function flattenTree(nodes: import("./groupsTypes").TreeNode[]): import("./groupsTypes").TreeNode[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)]);
}
