import { Edit3, Loader2, Save } from "lucide-react";
import type { GroupYouTubeVideo, VideoPreview } from "./groupYouTubeVideosTypes";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";

export function GroupYouTubeVideoPreviewModal({
  preview,
  openingVideoID,
  savingMetadata,
  draftTitle,
  draftDescription,
  onClose,
  onDraftTitleChange,
  onDraftDescriptionChange,
  onSave,
  onThumbnail,
}: {
  preview: VideoPreview;
  openingVideoID: string | null;
  savingMetadata: boolean;
  draftTitle: string;
  draftDescription: string;
  onClose: () => void;
  onDraftTitleChange: (value: string) => void;
  onDraftDescriptionChange: (value: string) => void;
  onSave: () => void;
  onThumbnail: (video: GroupYouTubeVideo) => void;
}) {
  const thumbnail = safeAssetUrl(preview.video.thumbnail_url);
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-sm"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div role="dialog" aria-modal="true" aria-labelledby="youtube-video-preview-title" className="max-h-[96vh] w-full max-w-6xl overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#11131a] p-5 shadow-2xl">
        <div className="mb-4 flex items-start justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-widest text-violet-300">Preview copertina</p>
            <h4 id="youtube-video-preview-title" className="mt-1 text-xl font-bold text-white">{preview.video.channel_name || "Video YouTube"}</h4>
            <p className="mt-1 text-[11px] text-[#9aa0aa]">Lingua: {(preview.video.language?.trim() || "en").toUpperCase()} · {preview.video.youtube_video_id}</p>
          </div>
          <button type="button" onClick={onClose} className="rounded-lg px-2 py-1 text-xl text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Chiudi preview">×</button>
        </div>

        <div className="overflow-hidden rounded-2xl border border-white/[0.10] bg-black">
          {thumbnail ? <img src={thumbnail} alt={`Thumbnail ${preview.video.channel_name || "YouTube"}`} className="block max-h-[76vh] w-full object-contain" /> : <div className="flex aspect-video items-center justify-center text-[#9aa0aa]">Thumbnail non disponibile</div>}
        </div>
        <div className="mt-4 grid gap-3 md:grid-cols-2">
          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Titolo video
            <input value={draftTitle} onChange={(event) => onDraftTitleChange(event.target.value)} className="rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50" />
          </label>
          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da] md:col-span-2">
            Descrizione video
            <textarea value={draftDescription} onChange={(event) => onDraftDescriptionChange(event.target.value)} rows={5} className="resize-y rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50" />
          </label>
        </div>

        <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-white/[0.08] pt-4">
          <button type="button" onClick={onClose} className="rounded-lg border border-white/[0.10] px-3 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08]">Chiudi</button>
          <button type="button" onClick={onSave} disabled={savingMetadata} className="inline-flex items-center gap-2 rounded-lg border border-white/[0.10] bg-white/[0.05] px-3 py-2 text-xs font-bold text-white hover:bg-white/[0.10] disabled:opacity-60">
            <Save size={14} aria-hidden="true" /> {savingMetadata ? "Salvataggio…" : "Salva titolo e descrizione"}
          </button>
          <button type="button" onClick={() => { onClose(); onThumbnail(preview.video); }} disabled={openingVideoID !== null} className="inline-flex items-center gap-2 rounded-lg bg-violet-500 px-3 py-2 text-xs font-bold text-white hover:bg-violet-400 disabled:opacity-60">
            {openingVideoID === preview.video.youtube_video_id ? <Loader2 size={14} className="animate-spin" /> : <Edit3 size={14} aria-hidden="true" />}
            Modifica in InstaEditor
          </button>
        </div>
      </div>
    </div>
  );
}
