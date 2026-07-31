/**
 * Thumbnail cache-buster for the channel feature and shared video grids.
 *
 * Why this exists: The browser caches <img> URLs by their src string.
 * After the Dark Editor flips a thumbnail, the server MAY return the
 * same `thumbnail_url` for the same `external_id` (YouTube CDN paths
 * are stable per-video). Without a cache-buster, the browser keeps
 * rendering the OLD thumbnail from its disk cache even after the API
 * confirms the new asset is live on YouTube.
 *
 * The fix is to append a temporal query parameter that changes on
 * every successful refetch — this forces the browser to fetch from
 * origin on the next render. The helper accepts the timestamp from
 * the hook's state (NOT generated per-render, which would invalidate
 * the cache on every React re-render and cause flicker).
 *
 * Why a YouTube-only filter: cache-busting a signed S3 URL (or any
 * signed asset URL) BREAKS the URL signature — the signature covers
 * query params + headers. We restrict the helper to the YouTube
 * CDN host to avoid silently breaking asset fetches elsewhere when
 * the helper is reused for non-YouTube thumbnails in the future.
 *
 * Tests: see utils/__tests__/thumbnailUrl.test.ts (or
 * features/channels/utils/__tests__/thumbnailUrl.test.ts).
 */

/**
 * Tiny YouTube CDN host detector. Covers the public thumbnail
 * hosts that the YouTube Data API exposes:
 *
 *   i.ytimg.com      — main image CDN (hqdefault, mqdefault, etc.)
 *   img.youtube.com  — alternate CDN
 *   youtu.be         — short-URL host (resolved to i.ytimg.com
 *                      internally by YouTube on the server side,
 *                      but client-side 302s can be cached too)
 *
 * Not exhaustive (legitimate thumbnails may come from a custom
 * CDN the YPP partner configures) but covers the common case. A
 * 404 on a custom host is acceptable; the user sees the placeholder
 * card instead of a stale image.
 *
 * The check is intentionally a substring match on the URL — a
 * full URL parse would need a WHATWG-URL polyfill in some
 * older browsers and is overkill for a helper called in hot
 * render paths.
 */
const YOUTUBE_THUMBNAIL_HOSTS = ["i.ytimg.com", "img.youtube.com"];

/**
 * Apply a cache-buster query parameter to a thumbnail URL.
 *
 * Parameters:
 *   - `url`: the raw `thumbnail_url` from the API. May be
 *     `undefined` or empty when the server hasn't built a CDN
 *     URL yet.
 *   - `bustKey`: a stable timestamp or revision token the caller
 *     owns. REQUIRED for the cache-bust to take effect — the
 *     helper does NOT call `Date.now()` itself because doing so
 *     in the render path would break the cache invariant.
 *
 * Returns:
 *   - `undefined` when the input is missing (the card renders a
 *     placeholder icon in that case).
 *   - The original URL unchanged when the URL isn't a YouTube CDN
 *     host (signature-safety contract).
 *   - The URL with `?v={bustKey}` or `&v={bustKey}` (preserving
 *     any pre-existing query params) for YouTube CDN URLs.
 */
export function withThumbnailCacheBust(
  url: string | undefined | null,
  bustKey: string | number | undefined | null,
): string | undefined {
  if (url == null || url === "") return undefined;
  if (bustKey == null) return url;
  const isYouTubeCdn = YOUTUBE_THUMBNAIL_HOSTS.some(
    (host) => url.includes(host),
  );
  if (!isYouTubeCdn) return url;
  const sep = url.includes("?") ? "&" : "?";
  return `${url}${sep}v=${encodeURIComponent(String(bustKey))}`;
}
