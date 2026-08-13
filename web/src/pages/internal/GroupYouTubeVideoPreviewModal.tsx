import { Image as ImageIcon, Loader2, Save } from "lucide-react";
import { useYouTubeCategories } from "../../features/youtube/hooks/useYouTubeCategories";
import type { VideoPreview, YouTubePrivacyStatus } from "./groupYouTubeVideosTypes";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";

/**
 * "Modifica video" drawer — the Dettagli destination for a group video.
 *
 * Edits the YouTube snippet metadata (title / description / category)
 * AND the visibility (Pubblico / Privato / Non in elenco) through the
 * single PATCH endpoint; saves via onSave (the hook's
 * saveVideoMetadata). The backend folds the visibility change into the
 * same videos.update and only writes status when it actually differs.
 */
export function GroupYouTubeVideoPreviewModal({
  preview,
  savingMetadata,
  draftTitle,
  draftDescription,
  editCategoryID,
  editPrivacyStatus,
  onClose,
  onDraftTitleChange,
  onDraftDescriptionChange,
  onEditCategoryIDChange,
  onEditPrivacyStatusChange,
  onSave,
}: {
  preview: VideoPreview;
  savingMetadata: boolean;
  draftTitle: string;
  draftDescription: string;
  editCategoryID: string;
  editPrivacyStatus: YouTubePrivacyStatus;
  onClose: () => void;
  onDraftTitleChange: (value: string) => void;
  onDraftDescriptionChange: (value: string) => void;
  onEditCategoryIDChange: (value: string) => void;
  onEditPrivacyStatusChange: (value: YouTubePrivacyStatus) => void;
  onSave: () => void;
}) {
  const thumbnail = safeAssetUrl(preview.video.thumbnail_url);
  const availability = preview.video.availability;
  const availabilityIssue = availability && availability.status !== "available";

  // Categories come from the centralized resource (useYouTubeCategories);
  // it serves the canonical snapshot until the backend proxy is live, so
  // the select always has options. The video's current category is
  // always kept in the list even if it is missing from the fetched set.
  const categories = useYouTubeCategories("IT");
  const categoryItems = categories.data ?? [];
  const hasCurrentCategory = editCategoryID !== "" && categoryItems.some((category) => category.id === editCategoryID);

  return (
    <div
      className="fixed inset-0 z-50 bg-black/80 backdrop-blur-sm"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="edit-metadata-title"
        data-testid="edit-metadata-drawer"
        className="ml-auto flex h-full w-full max-w-md flex-col border-l border-white/[0.12] bg-[#11131a] shadow-2xl"
      >
        <header className="flex items-start justify-between gap-4 border-b border-white/[0.08] px-5 py-4">
          <div className="min-w-0">
            <p className="text-[10px] font-bold uppercase tracking-widest text-violet-300">Dettagli video</p>
            <h4 id="edit-metadata-title" className="mt-1 truncate text-lg font-bold text-white">Modifica video</h4>
            <p className="mt-1 truncate text-[11px] text-[#9aa0aa]">
              {preview.video.channel_name || "Video YouTube"} · {preview.video.youtube_video_id}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Chiudi modifica video"
            className="rounded-lg px-2 py-1 text-xl text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white"
          >
            ×
          </button>
        </header>

        <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
          <div className="overflow-hidden rounded-2xl border border-white/[0.10] bg-black">
            {thumbnail ? <img src={thumbnail} alt={`Thumbnail ${preview.video.channel_name || "YouTube"}`} className="block max-h-[40vh] w-full object-contain" /> : (
              <div className="flex aspect-video items-center justify-center bg-[radial-gradient(circle_at_50%_35%,rgba(139,92,246,0.16),transparent_45%)]">
                <div className="text-center">
                  <span className="mx-auto flex h-11 w-11 items-center justify-center rounded-2xl border border-white/[0.08] bg-white/[0.04]"><ImageIcon size={20} className="text-white/30" aria-hidden="true" /></span>
                  <span className="mt-3 block px-3 text-[11px] text-[#7f8591]">Nessuna miniatura disponibile per questo video</span>
                </div>
              </div>
            )}
          </div>

          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Titolo video
            <input
              value={draftTitle}
              onChange={(event) => onDraftTitleChange(event.target.value)}
              data-testid="edit-metadata-title-input"
              className="rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50"
            />
          </label>

          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Descrizione video
            <textarea
              value={draftDescription}
              onChange={(event) => onDraftDescriptionChange(event.target.value)}
              rows={5}
              data-testid="edit-metadata-description-input"
              className="resize-y rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50"
            />
          </label>

          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Categoria
            <select
              value={editCategoryID}
              onChange={(event) => onEditCategoryIDChange(event.target.value)}
              data-testid="edit-metadata-category"
              className="rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50"
            >
              <option value="">Senza categoria</option>
              {categories.isLoading && categoryItems.length === 0 && (
                <option value="" disabled>Caricamento categorie…</option>
              )}
              {categoryItems.map((category) => (
                <option key={category.id} value={category.id}>{category.label}</option>
              ))}
              {editCategoryID !== "" && !hasCurrentCategory && (
                <option value={editCategoryID}>{editCategoryID}</option>
              )}
            </select>
          </label>

          <label className="grid gap-1.5 text-xs font-semibold text-[#cdd2da]">
            Visibilità
            <select
              value={editPrivacyStatus}
              onChange={(event) => onEditPrivacyStatusChange(event.target.value as YouTubePrivacyStatus)}
              data-testid="edit-metadata-privacy"
              className="rounded-lg border border-white/[0.10] bg-black/20 px-3 py-2 text-sm text-white outline-none focus:border-violet-400/50"
            >
              <option value="private">Privato</option>
              <option value="unlisted">Non in elenco</option>
              <option value="public">Pubblico</option>
            </select>
          </label>
          {availabilityIssue && (
            <p className="text-[11px] text-amber-200/90">
              {availability?.reason ?? "Il video non è attualmente gestibile dal canale."}
            </p>
          )}
        </div>

        <footer className="flex justify-end gap-2 border-t border-white/[0.08] px-5 py-4">
          <button
            type="button"
            onClick={onClose}
            data-testid="edit-metadata-cancel"
            className="rounded-lg border border-white/[0.10] px-3 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08]"
          >
            Annulla
          </button>
          <button
            type="button"
            onClick={onSave}
            disabled={savingMetadata}
            data-testid="edit-metadata-save"
            className="inline-flex items-center gap-2 rounded-lg bg-violet-500 px-3 py-2 text-xs font-bold text-white hover:bg-violet-400 disabled:opacity-60"
          >
            {savingMetadata ? <Loader2 size={14} className="animate-spin" aria-hidden="true" /> : <Save size={14} aria-hidden="true" />}
            {savingMetadata ? "Salvataggio…" : "Salva modifiche"}
          </button>
        </footer>
      </div>
    </div>
  );
}
