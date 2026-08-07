/**
 * EditorHeader — Cover editor top bar.
 *
 * Back navigation to the Copertine library, project identity (name +
 * id), the REAL save indicator (Salvataggio…/Salvato/Modifiche non
 * salvate/Errore), the manual "Salva progetto" flush button, and the
 * fixed canvas dimensions.
 */
import { ArrowLeft, Hash, Loader2, Save } from "lucide-react";
import type { ThumbnailSaveStatus } from "../../hooks/useThumbnailAutosave";
import { SaveIndicator } from "../SaveIndicator";

interface EditorHeaderProps {
  projectName: string;
  projectId: string;
  canvasWidth?: number;
  canvasHeight?: number;
  saveStatus: ThumbnailSaveStatus;
  lastSavedAt: Date | null;
  saveError: string | null;
  lastHash: string | null;
  onRetry: () => void;
  isManualSaving: boolean;
  saveDisabled: boolean;
  onManualSave: () => void;
  onClose: () => void;
}

export function EditorHeader({
  projectName,
  projectId,
  canvasWidth,
  canvasHeight,
  saveStatus,
  lastSavedAt,
  saveError,
  lastHash,
  onRetry,
  isManualSaving,
  saveDisabled,
  onManualSave,
  onClose,
}: EditorHeaderProps) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onClose}
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline"
        >
          <ArrowLeft size={14} /> Copertine
        </button>
        <div className="h-5 w-px bg-white/[0.08]" />
        <div>
          <h1 className="text-[20px] font-extrabold tracking-[-0.02em] text-white leading-tight">
            {projectName}
          </h1>
          <code className="flex items-center gap-1 text-[11px] text-[#9aa0aa]">
            <Hash size={10} />
            {projectId}
          </code>
        </div>
      </div>
      <div className="flex items-center gap-3">
        <SaveIndicator
          status={saveStatus}
          lastSavedAt={lastSavedAt}
          error={saveError}
          lastHash={lastHash}
          onRetry={onRetry}
        />
        <button
          type="button"
          onClick={onManualSave}
          disabled={isManualSaving || saveDisabled}
          className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isManualSaving ? (
            <Loader2 size={14} className="animate-spin" />
          ) : (
            <Save size={14} />
          )}
          Salva progetto
        </button>
        <span className="hidden sm:inline text-[12px] text-[#9aa0aa]">
          {canvasWidth}×{canvasHeight}
        </span>
      </div>
    </div>
  );
}
