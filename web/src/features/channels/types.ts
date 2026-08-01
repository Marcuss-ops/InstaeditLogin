/**
 * Shared types for the channel feature.
 *
 * The channel page renders a header for a single YouTube account + the
 * list of videos on it,
 * with privacy-chip filters and a highlight band for the just-uploaded
 * video (the `?video=…` query string). This file centralizes the
 * shapes the feature passes around so the page, the components, and
 * the upcoming channel-content hook can talk in one vocabulary.
 *
 * Conventions:
 *   • Keys mirror the server payload shape (`external_id`,
 *     `thumbnail_url`, `public_url`, `privacy`, `status`,
 *     `published_at`, `duration`, `metrics`) — the GET
 *     `/api/v1/accounts/{id}/content` endpoint returns ContentItems
 *     with these exact field names.
 *   • `privacy` is a free-form string because the server may return
 *     values outside the 3-state set (legacy rows, drafts, etc.). The
 *     UI narrows it to the PrivacyFilter union via
 *     {@link normalizePrivacy} for chip colour mapping.
 *   • `ChannelAccount` extends the existing `PlatformAccount` (from
 *     `types/uploads.ts`) with the optional `resource` blob that
 *     `/api/v1/accounts/{id}` returns. AccountDetailsPage already
 *     types this inline; we mirror it here so the channel-page
 *     header can render from the same payload without re-declaring.
 */

/** Filters exposed by the ChannelVideoFilters chip row. */
export type PrivacyFilter = "all" | "private" | "unlisted" | "public";

/**
 * Privacy values the server actually accepts in
 * `?privacy=…`. The `PrivacyFilter` union has the additional
 * `"all"` for the UI-only "no filter" state — see
 * {@link buildPrivacyParam}.
 */
export type ApiPrivacy = "private" | "unlisted" | "public";

/** Metric row on a ContentItem, e.g. `{ key: "views", value: 1234, display_value: "1.2K", label: "Views" }`. */
export interface ChannelVideoMetric {
  key: string;
  label: string;
  value: number;
  display_value: string;
}

/** A single video from GET /api/v1/accounts/{id}/content. */
export interface ChannelVideo {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  /** Server may return additional values (legacy rows); strict union lives in ApiPrivacy. */
  privacy?: string;
  /** Upload status — server-defined vocabulary (`processing`, `live`, `failed`, …). */
  status?: string;
  published_at?: string;
  /** ISO 8601 duration (`PT1H2M3S`). */
  duration?: string;
  metrics?: ChannelVideoMetric[];
  properties?: Record<string, unknown>;
}

/** Resource blob nested inside an account GET /api/v1/accounts/{id}. */
export interface ChannelAccountResource {
  display_name?: string;
  handle?: string;
  avatar_url?: string;
  banner_url?: string;
  public_url?: string;
  description?: string;
  metrics?: ChannelVideoMetric[];
  properties?: Record<string, unknown>;
  fetched_at?: string;
}

/** A channel's identity record. Mirrors AccountDetail's `account` shape (AccountDetailsPage). */
export interface ChannelAccount {
  id: number;
  platform: string;
  platform_user_id: string;
  username: string;
  status: string;
  account_state?: "valid" | "reconnect_required" | "suspended" | "deleted";
  is_publishable?: boolean;
  created_at: string;
  resource?: ChannelAccountResource;
}

/**
 * Narrow an arbitrary server-returned privacy string into the chip
 * taxonomy. Unknown values fall back to `"unknown"` (rendered as a
 * neutral chip) so a stale row never crashes the card.
 */
export type KnownPrivacy = "private" | "unlisted" | "public" | "unknown";

export function normalizePrivacy(raw: string | undefined | null): KnownPrivacy {
  switch (raw) {
    case "private":
    case "unlisted":
    case "public":
      return raw;
    default:
      return "unknown";
  }
}

/**
 * Visual tone tokens for the OAuth connection status chip on the
 * channel header (and any future re-use).
 *
 *   active / connected    → emerald (everything is fine)
 *   anything else         → amber   (needs attention: failed auth,
 *                                     pending reconnect, revoked
 *                                     token, etc.)
 *
 * The decision is intentionally binary (we don't tease apart every
 * server state into a separate colour) so the chip stays a quick
 * glance-and-act signal. Normalizes case so casing drift on the
 * server (active vs ACTIVE) doesn't bounce the chip to amber.
 */
export interface StatusTone {
  readonly bg: string;
  readonly border: string;
  readonly text: string;
}

export function getStatusTone(rawStatus: string | undefined | null): StatusTone {
  const s = (rawStatus ?? "").toLowerCase();
  const ok = s === "active" || s === "connected";
  return ok
    ? {
        bg: "bg-emerald-500/[0.08]",
        border: "border-emerald-500/[0.15]",
        text: "text-emerald-400",
      }
    : {
        bg: "bg-amber-500/[0.08]",
        border: "border-amber-500/[0.15]",
        text: "text-amber-400",
      };
}

/**
 * Translate the UI PrivacyFilter into the server query-string value.
 * `undefined` means "no filter" — the API treats an unfiltered call
 * as "all". `filters="all"` therefore omits the param entirely.
 */
export function buildPrivacyParam(filter: PrivacyFilter): string | undefined {
  return filter === "all" ? undefined : filter;
}

/**
 * Pull the views display value out of a video's metrics row.
 * Returns `undefined` if the metric isn't present (page renders a
 * `·` placeholder rather than risking a fake `0`).
 */
export function getViewsDisplay(
  metrics: ChannelVideoMetric[] | undefined,
): string | undefined {
  return metrics?.find((m) => m.key === "views")?.display_value;
}

/**
 * Build the fallback YouTube URL when `public_url` is missing.
 * Format: `https://youtu.be/{videoId}` — the canonical short
 * link YouTube resolves without a /watch?v= prefix.
 */
export function buildYouTubeFallbackUrl(videoId: string): string {
  return `https://youtu.be/${encodeURIComponent(videoId)}`;
}
