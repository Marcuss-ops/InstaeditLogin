import { useEffect, useState, type DragEvent } from "react";
import {
  Check,
  Folder,
  FolderPlus,
  GripVertical,
  Inbox,
  Plus,
  RefreshCw,
  Search,
  Square,
  X,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { authedFetch } from "../../lib/auth";
import { ErrorState, EmptyState, Skeleton } from "../../components/feedback";
import { useGroupsData } from "./useGroupsData";
import { TreeView } from "./GroupsTreeView";
import { AccountDetailPanel, GroupDetailPanel } from "./GroupsDetailPanels";
import {
  type PlatformAccount,
} from "./groupsTypes";
import { cn } from "../../lib/utils";
import { ProviderBadge } from "../../components/brand/PlatformLogos";

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
  const [batchBusy, setBatchBusy] = useState(false);

  const allGroups = flattenTree(tree);
  const assignedToAnyGroup = state.kind === "ready"
    ? new Set(Array.from(state.groupAccountIDs.values()).flat())
    : new Set<number>();
  const filteredYouTubeAccounts = availableYouTubeAccounts.filter((account) => (
    matchesChannelView(account, channelSearch, channelFilter, assignedToAnyGroup)
  ));
  const visibleSelectedIDs = filteredYouTubeAccounts
    .filter((account) => selectedChannelIds.has(account.id))
    .map((account) => account.id);
  const visibleSelectedCount = visibleSelectedIDs.length;

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

  const runBatchMembership = async (mode: "add" | "remove") => {
    if (selectedGroupId == null || visibleSelectedIDs.length === 0 || batchBusy) return;
    setBatchBusy(true);
    try {
      const selectedIDs = new Set(visibleSelectedIDs);
      await setGroupAccounts(selectedGroupId, (currentIDs) => mode === "add"
        ? [...currentIDs, ...Array.from(selectedIDs)]
        : currentIDs.filter((id) => !selectedIDs.has(id))
      );
      clearChannelSelection();
    } finally {
      setBatchBusy(false);
    }
  };

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
                onSaved={() => load(false, true)}
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
            accounts={filteredYouTubeAccounts}
            totalAccounts={availableYouTubeAccounts.length}
            search={channelSearch}
            filter={channelFilter}
            selectedIds={selectedChannelIds}
            selectedGroupName={selectedGroup?.name ?? null}
            selectedGroupId={selectedGroupId}
            visibleSelectedCount={visibleSelectedCount}
            batchBusy={batchBusy}
            busyAccountId={busyAccountId}
            draggedAccountId={draggedAccountId}
            allVisibleSelected={allVisibleSelected}
            onSearchChange={handleSearchChange}
            onFilterChange={handleFilterChange}
            onToggleSelection={toggleChannelSelection}
            onSelectAll={toggleSelectAllVisibleChannels}
            onClearSelection={clearChannelSelection}
            onBatchAdd={() => void runBatchMembership("add")}
            onBatchRemove={() => void runBatchMembership("remove")}
            onDragStart={setDraggedAccountId}
            onDragEnd={() => {
              setDraggedAccountId(null);
              setDragOverGroupId(null);
            }}
          />
        )}
      </div>
    </div>
  );
}

function YouTubeChannelsTray({
  accounts,
  totalAccounts,
  search,
  filter,
  selectedIds,
  selectedGroupName,
  selectedGroupId,
  visibleSelectedCount,
  allVisibleSelected,
  batchBusy,
  busyAccountId,
  draggedAccountId,
  onSearchChange,
  onFilterChange,
  onToggleSelection,
  onSelectAll,
  onClearSelection,
  onBatchAdd,
  onBatchRemove,
  onDragStart,
  onDragEnd,
}: {
  accounts: PlatformAccount[];
  totalAccounts: number;
  search: string;
  filter: "all" | "assigned" | "unassigned";
  selectedIds: Set<number>;
  selectedGroupName: string | null;
  selectedGroupId: number | null;
  visibleSelectedCount: number;
  allVisibleSelected: boolean;
  batchBusy: boolean;
  busyAccountId: number | null;
  draggedAccountId: number | null;
  onSearchChange: (value: string) => void;
  onFilterChange: (value: "all" | "assigned" | "unassigned") => void;
  onToggleSelection: (accountId: number) => void;
  onSelectAll: () => void;
  onClearSelection: () => void;
  onBatchAdd: () => void;
  onBatchRemove: () => void;
  onDragStart: (accountId: number | null) => void;
  onDragEnd: () => void;
}) {
  const dragging = draggedAccountId != null;
  const emptyState = totalAccounts === 0
    ? {
        title: "Nessun canale YouTube disponibile",
        description: "Collega un canale YouTube per poterlo organizzare nei gruppi.",
      }
    : search.trim()
      ? {
          title: "Nessun canale trovato",
          description: `Nessun canale corrisponde a «${search.trim()}». Prova con un altro nome o ID.`,
        }
      : filter === "assigned"
        ? {
            title: "Nessun canale nei gruppi",
            description: "Non ci sono ancora canali assegnati a nessun gruppo.",
          }
        : filter === "unassigned"
          ? {
              title: "Tutti i canali sono già nei gruppi",
              description: "Ogni canale YouTube disponibile è già organizzato in almeno un gruppo.",
            }
          : {
              title: "Nessun canale da mostrare",
              description: "Non ci sono canali che corrispondono al filtro corrente.",
            };

  return (
    <section
      className="mt-6 rounded-2xl border border-white/[0.08] bg-[#0b0c12] p-5 shadow-[0_18px_60px_rgba(0,0,0,0.18)]"
      data-testid="youtube-channels-tray"
    >
      <div className="mb-4 flex flex-col gap-3">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="flex items-center gap-2 text-[14px] font-bold uppercase tracking-wider text-white">
            <Inbox size={16} className="text-white/40" />
            Tutti i canali YouTube
            <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[11px] font-semibold tabular-nums text-[#9aa0aa]">
              {totalAccounts}
            </span>
            {accounts.length !== totalAccounts ? <span className="text-[11px] font-normal normal-case tracking-normal text-[#9aa0aa]">({accounts.length} mostrati)</span> : null}
          </h2>
          <p className={cn("text-[12px] transition-colors", dragging ? "text-amber-300" : "text-[#9aa0aa]")}>
            {dragging ? "Rilascia su una cartella qui sopra per aggiungere il canale." : "Trascina o seleziona più canali per gestirli insieme."}
          </p>
        </div>
        <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
          <label className="relative min-w-0 flex-1">
            <Search size={15} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa]" />
            <input
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder="Cerca per nome o ID canale…"
              aria-label="Cerca canali"
              className="w-full rounded-xl border border-white/[0.10] bg-black/20 py-2.5 pl-9 pr-9 text-[12px] text-white outline-none transition focus:border-violet-400/60 focus:ring-2 focus:ring-violet-500/20"
            />
            {search ? <button type="button" onClick={() => onSearchChange("")} aria-label="Cancella ricerca" className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-[#9aa0aa] hover:text-white"><X size={14} /></button> : null}
          </label>
          <select value={filter} onChange={(event) => onFilterChange(event.target.value as "all" | "assigned" | "unassigned")} aria-label="Filtra canali" className="rounded-xl border border-white/[0.10] bg-black/20 px-3 py-2.5 text-[12px] text-white outline-none focus:border-violet-400/60">
            <option value="all">Tutti i canali</option>
            <option value="assigned">Nei gruppi</option>
            <option value="unassigned">Non assegnati</option>
          </select>
          <button type="button" onClick={onSelectAll} disabled={accounts.length === 0} aria-label={allVisibleSelected ? "Deseleziona tutti" : "Seleziona tutti"} className="inline-flex items-center justify-center gap-1.5 rounded-xl border border-white/[0.10] bg-white/[0.04] px-3 py-2.5 text-[12px] font-semibold text-white hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-40">
            {allVisibleSelected ? <X size={14} /> : <Check size={14} />} {allVisibleSelected ? "Deseleziona tutti" : "Seleziona tutti"}
          </button>
        </div>
        {visibleSelectedCount > 0 ? (
          <div className="flex flex-col gap-2 rounded-xl border border-violet-400/30 bg-violet-500/[0.10] p-3 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-[12px] font-semibold text-violet-100">{visibleSelectedCount} canali selezionati{selectedGroupName ? <> · gruppo: {selectedGroupName}</> : null}</p>
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" onClick={onBatchAdd} disabled={selectedGroupId == null || batchBusy} className="rounded-lg bg-white px-3 py-2 text-[12px] font-bold text-black disabled:cursor-not-allowed disabled:opacity-40">{batchBusy ? "Salvataggio…" : "Aggiungi al gruppo"}</button>
              <button type="button" onClick={onBatchRemove} disabled={selectedGroupId == null || batchBusy} className="rounded-lg border border-red-300/30 bg-red-500/10 px-3 py-2 text-[12px] font-semibold text-red-200 disabled:cursor-not-allowed disabled:opacity-40">Rimuovi dal gruppo</button>
              <button type="button" onClick={onClearSelection} disabled={batchBusy} aria-label="Deseleziona canali" className="rounded-lg p-2 text-[#c7c9d1] hover:bg-white/[0.08] hover:text-white disabled:opacity-40"><X size={15} /></button>
            </div>
          </div>
        ) : null}
      </div>
      {accounts.length === 0 ? (
        <div
          className="rounded-xl border border-dashed border-white/[0.12] bg-white/[0.02] px-4 py-8 text-center"
          data-testid="youtube-channels-empty"
        >
          <p className="text-[13px] font-semibold text-white">{emptyState.title}</p>
          <p className="mt-1 text-[12px] text-[#9aa0aa]">{emptyState.description}</p>
        </div>
      ) : null}
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {accounts.map((account) => (
          <YouTubeChannelCard
            key={account.id}
            account={account}
            busy={busyAccountId === account.id}
            selected={selectedIds.has(account.id)}
            dragging={draggedAccountId === account.id}
            onToggleSelect={() => onToggleSelection(account.id)}
            onDragStart={() => onDragStart(account.id)}
            onDragEnd={onDragEnd}
          />
        ))}
      </div>
    </section>
  );
}

function YouTubeChannelCard({
  account,
  busy,
  selected,
  dragging,
  onToggleSelect,
  onDragStart,
  onDragEnd,
}: {
  account: PlatformAccount;
  busy: boolean;
  selected: boolean;
  dragging: boolean;
  onToggleSelect: () => void;
  onDragStart: () => void;
  onDragEnd: () => void;
}) {
  const [imageFailed, setImageFailed] = useState(false);
  const label = account.username || account.platform_user_id || `canale #${account.id}`;
  const initial = (account.username || account.platform_user_id || "?").charAt(0).toUpperCase();
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
      <button
        type="button"
        onClick={onToggleSelect}
        aria-label={`${selected ? "Deseleziona" : "Seleziona"} ${label}`}
        aria-pressed={selected}
        className={cn("flex h-5 w-5 shrink-0 items-center justify-center rounded border transition-colors", selected ? "border-violet-300 bg-violet-500 text-white" : "border-white/20 bg-black/20 text-transparent hover:border-white/50")}
      >
        {selected ? <Check size={13} /> : <Square size={12} />}
      </button>
      <GripVertical
        size={14}
        className={cn("shrink-0 text-[#9aa0aa]", !busy && "cursor-grab active:cursor-grabbing")}
      />
      <div className="relative flex h-9 w-9 shrink-0 items-center justify-center">
        <div className="absolute inset-0 overflow-hidden rounded-full bg-white/[0.04] ring-1 ring-white/[0.12]">
        {account.avatar_url && !imageFailed ? (
          <img
            src={account.avatar_url}
            alt={`${label} avatar`}
            className="h-full w-full object-cover"
            loading="lazy"
            decoding="async"
            referrerPolicy="no-referrer"
            onError={() => setImageFailed(true)}
          />
        ) : (
          <span aria-hidden="true" className="flex h-full w-full items-center justify-center bg-white/[0.06] text-[13px] font-extrabold text-white">{initial}</span>
        )}
        </div>
        <ProviderBadge
          platform={account.platform}
          className="relative h-4 w-4 translate-x-3 translate-y-3 justify-center rounded-[4px] border-0 shadow-md"
          compact
          logoClassName="h-4 w-4"
        />
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[12px] font-semibold text-white">{label}</p>
        <p className="truncate text-[10px] uppercase tracking-wider text-[#9aa0aa]">{account.platform === "youtube" ? "YouTube" : account.platform}</p>
      </div>
      {busy ? (
        <RefreshCw size={13} className="animate-spin text-amber-300" aria-label="Assegnazione in corso" />
      ) : (
        <GripVertical size={14} className="shrink-0 text-[#9aa0aa]" aria-hidden="true" />
      )}
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
