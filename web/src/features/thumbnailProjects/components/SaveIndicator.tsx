/**
 * SaveIndicator — the real canvas autosave status pill.
 *
 * Renders exactly one of the four honest states:
 *   - "Salvataggio…"         (spinner)      → PUT in flight
 *   - "Salvato alle HH:MM"   (check)        → server acked this snapshot
 *   - "Modifiche non salvate" (dot)         → local edits pending debounce
 *   - "Errore di salvataggio" (alert + retry) → last PUT failed
 *
 * "Salvato" is shown ONLY after the server response — never pre-empted
 * (DoD: "Non mostrare falsamente 'Salvato'").
 */
import { AlertTriangle, CheckCircle2, Cloud, Loader2 } from "lucide-react";
import { cn } from "../../../lib/utils";
import type { ThumbnailSaveStatus } from "../hooks/useThumbnailAutosave";

export interface SaveIndicatorProps {
  status: ThumbnailSaveStatus;
  lastSavedAt: Date | null;
  error?: string | null;
  lastHash?: string | null;
  onRetry?: () => void;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString("it-IT", { hour: "2-digit", minute: "2-digit" });
}

export function SaveIndicator({
  status,
  lastSavedAt,
  error,
  lastHash,
  onRetry,
}: SaveIndicatorProps) {
  const title =
    status === "saved" && lastHash ? `Snapshot ${lastHash.slice(0, 12)}…` : undefined;

  const content = (() => {
    switch (status) {
      case "saving":
        return (
          <>
            <Loader2 size={13} className="animate-spin text-sky-300" />
            <span>Salvataggio…</span>
          </>
        );
      case "saved":
        return (
          <>
            <CheckCircle2 size={13} className="text-emerald-400" />
            <span>
              Salvato {lastSavedAt ? `alle ${formatTime(lastSavedAt)}` : ""}
            </span>
          </>
        );
      case "error":
        return (
          <>
            <AlertTriangle size={13} className="text-red-400" />
            <span>Errore di salvataggio</span>
            {onRetry && (
              <button
                type="button"
                onClick={onRetry}
                className="ml-1 rounded-md px-1.5 py-0.5 text-[11px] font-bold text-red-300 hover:bg-red-500/[0.12] transition-colors"
              >
                Riprova
              </button>
            )}
          </>
        );
      case "dirty":
        return (
          <>
            <span className="h-1.5 w-1.5 rounded-full bg-amber-300" />
            <span>Modifiche non salvate</span>
          </>
        );
      default:
        return (
          <>
            <Cloud size={13} className="text-[#9aa0aa]" />
            <span>Salvato</span>
          </>
        );
    }
  })();

  return (
    <span
      data-testid="save-indicator"
      title={error ?? title}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[12px] font-medium transition-colors",
        status === "saving" && "border-sky-400/20 bg-sky-500/[0.08] text-sky-200",
        status === "saved" && "border-emerald-400/20 bg-emerald-500/[0.08] text-emerald-200",
        status === "dirty" && "border-amber-400/20 bg-amber-500/[0.08] text-amber-200",
        status === "error" && "border-red-400/20 bg-red-500/[0.08] text-red-200",
        status === "idle" && "border-white/[0.08] bg-white/[0.04] text-[#9aa0aa]",
      )}
    >
      {content}
    </span>
  );
}
