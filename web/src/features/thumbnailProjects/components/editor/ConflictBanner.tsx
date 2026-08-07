/**
 * ConflictBanner — 409 PROJECT_VERSION_CONFLICT pause banner.
 *
 * Never silent last-write-wins on the canvas: when the autosave hits a
 * version conflict (another tab wrote the project), this banner pauses
 * autosave and offers "Ricarica versione recente" (reload server truth)
 * or "Salva come copia" (fork the local snapshot into a NEW project).
 */
import { AlertTriangle, CopyPlus, Loader2, RefreshCw } from "lucide-react";
import type { ProjectVersionConflict } from "../../types";

interface ConflictBannerProps {
  conflict: ProjectVersionConflict;
  isSavingCopy: boolean;
  onReload: () => void;
  onSaveAsCopy: () => void;
}

export function ConflictBanner({ conflict, isSavingCopy, onReload, onSaveAsCopy }: ConflictBannerProps) {
  return (
    <div
      data-testid="conflict-banner"
      role="alertdialog"
      aria-label="Conflitto di versione"
      className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-red-400/25 bg-red-500/[0.08] px-4 py-3"
    >
      <p className="flex items-center gap-2 text-[13px] text-red-200">
        <AlertTriangle size={15} />
        Modifiche non salvate: il progetto è stato modificato in un'altra scheda
        {conflict.current_version !== undefined
          ? ` (versione attuale ${conflict.current_version})`
          : ""}.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={onReload}
          className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
        >
          <RefreshCw size={14} />
          Ricarica versione recente
        </button>
        <button
          type="button"
          onClick={onSaveAsCopy}
          disabled={isSavingCopy}
          className="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-[13px] font-semibold text-black hover:bg-white/90 transition-colors disabled:opacity-50"
        >
          {isSavingCopy ? <Loader2 size={14} className="animate-spin" /> : <CopyPlus size={14} />}
          Salva come copia
        </button>
      </div>
    </div>
  );
}
