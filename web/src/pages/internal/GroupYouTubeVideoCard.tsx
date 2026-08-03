import { Edit3, ExternalLink, Loader2, MoreHorizontal, Video } from "lucide-react";
import { cn } from "../../lib/utils";
import type { GroupYouTubeVideo } from "./groupYouTubeVideosTypes";
import {
  formatPublishAt,
  languageFlag,
  publicationState,
  safeAssetUrl,
  toneClasses,
} from "./groupYouTubeVideosVisual";

export function GroupYouTubeVideoCard({
  video,
  openingVideoID,
  onPreview,
  onThumbnail,
}: {
  video: GroupYouTubeVideo;
  openingVideoID: string | null;
  onPreview: (video: GroupYouTubeVideo) => void;
  onThumbnail: (video: GroupYouTubeVideo) => void;
}) {
  const publication = publicationState(video);
  const watchUrl = `https://www.youtube.com/watch?v=${encodeURIComponent(video.youtube_video_id)}`;
  const thumbnail = safeAssetUrl(video.thumbnail_url);
  return (
    <article
      className="grid min-h-[126px] cursor-pointer grid-cols-[142px_minmax(0,1fr)] gap-4 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-3 transition-colors hover:border-violet-400/30 hover:bg-white/[0.05] max-[700px]:grid-cols-[112px_minmax(0,1fr)]"
      onClick={() => onPreview(video)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onPreview(video);
        }
      }}
      role="button"
      tabIndex={0}
      data-testid="group-youtube-video"
    >
      <div className="relative h-[92px] w-[142px] shrink-0 overflow-hidden rounded-xl bg-white/[0.08] max-[700px]:h-[76px] max-[700px]:w-[112px]">
        {thumbnail ? (
          <img src={thumbnail} alt="" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full items-center justify-center text-white/20"><Video size={20} aria-hidden="true" /></div>
        )}
        <span className="absolute right-2 top-2 rounded-md bg-black/65 px-1.5 py-1 text-[10px] text-white">🔒</span>
      </div>
      <div className="flex min-w-0 flex-col justify-between">
        <div className="min-w-0">
          <p className="truncate text-[15px] font-semibold leading-tight text-white" title={video.title}>
            {video.title || "Video senza titolo"}
          </p>
          <p className="mt-1 truncate font-mono text-[11px] text-[#9aa0aa]" title={`${video.channel_name || `Account #${video.platform_account_id}`} · ${video.youtube_video_id}`}>
            {video.channel_name || `Account #${video.platform_account_id}`} <span className="px-1 text-white/30">·</span> {video.youtube_video_id}
          </p>
        </div>
        <div className="mt-2 flex min-w-0 items-end justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <span title={publication.label} className={cn("inline-flex h-7 min-w-[30px] items-center justify-center rounded-lg border px-2 text-[14px]", toneClasses[publication.tone])}>
              🔒
              <span className="sr-only">{publication.label}</span>
            </span>
            <span title={`Lingua: ${(video.language?.trim() || "non impostata").toUpperCase()}`} className="inline-flex h-7 min-w-[30px] items-center justify-center rounded-lg border border-violet-500/20 bg-violet-500/[0.10] px-2 text-[15px]">{languageFlag(video.language)}</span>
            {formatPublishAt(video.publish_at) && (
              <span className="rounded-lg border border-blue-500/20 bg-blue-500/[0.08] px-2 py-1 text-[10px] font-semibold text-blue-200" title="Orario di pubblicazione programmato">
                {formatPublishAt(video.publish_at)}
              </span>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <a href={watchUrl} target="_blank" rel="noopener noreferrer" onClick={(event) => event.stopPropagation()} className="grid h-8 w-8 place-items-center rounded-lg text-[#cdd2da] hover:bg-white/[0.08] hover:text-white" title="Apri su YouTube" aria-label="Apri su YouTube">
              <ExternalLink size={14} aria-hidden="true" />
            </a>
            <button type="button" onClick={(event) => { event.stopPropagation(); onThumbnail(video); }} disabled={openingVideoID !== null} className="grid h-8 w-8 place-items-center rounded-lg text-violet-300 hover:bg-violet-500/[0.12] hover:text-violet-200 disabled:cursor-wait disabled:opacity-60" title="Modifica copertina" aria-label="Modifica copertina">
              {openingVideoID === video.youtube_video_id ? <Loader2 size={14} className="animate-spin" /> : <Edit3 size={14} aria-hidden="true" />}
            </button>
            <button type="button" onClick={(event) => { event.stopPropagation(); onPreview(video); }} className="grid h-8 w-8 place-items-center rounded-lg text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" title="Altre azioni" aria-label="Altre azioni">
              <MoreHorizontal size={15} aria-hidden="true" />
            </button>
          </div>
        </div>
      </div>
    </article>
  );
}
