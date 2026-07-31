/**
 * Account-detail client — typed wrapper over `GET /api/v1/accounts/{id}`.
 *
 * Returns the full account record (id / platform / platform_user_id
 * / username / status / created_at + optional resource blob with
 * avatar/banner/handle/metrics). Companion to `channelsApi.ts`
 * which handles the LIST manifest.
 *
 * Used by the channel page to render the `ChannelHeader`.
 * AuthError (401) is thrown so callers can navigate to /login; other
 * 4xx/5xx surface as `ApiError`.
 *
 * Conventions:
 *   • Path uses the accountId as a numeric segment (the server
 *     validates ownership via the bearer token; we do not escape it
 *     because it's an integer from useParams).
 *   • The `signal` is the AbortController-owned signal from the
 *     hook, so unmount cancels mid-flight.
 */
import { authedFetch } from "../../../lib/auth";
import type { ChannelAccount } from "../types";

/** Public re-export so consumers can read both the api fn and its
 *  response type from one barrel — same pattern as channelsApi.ts. */
export type { ChannelAccount } from "../types";

/** Options for `getChannelAccount`. Accepts `signal` so the hook
 *  can cancel mid-flight on unmount. */
export interface GetChannelAccountOptions {
  accountId: number;
  signal?: AbortSignal;
}

/**
 * GET /api/v1/accounts/{id} — full account record for a single
 * platform account. Throws AuthError on 401; ApiError on others.
 */
export async function getChannelAccount(
  opts: GetChannelAccountOptions,
): Promise<ChannelAccount> {
  const resp = await authedFetch(
    `/api/v1/accounts/${opts.accountId}`,
    opts.signal ? { signal: opts.signal } : {},
  );
  return (await resp.json()) as ChannelAccount;
}
