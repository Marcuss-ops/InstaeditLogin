/**
 * Channel content client — typed wrapper over
 * `GET /api/v1/accounts/{accountId}/content`.
 *
 * Returns the list of videos (ContentItem rows) on a single
 * channel with cursor-based pagination and an optional privacy
 * filter (`?privacy=private|unlisted|public` — `"all"` is the UI
 * union's client-only "no filter" value and is dropped from the
 * URL via {@link buildPrivacyParam}).
 *
 * Defaults:
 *   • `limit=20` — matches the spec for the channel page (Blocco #2).
 *     Callers may override (the existing AccountDetails page uses
 *     the same default for its private-only video list).
 *   • No `privacy` value when the filter is `"all"`.
 *   • Cursor is optional — when omitted the server returns the
 *     first page. When present, it is sent as `?cursor=…`).
 *
 * Response shape:
 *   `{ items: ChannelVideo[], next_cursor?: string }`
 *
 *   `next_cursor` is the opaque pagination token the server
 *   returns for the next page. Absence means "no more results".
 *
 * Errors:
 *   authedFetch throws AuthError (401, session expired → caller
 *   navigates to /login) and ApiError (other 4xx/5xx, surfaces
 *   message in kind='error' from the hook). Same contract as
 *   {@link channelsApi.listYouTubeChannelsAndWorkspaces}.
 *
 * Notes for future editors:
 *   • The endpoint is tenant-scoped server-side; the bearer token
 *     must belong to a user that owns accountId. The hook's
 *     RefusedError state (401) is the singleton way the API
 *     surfaces "wrong tenant" without leaking details client-side.
 *   • The `properties` key on ChannelVideo is unparsed passthrough
 *     for server extension metadata — do not narrow without a
 *     schema doc.
 */
import { authedFetch } from "../../../lib/auth";
import type {
  ChannelVideo,
  PrivacyFilter,
} from "../types";
import { buildPrivacyParam } from "../types";

/**
 * Public re-export so consumers can import both the api function
 * and its response types from a single barrel — same pattern as
 * `channelsApi.ts` re-exporting `PlatformAccount` /
 * `Workspace` from `types/uploads`.
 */
export type { ChannelVideo, PrivacyFilter } from "../types";

/** Query-string options for GET /content. */
export interface ListChannelContentOptions {
  accountId: number;
  /**
   * Filter by server-side privacy value. The UI union's
   * `"all"` is dropped from the URL entirely (see
   * {@link buildPrivacyParam}).
   */
  privacy?: PrivacyFilter;
  /** Defaults to 20. Server caps observed in production at 50. */
  limit?: number;
  /** Opaque server token from a prior page's `next_cursor`. */
  cursor?: string;
  /** Shared by the wizard/hook to cancel mid-flight on unmount. */
  signal?: AbortSignal;
}

/** Response payload from GET /content. */
export interface ChannelContentPage {
  items: ChannelVideo[];
  /** Opaque pagination token; absent ⇒ last page. */
  next_cursor?: string;
}

const DEFAULT_LIMIT = 20;

/**
 * Build the canonical URL for the content endpoint.
 *
 *   /api/v1/accounts/{accountId}/content?limit=20&privacy=private
 *
 * Always includes `limit` (the server uses a sane default but
 * we set it explicitly so the wire payload is reproducible).
 */
function buildContentUrl(opts: ListChannelContentOptions): string {
  const params = new URLSearchParams();
  params.set("limit", String(opts.limit ?? DEFAULT_LIMIT));
  const privacyParam = opts.privacy
    ? buildPrivacyParam(opts.privacy)
    : undefined;
  if (privacyParam) {
    params.set("privacy", privacyParam);
  }
  if (opts.cursor) {
    params.set("cursor", opts.cursor);
  }
  const qs = params.toString();
  return `/api/v1/accounts/${opts.accountId}/content${qs ? `?${qs}` : ""}`;
}

/**
 * GET /api/v1/accounts/{accountId}/content — list videos on
 * a single channel with cursor pagination.
 *
 * AuthError (401) is thrown so callers can navigate to /login;
 * ApiError (other 4xx/5xx) surfaces the server's typed message.
 */
export async function listChannelContent(
  opts: ListChannelContentOptions,
): Promise<ChannelContentPage> {
  const url = buildContentUrl(opts);
  const resp = await authedFetch(url, { signal: opts.signal });
  const data = (await resp.json()) as Partial<ChannelContentPage>;
  return {
    items: data.items ?? [],
    ...(data.next_cursor != null ? { next_cursor: data.next_cursor } : {}),
  };
}
