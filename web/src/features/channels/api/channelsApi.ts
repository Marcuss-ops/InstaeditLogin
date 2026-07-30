/**
 * Typed channel/workspace manifest client.
 *
 * `GET /api/v1/accounts` returns the full list of connected
 * accounts across all platforms (YouTube, Google Drive, TikTok,
 * Instagram, …). Server-side filtering by `?platform=youtube` is
 * NOT supported today (the audit doc flagged it as a gap), so we
 * filter client-side. This is fine for the wizard scope: a user
 * has at most a few dozen accounts and the filter runs in memory.
 *
 * `GET /api/v1/workspaces` returns the workspace manifests the
 * user is a member of. Step 3 needs `workspace_id` for the
 * `CreatePostRequest` payload, so the wizard loads it alongside
 * the channel list to avoid a second round-trip.
 *
 * Both calls accept a shared `AbortSignal`; the wizard passes the
 * hook-owned controller so unmount cancels mid-flight.
 */
import { authedFetch } from "../../../lib/auth";
import type { PlatformAccount, Workspace } from "../../../types/uploads";

interface AccountsResponse {
  accounts?: PlatformAccount[];
}

interface WorkspacesResponse {
  workspaces?: Workspace[];
}

export interface YouTubeChannelsAndWorkspaces {
  channels: PlatformAccount[];
  workspaces: Workspace[];
}

/**
 * Public re-export of the upstream types so callers don't have to
 * reach into `../../../types/uploads`. Mirrors the same pattern
 * the publishing feature uses for `MediaAsset` (re-exported from
 * `mediaApi`).
 */
export type { PlatformAccount, Workspace };

const ACCOUNTS_PATH = "/api/v1/accounts";
const WORKSPACES_PATH = "/api/v1/workspaces";

export function filterYouTube(accounts: PlatformAccount[]): PlatformAccount[] {
  return accounts.filter((a) => a.platform === "youtube");
}

/**
 * Load accounts + workspaces in parallel using a shared signal.
 * Returns ONLY the YouTube channels (filtered client-side); other
 * platforms (drive, instagram, etc.) are dropped here so the
 * wizard UI doesn't have to know about them.
 *
 * Caller-side conventions:
 *   • AuthError is re-thrown (router-level ProtectedRoute handles
 *     the redirect to /login)
 *   • ApiError surfaces the server's typed message
 */
export async function listYouTubeChannelsAndWorkspaces(
  signal?: AbortSignal,
): Promise<YouTubeChannelsAndWorkspaces> {
  const [accountsResp, workspacesResp] = await Promise.all([
    authedFetch(ACCOUNTS_PATH, { signal }),
    authedFetch(WORKSPACES_PATH, { signal }),
  ]);
  const accounts =
    ((await accountsResp.json()) as AccountsResponse).accounts ?? [];
  const workspaces =
    ((await workspacesResp.json()) as WorkspacesResponse).workspaces ?? [];
  return { channels: filterYouTube(accounts), workspaces };
}
