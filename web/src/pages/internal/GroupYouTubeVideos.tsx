import { useState } from "react";
import { AlertTriangle, Loader2, RefreshCw, Video } from "lucide-react";
import { EmptyState } from "../../components/feedback/EmptyState";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { GroupYouTubeVideoCard } from "./GroupYouTubeVideoCard";
import { GroupYouTubeVideoPreviewModal } from "./GroupYouTubeVideoPreviewModal";
import { DEFAULT_PAGE_SIZE, RECENCY_OPTIONS } from "./groupYouTubeVideosTypes";

export function GroupYouTubeVideos({ groupId }: { groupId: number }) {
  const [enabled, setEnabled] = useState(false);
  const {
    state,
    recencyDays,
    setRecencyDays,
    openingVideoID,
    preview,
    setPreview,
    draftTitle,
    setDraftTitle,
    draftDescription,
    setDraftDescription,
    savingMetadata,
    openThumbnailEditor,
    openVideoPreview,
    saveVideoMetadata,
    refreshVideos,
    loadMoreVideos,
  } = useGroupYouTubeVideos(groupId, enabled);

  return (
    <section className="mb-6" data-testid="group-youtube-videos">
      <div className="flex items-center justify-between gap-3 mb-2">
        <div>
          <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa]">
            Video privati da pubblicare
          </h3>
          <p className="text-[12px] text-[#9aa0aa] mt-1">
            Solo video privati recenti dei canali presenti nel gruppo.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-[10px] text-[#9aa0aa]" htmlFor="group-video-recency">Periodo</label>
          <select
            id="group-video-recency"
            value={recencyDays}
            onChange={(event) => {
              setRecencyDays(Number(event.target.value));
            }}
            className="rounded-lg border border-white/[0.08] bg-white/[0.04] px-2 py-1.5 text-[11px] font-semibold text-[#cdd2da]"
            data-testid="group-youtube-videos-recency"
          >
            {RECENCY_OPTIONS.map((days) => <option key={days} value={days}>{days} giorni</option>)}
          </select>
          <button
            type="button"
            onClick={() => {
              setEnabled(true);
              refreshVideos(true, true);
            }}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white transition-colors"
            data-testid="group-youtube-videos-refresh"
          >
            <RefreshCw size={12} aria-hidden="true" />
            Aggiorna
          </button>
        </div>
      </div>

      {!enabled && (
        <div className="rounded-xl border border-dashed border-white/[0.10] bg-white/[0.02] px-4 py-5 text-[12px] text-[#9aa0aa]">
          <p>Carica i video YouTube solo quando vuoi consultare il gruppo.</p>
          <button type="button" onClick={() => setEnabled(true)} className="mt-3 rounded-lg bg-white px-3 py-1.5 text-[11px] font-semibold text-black hover:bg-white/90" data-testid="group-youtube-videos-load">Carica video</button>
        </div>
      )}

      {enabled && state.kind === "loading" && (
        <div className="flex items-center gap-2 rounded-xl border border-white/[0.08] bg-white/[0.03] px-4 py-5 text-[12px] text-[#9aa0aa]">
          <Loader2 size={15} className="animate-spin" aria-hidden="true" />
          Caricamento stato video…
        </div>
      )}

      {enabled && state.kind === "error" && (
        <div
          className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] px-4 py-4 text-[12px] text-amber-200"
          role="alert"
          data-testid={state.upstream ? "group-youtube-upstream-error" : undefined}
        >
          {state.message}
        </div>
      )}

      {enabled && state.kind === "ready" && state.warnings.length > 0 && (
        <div className="mb-2 flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/[0.05] px-3 py-2 text-[11px] text-amber-200" role="status">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" aria-hidden="true" />
          <span>Alcuni canali non sono stati verificati: {state.warnings.join(" · ")}</span>
        </div>
      )}

      {enabled && state.kind === "ready" && state.videos.length === 0 && (
        <EmptyState
          title="Nessun video privato recente"
          description="Non ci sono video privati nei giorni selezionati. Prova ad ampliare il periodo a 90 giorni; i video pubblici e non in elenco non vengono mostrati."
          icon={<Video size={28} />}
          className="p-6 bg-white/[0.02] border-white/[0.08]"
        />
      )}

      {enabled && state.kind === "ready" && state.videos.length > 0 && (
        <div className="space-y-3">
          {(() => {
            const midpoint = Math.ceil(state.videos.length / 2);
            const columns = [state.videos.slice(0, midpoint), state.videos.slice(midpoint)];
            return (
              <div className="grid grid-cols-1 gap-3 min-[1001px]:grid-cols-2">
                {columns.map((videos, index) => (
                  <section key={index} className="rounded-2xl border border-white/[0.08] bg-white/[0.018] p-4">
                    <div className="flex flex-col gap-2.5">
                      {videos.map((video) => (
                        <GroupYouTubeVideoCard
                          key={`${video.platform_account_id}:${video.youtube_video_id}`}
                          video={video}
                          openingVideoID={openingVideoID}
                          onPreview={openVideoPreview}
                          onThumbnail={openThumbnailEditor}
                        />
                      ))}
                    </div>
                  </section>
                ))}
              </div>
            );
          })()}
          {(
            <div className="grid min-h-[58px] grid-cols-[1fr_auto_1fr] items-center rounded-2xl border border-white/[0.08] bg-white/[0.018] px-4 text-[11px] text-[#9aa0aa]">
              <span>1–{state.videos.length} video</span>
              {state.hasMore ? (
                <button
                  type="button"
                  onClick={loadMoreVideos}
                  disabled={state.isLoadingMore}
                  className="inline-flex items-center justify-center gap-2 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[11px] font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white disabled:opacity-50"
                  data-testid="group-youtube-videos-load-more"
                >
                  {state.isLoadingMore ? <Loader2 size={14} className="animate-spin" /> : null}
                  {state.isLoadingMore ? "Caricamento…" : "Carica altri video"}
                </button>
              ) : <span className="rounded-lg border border-white/[0.06] px-3 py-2 text-[11px] text-[#7f8591]">Tutti i video caricati</span>}
              <span className="justify-self-end">Righe per pagina: {DEFAULT_PAGE_SIZE}</span>
            </div>
          )}
        </div>
      )}

      {preview && (
        <GroupYouTubeVideoPreviewModal
          preview={preview}
          openingVideoID={openingVideoID}
          savingMetadata={savingMetadata}
          draftTitle={draftTitle}
          draftDescription={draftDescription}
          onClose={() => setPreview(null)}
          onDraftTitleChange={setDraftTitle}
          onDraftDescriptionChange={setDraftDescription}
          onSave={() => void saveVideoMetadata()}
          onThumbnail={openThumbnailEditor}
        />
      )}
    </section>
  );
}
