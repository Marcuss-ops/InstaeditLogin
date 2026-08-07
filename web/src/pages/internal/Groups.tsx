import { useEffect, useState, type DragEvent } from "react";
import {
  Folder,
  Plus,
  RefreshCw,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { authedFetch } from "../../lib/auth";
import { useGroupsData } from "./useGroupsData";
import { AccountDetailPanel, GroupDetailPanel } from "./GroupsDetailPanels";
import { groupAccent } from "./groupAccent";
import { type PlatformAccount } from "./groupsTypes";
import { cn } from "../../lib/utils";
import { YouTubeChannelsTray } from "./YouTubeChannelsTray";

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
    setGroupAccounts,
    renameGroup,
    availableYouTubeAccounts,
    tree,
    selectedGroup,
    selectedAccount,
  } = useGroupsData();

  // Drag & drop state: which channel is being dragged and which group chip
  // it is currently hovering over (for the highlight ring).
  const [draggedAccountId, setDraggedAccountId] = useState<number | null>(null);
  const [dragOverGroupId, setDragOverGroupId] = useState<number | null>(null);
  const [channelSearch, setChannelSearch] = useState("");
  const [channelFilter, setChannelFilter] = useState<"all" | "assigned" | "unassigned">("all");
  const [selectedChannelIds, setSelectedChannelIds] = useState<Set<number>>(new Set());
  const [draggedChannelIds, setDraggedChannelIds] = useState<number[]>([]);

  const allGroups = flattenTree(tree);
  const assignedToAnyGroup = state.kind === "ready"
    ? new Set(Array.from(state.groupAccountIDs.values()).flat())
    : new Set<number>();
  // accountId -> group names the channel belongs to. Shown as small badges
  // on the tray cards so an operator can see at a glance which folder(s)
  // each channel lives in (a card without badges is not in any group).
  const groupNamesByAccountId = new Map<number, string[]>();
  for (const node of allGroups) {
    for (const account of node.accounts) {
      const names = groupNamesByAccountId.get(account.id) ?? [];
      if (!names.includes(node.name)) names.push(node.name);
      groupNamesByAccountId.set(account.id, names);
    }
  }
  const filteredYouTubeAccounts = availableYouTubeAccounts.filter((account) => (
    matchesChannelView(account, channelSearch, channelFilter, assignedToAnyGroup)
  ));
  // Tray order: channels that already live in a group first, free ones
  // last (stable sort keeps the source order inside each bucket), so the
  // operator always sees the assigned channels and the assignable ones at
  // a glance.
  const trayAccounts = [...filteredYouTubeAccounts].sort(
    (a, b) => Number(assignedToAnyGroup.has(b.id)) - Number(assignedToAnyGroup.has(a.id)),
  );
  const visibleSelectedIDs = filteredYouTubeAccounts
    .filter((account) => selectedChannelIds.has(account.id))
    .map((account) => account.id);
  const visibleSelectedCount = visibleSelectedIDs.length;
  // Channels that can be added to the open group directly from the detail
  // panel (everything publishable that is not already a member).
  const addableAccounts = selectedGroup
    ? availableYouTubeAccounts.filter((account) => !selectedGroup.accounts.some((member) => member.id === account.id))
    : [];

  useEffect(() => {
    const availableIDs = new Set(availableYouTubeAccounts.map((account) => account.id));
    setSelectedChannelIds((current) => {
      const next = new Set(Array.from(current).filter((id) => availableIDs.has(id)));
      if (next.size === current.size) return current;
      return next;
    });
  }, [availableYouTubeAccounts]);

  const toggleChannelSelection = (accountId: number) => {
    setSelectedChannelIds((current) => {
      const next = new Set(current);
      if (next.has(accountId)) next.delete(accountId);
      else next.add(accountId);
      return next;
    });
  };

  const retainVisibleSelection = (nextVisibleAccounts: PlatformAccount[]) => {
    const visibleIDs = new Set(nextVisibleAccounts.map((account) => account.id));
    setSelectedChannelIds((current) => new Set(Array.from(current).filter((id) => visibleIDs.has(id))));
  };

  const handleSearchChange = (value: string) => {
    setChannelSearch(value);
    retainVisibleSelection(availableYouTubeAccounts.filter((account) => (
      matchesChannelView(account, value, channelFilter, assignedToAnyGroup)
    )));
  };

  const handleFilterChange = (value: "all" | "assigned" | "unassigned") => {
    setChannelFilter(value);
    retainVisibleSelection(availableYouTubeAccounts.filter((account) => (
      matchesChannelView(account, channelSearch, value, assignedToAnyGroup)
    )));
  };

  const allVisibleSelected = filteredYouTubeAccounts.length > 0
    && visibleSelectedCount === filteredYouTubeAccounts.length;

  const toggleSelectAllVisibleChannels = () => {
    const visibleIDs = new Set(filteredYouTubeAccounts.map((account) => account.id));
    setSelectedChannelIds((current) => {
      if (allVisibleSelected) {
        return new Set(Array.from(current).filter((id) => !visibleIDs.has(id)));
      }
      return new Set(visibleIDs);
    });
  };

  const clearChannelSelection = () => setSelectedChannelIds(new Set());

  const handleDropOnGroup = (event: DragEvent, groupId: number) => {
    event.preventDefault();
    setDragOverGroupId(null);
    const rawPayload = event.dataTransfer.getData("application/x-instaedit-channel-ids")
      || event.dataTransfer.getData("text/plain");
    let droppedIDs: number[] = [];
    try {
      const parsed = JSON.parse(rawPayload) as unknown;
      if (Array.isArray(parsed)) droppedIDs = parsed.map(Number).filter((id) => Number.isInteger(id) && id > 0);
    } catch {
      const accountId = Number(rawPayload);
      if (Number.isInteger(accountId) && accountId > 0) droppedIDs = [accountId];
    }
    if (droppedIDs.length === 0) droppedIDs = draggedChannelIds;
    const accountIDs = Array.from(new Set(droppedIDs));
    setDraggedAccountId(null);
    setDraggedChannelIds([]);
    if (accountIDs.length === 0) return;
    if (accountIDs.length === 1) {
      void assignAccountToGroup(accountIDs[0], groupId);
      clearChannelSelection();
      return;
    }
    void setGroupAccounts(groupId, (currentIDs) => Array.from(new Set([...currentIDs, ...accountIDs]))).then(clearChannelSelection);
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
              Organizza tutti i tuoi canali YouTube nei gruppi. Clicca un gruppo
              per vedere i canali, poi trascina un canale su una cartella per
              aggiungerlo anche a quel gruppo.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load(false, true)}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        <div className="mb-6 flex flex-col gap-3 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-3 sm:flex-row sm:items-center">
          <div className="scrollbar-none flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
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
                <span className={`flex items-center gap-1.5 truncate ${selectedGroupId === node.id ? "text-[17px] font-bold" : "text-[13px] font-medium"}`}>
                  <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: groupAccent(node.name).text }} />
                  <span className="truncate">{node.name}</span>
                </span>
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
                // Silent reload after Reconnect/Validate: the account panel
                // stays mounted (no loading flash, no loss of view).
                onUpdated={() => void load(true)}
              />
            ) : selectedGroup ? (
              <GroupDetailPanel
                group={selectedGroup}
                groupNamesByAccountId={groupNamesByAccountId}
                onPickAccount={(id) => {
                  setSelectedAccountId(id);
                  navigate(`/app/dashboard-channels/${id}`);
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
                // Silent reload: keep the panel mounted in the current group
                // while the fresh account manifest confirms the saved language.
                // A full (non-silent) reload flashes the group to the loading
                // placeholder and throws the operator back to the group list.
                availableAccounts={addableAccounts}
                onAddAccounts={(accountIds) => {
                  void setGroupAccounts(
                    selectedGroup.id,
                    (currentIDs) => Array.from(new Set([...currentIDs, ...accountIds])),
                  );
                }}
                onSaved={() => load(true, true)}
                onRename={(name) => renameGroup(selectedGroup.id, name)}
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

        {state.kind === "ready" && (
          <YouTubeChannelsTray
            accounts={trayAccounts}
            totalAccounts={availableYouTubeAccounts.length}
            search={channelSearch}
            filter={channelFilter}
            selectedIds={selectedChannelIds}
            visibleSelectedCount={visibleSelectedCount}
            busyAccountId={busyAccountId}
            draggedAccountId={draggedAccountId}
            allVisibleSelected={allVisibleSelected}
            groupNamesByAccountId={groupNamesByAccountId}
            onSearchChange={handleSearchChange}
            onFilterChange={handleFilterChange}
            onToggleSelection={toggleChannelSelection}
            onSelectAll={toggleSelectAllVisibleChannels}
            onClearSelection={clearChannelSelection}
            onDragStart={(accountId, ids) => {
              setDraggedAccountId(accountId);
              setDraggedChannelIds(ids);
            }}
            onDragEnd={() => {
              setDraggedAccountId(null);
              setDraggedChannelIds([]);
              setDragOverGroupId(null);
            }}
          />
        )}
      </div>
    </div>
  );
}

function matchesChannelView(
  account: PlatformAccount,
  search: string,
  filter: "all" | "assigned" | "unassigned",
  assignedToAnyGroup: Set<number>,
): boolean {
  const normalizedSearch = search.trim().toLocaleLowerCase();
  const matchesSearch = !normalizedSearch || [account.username, account.platform_user_id]
    .some((value) => value.toLocaleLowerCase().includes(normalizedSearch));
  const assigned = assignedToAnyGroup.has(account.id);
  const matchesFilter = filter === "all" || (filter === "assigned" ? assigned : !assigned);
  return matchesSearch && matchesFilter;
}

function flattenTree(nodes: import("./groupsTypes").TreeNode[]): import("./groupsTypes").TreeNode[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)]);
}
