import { Edit3, X } from "lucide-react";
import type { GroupDraft } from "./groupCoversTypes";

export function GroupDraftPreviewModal({
  draft,
  previewUrl,
  opening,
  onClose,
  onOpenEditor,
}: {
  draft: GroupDraft;
  previewUrl?: string;
  opening: boolean;
  onClose: () => void;
  onOpenEditor: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-sm"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="draft-cover-preview-title"
        className="max-h-[94vh] w-full max-w-5xl overflow-y-auto rounded-3xl border border-white/[0.14] bg-[#11131a] p-5 shadow-2xl sm:p-6"
      >
        <div className="mb-5 flex items-start justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-violet-300">Anteprima copertina</p>
            <h2 id="draft-cover-preview-title" className="mt-1 text-xl font-bold text-white">{draft.name}</h2>
            <p className="mt-1 text-[11px] text-[#9aa0aa]">Bozza standalone · Nessun video associato</p>
          </div>
          <button type="button" onClick={onClose} className="rounded-xl p-2 text-[#9aa0aa] transition-colors hover:bg-white/[0.08] hover:text-white" aria-label="Chiudi anteprima">
            <X size={18} aria-hidden="true" />
          </button>
        </div>

        <div className="overflow-hidden rounded-2xl border border-white/[0.10] bg-black shadow-[0_20px_80px_rgba(0,0,0,0.35)]">
          {previewUrl ? <img src={previewUrl} alt={`Copertina ${draft.name}`} className="block max-h-[68vh] w-full object-contain" /> : <div className="flex aspect-video flex-col items-center justify-center gap-2 text-sm text-[#9aa0aa]">Copertina non ancora esportata.</div>}
        </div>

        <div className="mt-5 rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 text-xs text-[#9aa0aa]">
          <p className="font-semibold text-[#cdd2da]">Dettagli</p>
          <p className="mt-2">Stato: <span className="text-white">{draft.status === "ready" ? "Pronta" : "Bozza"}</span></p>
          <p className="mt-1">Video associato: <span className="text-white">Nessuno</span></p>
        </div>

        <div className="mt-5 flex justify-end gap-2 border-t border-white/[0.08] pt-4">
          <button type="button" onClick={onClose} className="rounded-xl border border-white/[0.10] px-3.5 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08]">Chiudi</button>
          <button type="button" onClick={onOpenEditor} disabled={opening} className="inline-flex items-center gap-2 rounded-xl bg-violet-500 px-3.5 py-2 text-xs font-bold text-white hover:bg-violet-400 disabled:opacity-60">
            <Edit3 size={14} aria-hidden="true" /> {opening ? "Apertura…" : "Modifica in InstaEditor"}
          </button>
        </div>
      </div>
    </div>
  );
}
