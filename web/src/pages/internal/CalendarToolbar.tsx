import type { ElementType } from "react";
import {
  Calendar as CalendarIcon,
  ChevronLeft,
  ChevronRight,
  Clock,
  LayoutGrid,
  X,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { type CalendarViewMode } from "./CalendarGrid";
import type { CalendarGroup } from "./calendarTypes";

const viewTabs: { id: CalendarViewMode; label: string; icon: ElementType }[] = [
  { id: "month", label: "Mese", icon: CalendarIcon },
  { id: "week", label: "Settimana", icon: LayoutGrid },
  { id: "day", label: "Giorno", icon: Clock },
];

const statusOptions = [
  { value: "all", label: "Tutti gli stati" },
  { value: "draft", label: "Bozza" },
  { value: "queued", label: "Programmato" },
  { value: "publishing", label: "In pubblicazione" },
  { value: "published", label: "Pubblicato" },
  { value: "failed", label: "Fallito" },
];

export function CalendarToolbar({
  view,
  setView,
  shiftDate,
  setCurrentDate,
  formattedDate,
  statusFilter,
  setStatusFilter,
  groupFilter,
  setGroupFilter,
  groups,
  hasActiveFilters,
  clearFilters,
}: {
  view: CalendarViewMode;
  setView: (view: CalendarViewMode) => void;
  shiftDate: (delta: number) => void;
  setCurrentDate: (date: Date) => void;
  formattedDate: string;
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
        <button
          type="button"
          onClick={() => shiftDate(-1)}
          className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
          aria-label="Precedente"
        >
          <ChevronLeft size={18} />
        </button>
        <button
          type="button"
          onClick={() => setCurrentDate(new Date())}
          className="px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
        >
          Oggi
        </button>
        <button
          type="button"
          onClick={() => shiftDate(1)}
          className="p-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-white hover:bg-white/[0.08] transition-colors"
          aria-label="Successivo"
        >
          <ChevronRight size={18} />
        </button>
        <h2 className="ml-2 text-[16px] sm:text-[18px] font-bold text-white min-w-[140px]">
          {formattedDate}
        </h2>
      </div>

      <div className="flex items-center gap-2">
        <div className="inline-flex p-1 rounded-xl bg-white/[0.04] border border-white/[0.08]">
          {viewTabs.map((tab) => {
            const Icon = tab.icon;
            const active = view === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                onClick={() => setView(tab.id)}
                className={cn(
                  "flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all",
                  active
                    ? "bg-white/[0.08] text-white shadow-[inset_0_1px_0_0_rgba(255,255,255,0.1)]"
                    : "text-[#9aa0aa] hover:text-white hover:bg-white/[0.04]",
                )}
              >
                <Icon size={14} />
                <span className="hidden sm:inline">{tab.label}</span>
              </button>
            );
          })}
        </div>

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
