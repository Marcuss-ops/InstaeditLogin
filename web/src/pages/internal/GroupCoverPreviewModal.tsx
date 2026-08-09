import { Edit3, Loader2, Save, X } from "lucide-react";
import type { GroupCover } from "./groupCoversTypes";

export function GroupCoverPreviewModal({
  cover,
  previewUrl,
  saving,
  opening,
  title,
  description,
  onTitleChange,
  onDescriptionChange,
  onClose,
  onSave,
  onOpenEditor,
}: {
  cover: GroupCover;
  previewUrl?: string;
  saving: boolean;
  opening: boolean;
  title: string;
  description: string;
  onTitleChange: (value: string) => void;
  onDescriptionChange: (value: string) => void;
  onClose: () => void;
  onSave: () => void;
  onOpenEditor: () => void;
}) {
  const preview = previewUrl;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-sm"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="cover-preview-title"
        className="max-h-[94vh] w-full max-w-5xl overflow-y-auto rounded-3xl border border-white/[0.14] bg-[#11131a] p-5 shadow-2xl sm:p-6"
      >
        <div className="mb-5 flex items-start justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-violet-300">Anteprima copertina</p>
            <h2 id="cover-preview-title" className="mt-1 text-xl font-bold text-white">{cover.channel_name || "Copertina YouTube"}</h2>
            <p className="mt-1 text-[11px] text-[#9aa0aa]">{cover.youtube_video_id} · {cover.language?.toUpperCase() || "Lingua non impostata"}</p>
          </div>
          <button type="button" onClick={onClose} className="rounded-xl p-2 text-[#9aa0aa] transition-colors hover:bg-white/[0.08] hover:text-white" aria-label="Chiudi anteprima">
            <X size={18} aria-hidden="true" />
          </button>
        </div>

        <div className="overflow-hidden rounded-2xl border border-white/[0.10] bg-black shadow-[0_20px_80px_rgba(0,0,0,0.35)]">
          {preview ? <img src={preview} alt={`Copertina ${cover.draft_title || cover.name}`} className="block max-h-[62vh] w-full object-contain" /> : <div className="flex aspect-video flex-col items-center justify-center gap-2 text-sm text-[#9aa0aa]"><span>La copertina non è ancora esportata.</span><span className="text-xs text-[#7f8591]">Apri InstaEditor per visualizzarla e modificarla.</span></div>}
        </div>

        <div className="mt-5 grid gap-4 md:grid-cols-2">
          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Titolo copertina
            <input value={title} onChange={(event) => onTitleChange(event.target.value)} maxLength={100} className="rounded-xl border border-white/[0.10] bg-black/25 px-3 py-2.5 text-sm text-white outline-none transition-colors focus:border-violet-400/60" />
          </label>
          <div className="rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 text-xs text-[#9aa0aa]">
            <p className="font-semibold text-[#cdd2da]">Dettagli</p>
            <p className="mt-2">Canale: <span className="text-white">{cover.channel_name || `Account #${cover.platform_account_id}`}</span></p>
            <p className="mt-1">Stato: <span className="text-white">{cover.project_status}</span></p>
            <p className="mt-1">Video: <span className="font-mono text-white">{cover.youtube_video_id}</span></p>
          </div>
          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da] md:col-span-2">
            Descrizione
            <textarea value={description} onChange={(event) => onDescriptionChange(event.target.value)} rows={4} maxLength={5000} className="resize-y rounded-xl border border-white/[0.10] bg-black/25 px-3 py-2.5 text-sm text-white outline-none transition-colors focus:border-violet-400/60" />
          </label>
        </div>

        <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-white/[0.08] pt-4">
          <button type="button" onClick={onClose} className="rounded-xl border border-white/[0.10] px-3.5 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08]">Chiudi</button>
          <button type="button" onClick={onSave} disabled={saving} className="inline-flex items-center gap-2 rounded-xl border border-white/[0.10] bg-white/[0.05] px-3.5 py-2 text-xs font-bold text-white hover:bg-white/[0.10] disabled:opacity-60">
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} aria-hidden="true" />} {saving ? "Salvataggio…" : "Salva modifiche"}
          </button>
          <button type="button" onClick={onOpenEditor} disabled={opening || !cover.velox_project_id || !cover.editor_url} className="inline-flex items-center gap-2 rounded-xl bg-violet-500 px-3.5 py-2 text-xs font-bold text-white hover:bg-violet-400 disabled:opacity-60">
            {opening ? <Loader2 size={14} className="animate-spin" /> : <Edit3 size={14} aria-hidden="true" />} Modifica in InstaEditor
          </button>
        </div>
      </div>
    </div>
  );
}
