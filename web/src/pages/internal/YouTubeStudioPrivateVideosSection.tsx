import { Loader2, Video } from "lucide-react";
import { EmptyState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import type { ContentItem } from "./youtubeStudioTypes";

/**
 * YouTubeStudioPrivateVideosSection renders the "Video privati sul
 * canale" grid for the selected channel. Clicking a card fills the
 * video-ID input of YouTubeStudioCreateForm and scrolls back to the top.
 * Pure presentational — the fetch logic lives in
 * useYouTubeStudioPrivateVideos.
 */
export function YouTubeStudioPrivateVideosSection({
  selectedChannelId,
  privateVideos,
  loadingVideos,
  manualVideoId,
  onSelectVideo,
}: {
  selectedChannelId: number | "";
  privateVideos: ContentItem[];
  loadingVideos: boolean;
  manualVideoId: string;
  onSelectVideo: (videoId: string) => void;
}) {
  if (selectedChannelId === "") return null;

  return (
    <section className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-4 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
      <header>
        <h2 className="text-[16px] font-bold text-white flex items-center gap-2">
          <Video size={16} aria-hidden="true" />
          Video privati sul canale
        </h2>
        <p className="text-[13px] text-[#9aa0aa] mt-1">
          Clicca un video per iniziare a modificare la copertina.
        </p>
      </header>

      {loadingVideos && (
        <div className="flex items-center gap-2 text-[13px] text-[#9aa0aa]">
          <Loader2 size={14} className="animate-spin" /> Caricamento video…
        </div>
      )}

      {!loadingVideos && privateVideos.length === 0 && (
        <EmptyState
          title="Nessun video privato trovato"
          description="Carica un video privato su YouTube e ricarica la pagina."
          icon={<Video size={32} />}
          className="bg-white/[0.02] border-white/[0.06]"
        />
      )}

      {!loadingVideos && privateVideos.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {privateVideos.map((v) => (
            <button
              key={v.external_id}
              type="button"
              onClick={() => onSelectVideo(v.external_id)}
              className={cn(
                "flex gap-3 p-3 rounded-xl border text-left transition-all no-underline group",
                manualVideoId === v.external_id
                  ? "border-blue-500/50 bg-blue-500/[0.08]"
                  : "border-white/[0.08] bg-white/[0.03] hover:bg-white/[0.06] hover:border-white/[0.15]",
              )}
            >
              <div className="w-28 h-16 rounded-lg bg-white/[0.08] overflow-hidden shrink-0">
                {v.thumbnail_url ? (
                  <img
                    src={v.thumbnail_url}
                    alt={v.title ?? ""}
                    className="w-full h-full object-cover"
                    loading="lazy"
                  />
                ) : (
                  <div className="w-full h-full flex items-center justify-center">
                    <Video size={16} className="text-white/20" />
                  </div>
                )}
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-[12px] font-semibold text-white truncate">
                  {v.title || v.external_id}
                </p>
                <p className="text-[11px] text-[#9aa0aa] font-mono mt-0.5">
                  {v.external_id}
                </p>
                {v.published_at && (
                  <p className="text-[10px] text-[#9aa0aa] mt-0.5">
                    {new Date(v.published_at).toLocaleDateString("it-IT")}
                  </p>
                )}
              </div>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
