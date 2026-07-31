/**
 * ChannelVideoCard — single video tile on the channel page
 * (/app/dashboard-channels/:accountId).
 *
 * Layout mirrors ContentVideoCard in AccountDetails for visual
 * consistency, with the channel page's thumbnail-edit affordance:
 *
 *   ┌─────────────────────────────────────────────────────────────┐
 *   │ [thumbnail w/ duration] [title + handle]   [privacy chip]    │
 *   │                      [external_id + date]  [status chip]    │
 *   │                      [views ▸ duration]                    │
 *   │                                          [Apri su YouTube]  │
 *   │                                          [Modifica cop.]    │
 *   └─────────────────────────────────────────────────────────────┘
 *
 * The card supports a "highlight" state matching the `?video=…`
 * query param on the channel page. When `highlightVideoId ===
 * video.external_id`, the card renders a subtle emerald ring + a
 * small "Appena caricato" badge so the user can see the just-uploaded
 * video even after switching away from the "Privati" chip.
 *
 * Buttons:
 *   • "Apri su YouTube" → external link to `public_url` (fallback
 *     `https://youtu.be/{external_id}` when the server omits the
 *     public_url — common for unlisted uploads that the server has
 *     not yet built a slug for).
 *   • "Modifica copertina" → calls `onEditThumbnail(video)` so the
 *     parent page keeps ownership of the editor-sessions POST.
 */
import { ExternalLink, Sparkles, Video } from "lucide-react";
import { cn } from "../../../lib/utils";
import type { ChannelVideo } from "../types";
import { buildYouTubeFallbackUrl, getViewsDisplay, normalizePrivacy } from "../types";
import { withThumbnailCacheBust } from "../utils/thumbnailUrl";

export interface ChannelVideoCardProps {
  video: ChannelVideo;
  /**
   * The `?video=…` value the parent parsed from the URL search
   * params. When it matches `video.external_id`, the card adds
   * the highlight ring + "Appena caricato" badge.
   */
  highlightVideoId?: string;
  /**
   * The cache-bust key from `useChannelContent().cacheBust`.
   * Forwarded to {@link withThumbnailCacheBust} so the YouTube
   * CDN image URL busts on every successful refetch — a new
   * thumbnail revision from the server therefore renders
   * immediately instead of being served from the browser's
   * `<img>` disk cache.
   *
   * Recommended: forward the hook's `cacheBust` directly:
   *   <ChannelVideoCard cacheBust={cacheBust} />
   *
   * When omitted, no bust is applied (helper no-ops); the card
   * still renders correctly, just without the freshness
   * guarantee.
   */
  cacheBust?: number;
  /**
   * Fires when the user clicks "Modifica copertina". The parent
   * page owns the editor-sessions POST so error toasts and
   * workspace lookup stay a page-level concern (the channel
   * page already loads workspaces via useYouTubeChannels).
   */
  onEditThumbnail: (video: ChannelVideo) => void;
}

function formatDuration(iso: string | undefined): string | null {
  if (!iso) return null;
  const match = iso.match(/PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?/);
  if (!match) return iso;
  const h = match[1] ? parseInt(match[1], 10) : 0;
  const m = match[2] ? parseInt(match[2], 10) : 0;
  const s = match[3] ? parseInt(match[3], 10) : 0;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

interface PrivacyChipStyle {
  readonly bg: string;
  readonly border: string;
  readonly text: string;
}

// NOTE for future editors:
//
// "unknown" is the hard fallback entry in PRIVACY_CHIP and
// STATUS_CHIP below. Removing it will break the `!` non-null
// assertions (privacyStyle.bg, statusStyle.bg, …) AND crash at
// runtime if a server row ever ships a privacy/status value
// outside the canonical set. Always keep "unknown" as the LAST
// map entry in each table. If you add a new enum value, append
// it ABOVE "unknown" — never at the bottom.

const PRIVACY_CHIP: Record<string, PrivacyChipStyle> = {
  private: {
    bg: "bg-emerald-500/[0.08]",
    border: "border-emerald-500/[0.18]",
    text: "text-emerald-300",
  },
  unlisted: {
    bg: "bg-amber-500/[0.08]",
    border: "border-amber-500/[0.18]",
    text: "text-amber-300",
  },
  public: {
    bg: "bg-blue-500/[0.08]",
    border: "border-blue-500/[0.18]",
    text: "text-blue-300",
  },
  unknown: {
    bg: "bg-white/[0.06]",
    border: "border-white/[0.12]",
    text: "text-[#9aa0aa]",
  },
};

const PRIVACY_CHIP_LABEL: Record<string, string> = {
  private: "Privato",
  unlisted: "Non in elenco",
  public: "Pubblico",
  unknown: "Sconosciuto",
};

interface StatusChipStyle {
  readonly bg: string;
  readonly border: string;
  readonly text: string;
}

const STATUS_CHIP: Record<string, StatusChipStyle> = {
  live: {
    bg: "bg-emerald-500/[0.08]",
    border: "border-emerald-500/[0.18]",
    text: "text-emerald-300",
  },
  processed: {
    bg: "bg-emerald-500/[0.08]",
    border: "border-emerald-500/[0.18]",
    text: "text-emerald-300",
  },
  published: {
    bg: "bg-emerald-500/[0.08]",
    border: "border-emerald-500/[0.18]",
    text: "text-emerald-300",
  },
  queued: {
    bg: "bg-blue-500/[0.08]",
    border: "border-blue-500/[0.18]",
    text: "text-blue-300",
  },
  publishing: {
    bg: "bg-blue-500/[0.08]",
    border: "border-blue-500/[0.18]",
    text: "text-blue-300",
  },
  processing: {
    bg: "bg-blue-500/[0.08]",
    border: "border-blue-500/[0.18]",
    text: "text-blue-300",
  },
  failed: {
    bg: "bg-red-500/[0.08]",
    border: "border-red-500/[0.18]",
    text: "text-red-300",
  },
  unknown: {
    bg: "bg-white/[0.06]",
    border: "border-white/[0.12]",
    text: "text-[#9aa0aa]",
  },
};

const STATUS_CHIP_LABEL: Record<string, string> = {
  live: "Live",
  processed: "Live",
  published: "Pubblicato",
  queued: "In coda",
  publishing: "In pubblicazione",
  processing: "In elaborazione",
  failed: "Fallito",
  unknown: "Sconosciuto",
};

export function ChannelVideoCard({
  video,
  highlightVideoId,
  cacheBust,
  onEditThumbnail,
}: ChannelVideoCardProps) {
  const isHighlighted = highlightVideoId === video.external_id;
  const normalizedPrivacy = normalizePrivacy(video.privacy);
  const privacyStyle = PRIVACY_CHIP[normalizedPrivacy]!;
  const rawStatus = (video.status ?? "unknown").toLowerCase();
  const normalizedStatus = STATUS_CHIP[rawStatus] ? rawStatus : "unknown";
  const statusStyle = STATUS_CHIP[normalizedStatus] ?? STATUS_CHIP["unknown"]!;
  const durationLabel = formatDuration(video.duration);
  const viewsDisplay = getViewsDisplay(video.metrics);
  // Cache-bust the thumbnail on every page-level fetch. The hook
  // bumps `cacheBust` IMMEDIATELY after a successful fetch, so
  // the prop change forces React to remount the <img> (the src
  // string changes) and the browser re-fetches the YouTube CDN
  // image. Confined to YouTube CDN hosts by the helper — see
  // utils/thumbnailUrl.ts for the S3-signed-URL safety contract.
  const thumbnailSrc = withThumbnailCacheBust(video.thumbnail_url, cacheBust);

  const handleEdit = (e: React.MouseEvent<HTMLButtonElement>) => {
    // Stop propagation so the wrapping <a>-as-card (if any future
    // page wraps the card in a link) doesn't double-fire.
    e.preventDefault();
    e.stopPropagation();
    onEditThumbnail(video);
  };

  return (
    <div
      className={cn(
        "relative flex gap-4 p-3 rounded-xl bg-white/[0.03] border transition-colors",
        "group",
        isHighlighted
          ? "border-emerald-500/60 ring-1 ring-emerald-500/30 bg-emerald-500/[0.04]"
          : "border-white/[0.06] hover:bg-white/[0.06]",
      )}
      data-testid="channel-video-card"
      data-highlighted={isHighlighted ? "true" : "false"}
    >
      {/* Highlight badge — only when matching ?video=… Kept inside
          the card bounds (top-left over the thumbnail corner) so it
          never overlaps sibling rows in a stacked card list. The
          z-10 wins against the duration pill overlay. */}
      {isHighlighted && (
        <span
          className="absolute top-2 left-2 z-10 inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-emerald-500 text-[#030308] text-[10px] font-bold shadow-[0_2px_8px_rgba(16,185,129,0.5)]"
          data-testid="channel-video-card-highlight-badge"
        >
          <Sparkles size={10} aria-hidden="true" />
          Appena caricato
        </span>
      )}

      {/* Thumbnail */}
      <div className="w-40 h-24 rounded-lg bg-white/[0.08] overflow-hidden shrink-0 relative">
        {thumbnailSrc ? (
          <img
            src={thumbnailSrc}
            alt={video.title ?? ""}
            className="w-full h-full object-cover"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <Video size={20} className="text-white/20" aria-hidden="true" />
          </div>
        )}
        {durationLabel && (
          <span
            className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded bg-black/70 text-[10px] text-white font-medium"
            data-testid="channel-video-card-duration"
          >
            {durationLabel}
          </span>
        )}
      </div>

      {/* Metadata column */}
      <div className="flex flex-col justify-between min-w-0 flex-1 py-0.5 gap-2">
        <div>
          <p
            className="text-[13px] font-semibold text-white truncate"
            data-testid="channel-video-card-title"
          >
            {video.title ?? "(senza titolo)"}
          </p>
          <p
            className="text-[11px] text-[#9aa0aa] truncate mt-0.5 font-mono"
            data-testid="channel-video-card-id"
          >
            {video.external_id}
          </p>
        </div>

        <div className="flex items-center gap-2 flex-wrap text-[11px]">
          <span
            className={cn(
              "inline-flex items-center px-2 py-0.5 rounded-full font-semibold border",
              privacyStyle.bg,
              privacyStyle.border,
              privacyStyle.text,
            )}
            data-testid="channel-video-card-privacy"
          >
            {PRIVACY_CHIP_LABEL[normalizedPrivacy]}
          </span>
          <span
            className={cn(
              "inline-flex items-center px-2 py-0.5 rounded-full border",
              statusStyle.bg,
              statusStyle.border,
              statusStyle.text,
            )}
            data-testid="channel-video-card-status"
          >
            {STATUS_CHIP_LABEL[normalizedStatus] ?? normalizedStatus}
          </span>
          {video.published_at && (
            <span
              className="text-[#9aa0aa]"
              data-testid="channel-video-card-published-at"
            >
              {new Date(video.published_at).toLocaleDateString()}
            </span>
          )}
          {viewsDisplay && (
            <span
              className="text-[#9aa0aa]"
              data-testid="channel-video-card-views"
            >
              {viewsDisplay} visualizzazioni
            </span>
          )}
        </div>
      </div>

      {/* Actions column */}
      <div className="flex flex-col items-end justify-center gap-2 shrink-0">
        <a
          href={video.public_url ?? buildYouTubeFallbackUrl(video.external_id)}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-white/[0.06] border border-white/[0.08] text-[11px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors no-underline"
          data-testid="channel-video-card-open"
        >
          Apri su YouTube <ExternalLink size={12} aria-hidden="true" />
        </a>
        <button
          type="button"
          onClick={handleEdit}
          className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-blue-500/10 border border-blue-500/20 text-[11px] font-semibold text-blue-400 hover:bg-blue-500/20 hover:text-blue-300 transition-colors"
          data-testid="channel-video-card-edit"
        >
          Modifica copertina
        </button>
      </div>
    </div>
  );
}
