import { AlertCircle, CheckCircle2, Loader2, Video } from "lucide-react";
import { EmptyState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { isCopyrightProblem, type YouTubeCopyrightCheck } from "../../features/youtube/api/copyrightApi";
import type { ContentItem, CopyrightByVideoId } from "./youtubeStudioTypes";

function CopyrightSection({ check }: { check?: YouTubeCopyrightCheck }) {
  const status = check?.status ?? "clear";
  const problem = isCopyrightProblem(status);
  const checking = status === "pending" || status === "processing";

  return (
    <div
      className={cn(
        "mt-2 flex items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px] font-semibold",
        problem
          ? "border-red-500/30 bg-red-500/10 text-red-300"
          : checking
            ? "border-amber-500/30 bg-amber-500/10 text-amber-200"
            : "border-emerald-500/20 bg-emerald-500/10 text-emerald-300",
      )}
      data-testid="private-video-copyright"
      title={problem ? check?.message || "Problema copyright" : undefined}
    >
      {problem ? <AlertCircle size={12} aria-hidden="true" /> : checking ? <Loader2 size={12} className="animate-spin" aria-hidden="true" /> : <CheckCircle2 size={12} aria-hidden="true" />}
      <span>Copyright: {problem ? "Problema" : checking ? "In verifica" : "None"}</span>
    </div>
  );
}

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
  copyrightByVideoId,
  manualVideoId,
  onSelectVideo,
  privateVideosEnabled,
  onLoad,
}: {
  selectedChannelId: number | "";
  privateVideos: ContentItem[];
  loadingVideos: boolean;
  copyrightByVideoId: CopyrightByVideoId;
  manualVideoId: string;
  onSelectVideo: (videoId: string) => void;
  privateVideosEnabled: boolean;
  onLoad: () => void;
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

      {!privateVideosEnabled && (
        <button type="button" onClick={onLoad} className="rounded-xl bg-white px-4 py-2 text-[13px] font-semibold text-black hover:bg-white/90" data-testid="youtube-studio-load-private-videos">
          Carica video privati
        </button>
      )}

      {privateVideosEnabled && loadingVideos && (
        <div className="flex items-center gap-2 text-[13px] text-[#9aa0aa]">
          <Loader2 size={14} className="animate-spin" /> Caricamento video…
        </div>
      )}

      {privateVideosEnabled && !loadingVideos && privateVideos.length === 0 && (
        <EmptyState
          title="Nessun video privato trovato"
          description="Carica un video privato su YouTube e ricarica la pagina."
          icon={<Video size={32} />}
          className="bg-white/[0.02] border-white/[0.06]"
        />
      )}

      {privateVideosEnabled && !loadingVideos && privateVideos.length > 0 && (
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
                <CopyrightSection check={copyrightByVideoId[v.external_id]} />
              </div>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
