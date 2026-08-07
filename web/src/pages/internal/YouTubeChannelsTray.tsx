import { useState } from "react";
import {
  Check,
  GripVertical,
  Inbox,
  Plus,
  RefreshCw,
  Search,
  Square,
  X,
} from "lucide-react";
import { API_BASE_URL } from "../../lib/api";
import { GroupBadges } from "./GroupBadges";
import { type PlatformAccount } from "./groupsTypes";
import { cn } from "../../lib/utils";
import { ProviderBadge } from "../../components/brand/PlatformLogos";

export function YouTubeChannelsTray({
  accounts,
  totalAccounts,
  search,
  filter,
  selectedIds,
  visibleSelectedCount,
  onDragStart,
  allVisibleSelected,
  busyAccountId,
  draggedAccountId,
  groupNamesByAccountId,
  onSearchChange,
  onFilterChange,
  onToggleSelection,
  onSelectAll,
  onClearSelection,
  onDragEnd,
}: {
  accounts: PlatformAccount[];
  totalAccounts: number;
  search: string;
  filter: "all" | "assigned" | "unassigned";
  selectedIds: Set<number>;
  visibleSelectedCount: number;
  onDragStart: (accountId: number, ids: number[]) => void;
  allVisibleSelected: boolean;
  busyAccountId: number | null;
  draggedAccountId: number | null;
  groupNamesByAccountId: Map<number, string[]>;
  onSearchChange: (value: string) => void;
  onFilterChange: (value: "all" | "assigned" | "unassigned") => void;
  onToggleSelection: (accountId: number) => void;
  onSelectAll: () => void;
  onClearSelection: () => void;
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
            {dragging ? "Rilascia su un gruppo per aggiungere tutti i canali selezionati." : "Clicca le card per selezionarle, poi trascinane una nel gruppo desiderato."}
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
          <a
            href={`${API_BASE_URL}/api/v1/auth/youtube/login?mode=add&redirect=/app/groups`}
            data-testid="groups-add-channel"
            className="inline-flex items-center justify-center gap-1.5 rounded-xl border border-white/[0.10] bg-white/[0.04] px-3 py-2.5 text-[12px] font-semibold text-white no-underline transition-colors hover:bg-white/[0.08]"
            title="Collega un nuovo canale YouTube"
          >
            <Plus size={14} aria-hidden="true" /> Aggiungi canale
          </a>
        </div>
        {visibleSelectedCount > 0 ? (
          <div className="flex flex-col gap-2 rounded-xl border border-violet-400/30 bg-violet-500/[0.08] p-3 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-[12px] font-semibold text-violet-100">
              {dragging ? "Rilascia su un gruppo" : `${visibleSelectedCount} canali selezionati`}
            </p>
            <button type="button" onClick={onClearSelection} aria-label="Deseleziona canali" className="inline-flex shrink-0 items-center gap-1 rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1 text-[12px] font-semibold text-white transition hover:bg-white/[0.08]">
              <X size={14} /> Deseleziona
            </button>
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
            selectedIds={selectedIds}
            dragging={draggedAccountId === account.id}
            groupNames={groupNamesByAccountId.get(account.id) ?? []}
            onToggleSelect={() => onToggleSelection(account.id)}
            onDragStart={(ids) => onDragStart(account.id, ids)}
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
  selectedIds,
  dragging,
  groupNames,
  onToggleSelect,
  onDragStart,
  onDragEnd,
}: {
  account: PlatformAccount;
  busy: boolean;
  selected: boolean;
  selectedIds: Set<number>;
  dragging: boolean;
  groupNames: string[];
  onToggleSelect: () => void;
  onDragStart: (ids: number[]) => void;
  onDragEnd: () => void;
}) {
  const [imageFailed, setImageFailed] = useState(false);
  const label = account.username || account.platform_user_id || `canale #${account.id}`;
  const initial = (account.username || account.platform_user_id || "?").charAt(0).toUpperCase();
  return (
    <div
      draggable={!busy}
      onDragStart={(event) => {
        const ids = selected ? Array.from(selectedIds) : [account.id];
        const payload = JSON.stringify(ids);
        event.dataTransfer.setData("application/x-instaedit-channel-ids", payload);
        event.dataTransfer.setData("text/plain", payload);
        event.dataTransfer.effectAllowed = "move";
        onDragStart(ids);
      }}
      onDragEnd={onDragEnd}
      onClick={onToggleSelect}
      data-account-id={account.id}
      className={cn(
        "group flex cursor-pointer items-center gap-2 rounded-2xl border bg-white/[0.04] p-2.5 transition-all duration-200",
        selected ? "border-violet-300/70 bg-violet-500/[0.08]" : "border-white/[0.10]",
        busy && "pointer-events-none opacity-60",
        dragging && "scale-[0.98] opacity-40 ring-2 ring-amber-300/50",
        !busy && "hover:-translate-y-0.5 hover:border-violet-300/40 hover:bg-white/[0.07]",
      )}
    >
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onToggleSelect();
        }}
        aria-label={`${selected ? "Deseleziona" : "Seleziona"} ${label}`}
        aria-pressed={selected}
        className={cn("flex h-5 w-5 shrink-0 items-center justify-center rounded-lg border transition-all", selected ? "border-violet-300 bg-violet-500 text-white" : "border-white/20 bg-black/20 text-transparent hover:border-white/50")}
      >
        {selected ? <Check size={13} /> : <Square size={12} />}
      </button>
      <GripVertical size={14} className={cn("shrink-0 text-[#9aa0aa] transition-colors", !busy && "cursor-grab text-white/35 group-hover:text-violet-200")} />
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
        <p className="truncate text-[14px] font-semibold text-white">{label}</p>
        <p className="truncate text-[10px] uppercase tracking-wider text-[#9aa0aa]">{account.platform === "youtube" ? "YouTube" : account.platform}</p>
        <GroupBadges names={groupNames} className="mt-1" />
      </div>
      {busy ? (
        <RefreshCw size={13} className="animate-spin text-amber-300" aria-label="Assegnazione in corso" />
      ) : null}
    </div>
  );
}
