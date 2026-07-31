/**
 * ChannelHeader — top banner for the single-channel page
 * (/app/dashboard-channels/:accountId).
 *
 * Extracted from the inline JSX in AccountDetailsPage to be the
 * canonical header for the channel-page. Layout:
 *
 *   ┌─────────────────────────────────────────────────────────────┐
 *   │ <─ Back                  [banner image, optional]           │
 *   │                                                              │
 *   │  [avatar]   Channel Name                [STATUS chip]        │
 *   │             @handle                                         │
 *   │              ▸ Open on YouTube                               │
 *   │                                            [Refresh btn]     │
 *   └─────────────────────────────────────────────────────────────┘
 *
 * The component is presentation-only — it never fetches. Parent
 * (the channel page) owns the auth state, the refresh action, and
 * the navigate("/app/linking") back target.
 *
 * Account prop is `undefined` while loading so the header renders
 * a skeleton shell rather than collapsing the layout (avoids a
 * CLS flash on every refetch).
 */
import { ArrowLeft, ExternalLink, RefreshCw, Loader2, Video } from "lucide-react";
import { cn } from "../../../lib/utils";
import type { ChannelAccount } from "../types";
import { getStatusTone } from "../types";

export interface ChannelHeaderProps {
  /** Undefined while the parent page is loading the channel. */
  account?: ChannelAccount;
  /** True while a sync/refresh request is in flight. */
  refreshing?: boolean;
  onRefresh: () => void;
  onBack: () => void;
}

const providerColorFallback =
  "bg-gradient-to-br from-white/20 to-white/10 text-white";

const STATUS_CHIP_BASE_CLS =
  "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-semibold border";

export function ChannelHeader({
  account,
  refreshing,
  onRefresh,
  onBack,
}: ChannelHeaderProps) {
  const isLoaded = account != null;
  const resource = account?.resource;
  const statusTone = getStatusTone(account?.status);

  return (
    <div
      className="rounded-2xl bg-[#1f1f2e] border border-white/[0.12] overflow-hidden mb-6"
      data-testid="channel-header"
    >
      {/* Banner — matches the AccountDetails banner style. Hidden when
          account is loading or banner_url is missing. */}
      {resource?.banner_url ? (
        <div className="h-32 w-full bg-white/[0.04]">
          <img
            src={resource.banner_url}
            alt=""
            className="w-full h-full object-cover"
          />
        </div>
      ) : (
        <div className="h-32 w-full bg-white/[0.04]" aria-hidden="true" />
      )}

      <div className="p-6">
        <div className="flex items-start gap-4">
          {/* Avatar */}
          {resource?.avatar_url ? (
            <img
              src={resource.avatar_url}
              alt=""
              className="w-16 h-16 rounded-full border-2 border-white/10 shrink-0"
              data-testid="channel-header-avatar"
            />
          ) : isLoaded ? (
            <div
              className={cn(
                "w-16 h-16 rounded-full flex items-center justify-center text-xl font-bold shrink-0",
                providerColorFallback,
              )}
              data-testid="channel-header-avatar-fallback"
              aria-hidden="true"
            >
              {(resource?.display_name ?? account!.username)?.charAt(0).toUpperCase() ?? "?"}
            </div>
          ) : (
            <div
              className="w-16 h-16 rounded-full bg-white/[0.06] animate-pulse shrink-0"
              aria-hidden="true"
            />
          )}

          {/* Title + handle + public link */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-3 flex-wrap">
              <h1
                className="text-[22px] font-extrabold text-white leading-tight"
                data-testid="channel-header-name"
              >
                {resource?.display_name ?? account?.username ?? "..."}
              </h1>
              {isLoaded && (
                <span
                  className={cn(
                    STATUS_CHIP_BASE_CLS,
                    statusTone.bg,
                    statusTone.border,
                    statusTone.text,
                  )}
                  data-testid="channel-header-status"
                >
                  {account!.status.toUpperCase()}
                </span>
              )}
            </div>

            {resource?.handle && (
              <p
                className="text-[14px] text-[#9aa0aa] mt-0.5"
                data-testid="channel-header-handle"
              >
                {resource.handle}
              </p>
            )}

            {(resource?.public_url || account?.username) && (
              <a
                href={
                  resource?.public_url ??
                  `https://youtube.com/@${encodeURIComponent(
                    account!.username,
                  )}`
                }
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-[13px] text-blue-400 hover:text-blue-300 mt-2 no-underline"
                data-testid="channel-header-public-link"
              >
                Apri su YouTube <ExternalLink size={12} aria-hidden="true" />
              </a>
            )}
          </div>

          {/* Refresh */}
          <button
            type="button"
            onClick={onRefresh}
            disabled={!isLoaded || refreshing}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-white/[0.06] border border-white/[0.08] text-[12px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors disabled:opacity-50"
            data-testid="channel-header-refresh"
          >
            {refreshing ? (
              <Loader2 size={12} className="animate-spin" aria-hidden="true" />
            ) : (
              <RefreshCw size={12} aria-hidden="true" />
            )}
            Aggiorna
          </button>
        </div>

        {/* Loading-only "channel content" placeholder. Keeps the page from
            collapsing to a bare refresh button. */}
        {!isLoaded && (
          <div className="flex items-center gap-2 mt-4 text-[12px] text-[#9aa0aa]">
            <Video size={14} className="animate-pulse" aria-hidden="true" />
            <span>Caricamento canale…</span>
          </div>
        )}

        {/* Back link — bottom-left of the card so it survives even when
            the avatar/handle area is short. Matches AccountDetails'
            visual language. */}
        <div className="mt-5 pt-4 border-t border-white/[0.06]">
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 text-[13px] text-[#9aa0aa] hover:text-white transition-colors"
            data-testid="channel-header-back"
          >
            <ArrowLeft size={14} aria-hidden="true" />
            Torna ai canali
          </button>
        </div>
      </div>
    </div>
  );
}
