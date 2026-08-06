import { memo, type MouseEvent } from "react";
import { ExternalLink, Video } from "lucide-react";
import { withThumbnailCacheBust } from "../../features/channels/utils/thumbnailUrl";

export type ContentMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

export type ContentItem = {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
  metrics?: ContentMetric[];
  properties?: Record<string, unknown>;
};

export const AccountDetailsVideoCard = memo(function AccountDetailsVideoCard({
  item,
  onEditThumbnail,
  cacheBust,
}: {
  item: ContentItem;
  onEditThumbnail?: (item: ContentItem) => void;
  /** Cache-bust key from the page's content state. */
  cacheBust?: number;
}) {
  const handleEdit = (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    onEditThumbnail?.(item);
  };

  return (
    <div className="flex gap-4 p-3 rounded-xl bg-white/[0.03] border border-white/[0.06] hover:bg-white/[0.06] transition-colors no-underline group">
      <div className="w-40 h-24 rounded-lg bg-white/[0.08] overflow-hidden shrink-0 relative">
        {item.thumbnail_url ? (
          <img
            src={withThumbnailCacheBust(item.thumbnail_url, cacheBust)}
            alt={item.title ?? ""}
            loading="lazy"
            decoding="async"
            className="w-full h-full object-cover"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <Video size={20} className="text-white/20" />
          </div>
        )}
        {item.duration && (
          <span className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded bg-black/70 text-[10px] text-white font-medium">
            {formatDuration(item.duration)}
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
          {item.metrics?.map((metric) => (
            <span key={metric.key}>
              {metric.display_value} {metric.label.toLowerCase()}
            </span>
          ))}
        </div>
      </div>
      <div className="flex flex-col items-end justify-center gap-2 shrink-0">
        <a
          href={item.public_url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-white/[0.06] border border-white/[0.08] text-[11px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors no-underline"
          onClick={(event) => event.stopPropagation()}
        >
          Open on YouTube <ExternalLink size={12} />
        </a>
        <button
          type="button"
          onClick={handleEdit}
          className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-blue-500/10 border border-blue-500/20 text-[11px] font-semibold text-blue-400 hover:bg-blue-500/20 hover:text-blue-300 transition-colors"
        >
          Modifica copertina
        </button>
      </div>
    </div>
  );
});

function formatDuration(iso: string): string {
  const match = iso.match(/PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?/);
  if (!match) return iso;
  const hours = match[1] ? parseInt(match[1]) : 0;
  const minutes = match[2] ? parseInt(match[2]) : 0;
  const seconds = match[3] ? parseInt(match[3]) : 0;
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}
