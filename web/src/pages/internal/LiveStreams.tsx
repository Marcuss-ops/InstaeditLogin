import { useState } from "react";
import {
  Activity,
  AlertTriangle,
  CalendarClock,
  Loader2,
  Plus,
  Radio,
  WifiOff,
} from "lucide-react";
import { EmptyState } from "../../components/feedback/EmptyState";
import { ErrorState, Skeleton } from "../../components/feedback";
import { toastBus } from "../../components/toast/toast-bus";
import { cn } from "../../lib/utils";
import { useLivestreams } from "./useLivestreams";
import { LiveStreamCard } from "./LiveStreamCard";
import type { LivestreamTab } from "./livestreamsTypes";
import { LIVESTREAM_TABS, matchesTab, summarize } from "./livestreamsVisual";

const SUMMARY_CARDS = [
  { key: "live", label: "Live ora", icon: Activity, valueClass: "text-emerald-300", iconBoxClass: "bg-emerald-400/15 text-emerald-300 border-emerald-400/20" },
  { key: "scheduled", label: "Programmate", icon: CalendarClock, valueClass: "text-blue-300", iconBoxClass: "bg-blue-400/15 text-blue-300 border-blue-400/20" },
  { key: "reconnecting", label: "In riconnessione", icon: WifiOff, valueClass: "text-amber-300", iconBoxClass: "bg-amber-400/15 text-amber-300 border-amber-400/20" },
  { key: "errors", label: "Con errori", icon: AlertTriangle, valueClass: "text-red-300", iconBoxClass: "bg-red-400/15 text-red-300 border-red-400/20" },
] as const;

/**
 * Live streaming control center — the entry point for the livestream
 * module (sidebar "Live streaming" → /app/livestreams).
 *
 * Shows four summary cards, state tabs and per-live cards backed by
 * GET /api/v1/livestreams?workspace_id=N. The "Crea nuova live" wizard
 * (POST + prepare/start flow) lands with the next module step.
 */
export function LiveStreamsPage() {
  const { state, deletingID, deleteLivestream, reload } = useLivestreams();
  const [tab, setTab] = useState<LivestreamTab>("all");

  const items = state.kind === "ready" ? state.items : [];
  const summary = summarize(items);
  const visible = items.filter((row) => matchesTab(row, tab));

  const createLive = () => {
    toastBus.push("info", "Il wizard di creazione live arriva con il prossimo step.");
  };

  return (
    <div className="min-h-full bg-[#030308] p-8 text-[#e8e8ef]">
      <div className="mx-auto max-w-6xl">
        <header className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h1 className="flex items-center gap-3 text-[28px] font-extrabold tracking-[-0.02em] text-white">
              <Radio size={28} className="text-violet-300" aria-hidden="true" />
              Live streaming
            </h1>
            <p className="mt-1 text-[15px] text-[#9aa0aa]">
              Gestisci live attive, programmate e trasmissioni 24/7.
            </p>
          </div>
          <button
            type="button"
            onClick={createLive}
            data-testid="livestreams-create-cta"
            className="inline-flex items-center gap-2 rounded-xl bg-violet-500 px-4 py-2.5 text-[13px] font-bold text-white shadow-[0_4px_20px_rgba(139,92,246,0.35)] transition-colors hover:bg-violet-400"
          >
            <Plus size={16} aria-hidden="true" />
            Crea nuova live
          </button>
        </header>

        {/* Summary cards */}
        <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {SUMMARY_CARDS.map((card) => {
            const Icon = card.icon;
            return (
              <div
                key={card.key}
                className="rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-5 transition-colors hover:border-white/[0.24]"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <p className="mb-1 text-[13px] font-medium text-[#9aa0aa]">{card.label}</p>
                    <p
                      className={cn("text-[28px] font-extrabold tracking-tight", card.valueClass)}
                      data-testid={`livestreams-summary-${card.key}`}
                    >
                      {summary[card.key]}
                    </p>
                  </div>
                  <div className={cn("grid h-10 w-10 place-items-center rounded-xl border", card.iconBoxClass)}>
                    <Icon size={20} aria-hidden="true" />
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        {/* Tabs */}
        <div className="mb-4 flex flex-wrap items-center gap-1.5 border-b border-white/[0.08] pb-3">
          {LIVESTREAM_TABS.map((entry) => {
            const active = tab === entry.id;
            const count = entry.id === "all" ? items.length : items.filter((row) => matchesTab(row, entry.id)).length;
            return (
              <button
                key={entry.id}
                type="button"
                onClick={() => setTab(entry.id)}
                aria-pressed={active}
                data-testid={`livestreams-tab-${entry.id}`}
                className={cn(
                  "inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12px] font-semibold transition-colors",
                  active
                    ? "bg-violet-500/[0.16] text-violet-200"
                    : "text-[#9aa0aa] hover:bg-white/[0.05] hover:text-white",
                )}
              >
                {entry.label}
                <span
                  className={cn(
                    "rounded-md px-1.5 py-0.5 text-[10px] tabular-nums",
                    active ? "bg-violet-500/25 text-violet-100" : "bg-white/[0.06] text-[#7f8591]",
                  )}
                >
                  {count}
                </span>
              </button>
            );
          })}
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 gap-3 min-[1001px]:grid-cols-2">
            <Skeleton variant="card" height={150} />
            <Skeleton variant="card" height={150} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Impossibile caricare le live"
            message={state.message}
            onRetry={() => void reload()}
            retryLabel="Riprova"
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && items.length === 0 && (
          <EmptyState
            icon={<Radio size={30} />}
            title="Nessuna live configurata"
            description="Trasmetti un video o una playlist preregistrata direttamente dal tuo server."
            cta={
              <button
                type="button"
                onClick={createLive}
                data-testid="livestreams-empty-cta"
                className="inline-flex items-center gap-2 rounded-xl bg-violet-500 px-4 py-2 text-[13px] font-bold text-white transition-colors hover:bg-violet-400"
              >
                <Plus size={16} aria-hidden="true" />
                Crea la prima live
              </button>
            }
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && items.length > 0 && visible.length === 0 && (
          <EmptyState
            icon={<AlertTriangle size={24} />}
            title="Nessuna live in questa categoria"
            description="Non ci sono live nello stato selezionato. Prova a cambiare scheda."
            className="p-8 bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && visible.length > 0 && (
          <div className="grid grid-cols-1 gap-3 min-[1001px]:grid-cols-2">
            {visible.map((row) => (
              <LiveStreamCard
                key={row.id}
                livestream={row}
                deleting={deletingID === row.id}
                onDelete={deleteLivestream}
              />
            ))}
          </div>
        )}

        {state.kind === "ready" && items.length > 0 && (
          <p className="mt-4 flex items-center gap-1.5 text-[11px] text-[#6b7280]" data-testid="livestreams-auto-refresh">
            <Loader2 size={11} className="animate-spin" aria-hidden="true" />
            Aggiornamento automatico ogni 30 secondi
          </p>
        )}
      </div>
    </div>
  );
}
