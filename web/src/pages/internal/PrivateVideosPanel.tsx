import { ExternalLink, Loader2, Video } from "lucide-react";
import { Skeleton, ErrorState } from "../../components/feedback";
import { EmptyState } from "../../components/feedback/EmptyState";
import type { ContentItem, VideoState } from "./calendarTypes";

export function PrivateVideosPanel({
  videoState,
  loadVideos,
  handleEditThumbnail,
}: {
  videoState: VideoState;
  loadVideos: (cursor?: string) => void | Promise<void>;
  handleEditThumbnail: (item: ContentItem) => void | Promise<void>;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-4 sm:p-6 flex-1 min-h-0 flex flex-col overflow-y-auto">
      {videoState.kind === "loading" && (
        <div className="flex-1 flex flex-col gap-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} variant="card" height={72} />
          ))}
        </div>
      )}

      {videoState.kind === "error" && (
        <ErrorState
          title="Couldn't load videos"
          message={videoState.message}
          onRetry={() => void loadVideos()}
          className="bg-[#1f1f2e] border-white/[0.12]"
        />
      )}

      {videoState.kind === "ready" && videoState.items.length === 0 && (
        <EmptyState
          title="Nessun video privato"
          description="Non ci sono video privati per questo canale."
          icon={<Video size={32} />}
          className="bg-[#1f1f2e] border-white/[0.12]"
        />
      )}

      {videoState.kind === "ready" && (
        <div className="flex flex-col gap-2">
          {videoState.items.map((item) => (
            <div
              key={item.external_id}
              className="flex gap-4 p-3 rounded-xl bg-white/[0.03] border border-white/[0.06] hover:bg-white/[0.06] transition-colors"
            >
              <div className="w-40 h-24 rounded-lg bg-white/[0.08] overflow-hidden shrink-0 relative">
                {item.thumbnail_url ? (
                  <img
                    src={item.thumbnail_url}
                    alt={item.title ?? ""}
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <div className="w-full h-full flex items-center justify-center">
                    <Video size={20} className="text-white/20" />
                  </div>
                )}
                {item.duration && (
                  <span className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded bg-black/70 text-[10px] text-white font-medium">
                    {item.duration}
                  </span>
                )}
              </div>
              <div className="flex flex-col justify-between min-w-0 flex-1 py-0.5">
                <div>
                  <p className="text-[13px] font-semibold text-white truncate">
                    {item.title}
                  </p>
                  <p className="text-[11px] text-[#9aa0aa] truncate mt-0.5">
                    {item.external_id}
                  </p>
                </div>
                <div className="flex items-center gap-3 text-[11px] text-[#9aa0aa]">
                  {item.published_at && (
                    <span>{new Date(item.published_at).toLocaleDateString()}</span>
                  )}
                  {item.privacy && (
                    <span className="capitalize">{item.privacy}</span>
                  )}
                </div>
              </div>
              <div className="flex flex-col items-end justify-center gap-2 shrink-0">
                <a
                  href={item.public_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-white/[0.06] border border-white/[0.08] text-[11px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors no-underline"
                >
                  YouTube <ExternalLink size={12} />
                </a>
                <button
                  type="button"
                  onClick={() => void handleEditThumbnail(item)}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-blue-500/10 border border-blue-500/20 text-[11px] font-semibold text-blue-400 hover:bg-blue-500/20 hover:text-blue-300 transition-colors"
                >
                  Modifica copertina
                </button>
              </div>
            </div>
          ))}
          {videoState.nextCursor && (
            <button
              type="button"
              onClick={() => void loadVideos(videoState.nextCursor)}
              disabled={videoState.isLoadingMore}
              className="mt-2 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-medium text-[#9aa0aa] hover:text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50"
            >
              {videoState.isLoadingMore ? (
                <span className="flex items-center gap-2">
                  <Loader2 size={14} className="animate-spin" /> Caricamento…
                </span>
              ) : (
                "Carica altri video"
              )}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
