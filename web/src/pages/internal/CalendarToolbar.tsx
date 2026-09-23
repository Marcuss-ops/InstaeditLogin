import { ChevronLeft, ChevronRight, X } from "lucide-react";
import type { CalendarGroup } from "./calendarTypes";

const statusOptions = [
  { value: "all", label: "Tutti gli stati" },
  { value: "draft", label: "Bozza" },
  { value: "queued", label: "Programmato" },
  { value: "publishing", label: "In pubblicazione" },
  { value: "published", label: "Pubblicato" },
  { value: "failed", label: "Fallito" },
];

export function CalendarToolbar({
  formattedDate,
  onPrevious,
  onNext,
  onToday,
  statusFilter,
  setStatusFilter,
  groupFilter,
  setGroupFilter,
  groups,
  hasActiveFilters,
  clearFilters,
}: {
  formattedDate: string;
  onPrevious: () => void;
  onNext: () => void;
  onToday: () => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  groupFilter: string;
  setGroupFilter: (value: string) => void;
  groups: CalendarGroup[];
  hasActiveFilters: boolean;
  clearFilters: () => void;
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between mb-4 shrink-0">
      <div className="flex items-center gap-2">
        <div>
          <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-[#8f9299]">Calendario · prossimi 30 giorni</p>
          <h2 className="mt-1 text-[16px] sm:text-[18px] font-bold text-white">{formattedDate}</h2>
        </div>
        <div className="ml-2 flex items-center rounded-xl border border-white/[0.08] bg-white/[0.04]">
          <button type="button" onClick={onPrevious} aria-label="Mese precedente" className="rounded-l-xl p-2 text-white/65 hover:bg-white/10 hover:text-white"><ChevronLeft size={16} /></button>
          <button type="button" onClick={onToday} className="border-x border-white/[0.08] px-3 py-2 text-xs font-semibold text-white/70 hover:bg-white/10 hover:text-white">Oggi</button>
          <button type="button" onClick={onNext} aria-label="Mese successivo" className="rounded-r-xl p-2 text-white/65 hover:bg-white/10 hover:text-white"><ChevronRight size={16} /></button>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <div className="flex items-center gap-2">
          <select
            data-testid="calendar-filter-status"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-white focus:outline-none focus:border-white/[0.20]"
            aria-label="Filtra per stato"
          >
            {statusOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          {groups.length > 0 && (
            <select
              data-testid="calendar-filter-group"
              value={groupFilter}
              onChange={(e) => setGroupFilter(e.target.value)}
              className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-white focus:outline-none focus:border-white/[0.20]"
              aria-label="Seleziona gruppo"
            >
              <option value="all">Tutti i gruppi</option>
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
            </select>
          )}
          {hasActiveFilters && (
            <button
              type="button"
              data-testid="calendar-filter-clear"
              onClick={clearFilters}
              className="inline-flex items-center gap-1.5 px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-[#9aa0aa] hover:text-white hover:bg-white/[0.08] transition-colors"
              aria-label="Cancella filtri"
            >
              <X size={14} /> Cancella
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
