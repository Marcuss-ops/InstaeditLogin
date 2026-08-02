import { CheckCircle2, ExternalLink, Tv } from "lucide-react";
import { Link } from "react-router-dom";
import type { PostTarget } from "../../features/publishing/api/types";

/**
 * SuccessCard — shown when EVERY target is published. Renders one
 * row per published target so each channel's video_id is visible,
 * with "Visualizza nel canale" + "Apri su YouTube" CTAs.
 */
export function SuccessCard({ targets }: { targets: PostTarget[] }) {
  return (
    <div
      className="rounded-2xl border border-emerald-500/30 bg-emerald-500/[0.06] p-6 md:p-8"
      data-testid="success-card"
    >
      <div className="flex items-center gap-3 mb-4">
        <CheckCircle2
          size={28}
          className="text-emerald-300 shrink-0"
          aria-hidden="true"
        />
        <div className="text-lg font-semibold text-emerald-100">
          Video caricato correttamente
        </div>
      </div>

      {/* Multi-target posts: one row per published target so each
          channel's video_id is visible. The aggregate headline stays
          at the top because the parent gate is
          `every(t.status === "published")`. */}
      <div className="space-y-3">
        {targets.map((t) => (
          <SuccessTargetRow key={t.id} target={t} />
        ))}
      </div>
    </div>
  );
}

function SuccessTargetRow({ target }: { target: PostTarget }) {
  const videoId = target.external_id ?? null;
  const fallbackUrl = videoId
    ? `https://www.youtube.com/watch?v=${encodeURIComponent(videoId)}`
    : null;
  const ytUrl = target.public_url ?? fallbackUrl;
  const channelUrl = videoId
    ? `/app/dashboard-channels/${target.platform_account_id}?video=${encodeURIComponent(videoId)}`
    : `/app/dashboard-channels/${target.platform_account_id}`;
  return (
    <div
      className="rounded-xl border border-emerald-500/20 bg-emerald-500/[0.04] px-4 py-3"
      data-testid={`success-row-${target.id}`}
    >
      <div className="text-xs text-[#9aa0aa] font-mono break-all mb-2">
        target_id={target.id} · video_id={videoId ?? "(sconosciuto)"}
      </div>
      <div className="flex flex-wrap gap-2 mb-3 text-xs">
        <span className="rounded-md border border-white/10 bg-white/[0.04] px-2 py-1 text-[#cdd2da]">
          Privacy effettiva: {target.actual_privacy ?? target.privacy_status ?? "in verifica"}
        </span>
        <span className="rounded-md border border-white/10 bg-white/[0.04] px-2 py-1 text-[#cdd2da]">
          YouTube sync: {target.youtube_sync_status ?? "in verifica"}
        </span>
      </div>
      <div className="flex flex-col sm:flex-row gap-2">
        <Link
          to={channelUrl}
          className="inline-flex items-center justify-center gap-2 rounded-lg bg-white text-[#030308] px-3 py-1.5 text-sm font-semibold hover:bg-[#e8ecf2] transition-colors"
          data-testid={`view-channel-button-${target.id}`}
        >
          <Tv size={14} aria-hidden="true" />
          Visualizza nel canale
        </Link>
        {ytUrl && (
          <a
            href={ytUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center justify-center gap-2 rounded-lg border border-white/15 bg-white/[0.06] hover:bg-white/[0.12] text-white px-3 py-1.5 text-sm font-medium transition-colors"
            data-testid={`open-youtube-button-${target.id}`}
          >
            <ExternalLink size={14} aria-hidden="true" />
            Apri su YouTube
          </a>
        )}
      </div>
    </div>
  );
}
