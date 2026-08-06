/**
 * Typed channel/workspace manifest client.
 *
 * `GET /api/v1/accounts` returns the full list of connected
 * accounts across all platforms (YouTube, Google Drive, TikTok,
 * Instagram, …). We filter the returned list client-side so the
 * wizard receives only YouTube channels.
 */
import { authedFetch } from "../../../lib/auth";
import {
  isPublishableAccount,
  type PlatformAccount,
  type Workspace,
} from "../../../types/uploads";

interface AccountsResponse {
  accounts?: PlatformAccount[];
  next_cursor?: string;
  has_more?: boolean;
}

interface WorkspacesResponse {
  workspaces?: Workspace[];
}

export interface YouTubeChannelsAndWorkspaces {
  channels: PlatformAccount[];
  workspaces: Workspace[];
}

export type { PlatformAccount, Workspace };

const ACCOUNTS_PATH = "/api/v1/accounts";
const WORKSPACES_PATH = "/api/v1/workspaces";

/** Follow account cursors so callers keep seeing the complete manifest. */
export async function listAllAccounts(signal?: AbortSignal): Promise<PlatformAccount[]> {
  const accounts: PlatformAccount[] = [];
  let cursor: string | undefined;
  do {
    const params = new URLSearchParams({ limit: "100" });
    if (cursor) params.set("cursor", cursor);
    const path = cursor ? `${ACCOUNTS_PATH}?${params.toString()}` : ACCOUNTS_PATH;
    const response = await authedFetch(path, { signal });
    const page = (await response.json()) as AccountsResponse;
    accounts.push(...(page.accounts ?? []));
    cursor = page.has_more ? page.next_cursor : undefined;
  } while (cursor);
  return accounts;
}

export function filterYouTube(accounts: PlatformAccount[]): PlatformAccount[] {
  return accounts.filter(
    (account) => account.platform === "youtube" && isPublishableAccount(account),
  );
}

export async function listYouTubeChannelsAndWorkspaces(
  signal?: AbortSignal,
): Promise<YouTubeChannelsAndWorkspaces> {
  const [accounts, workspacesResp] = await Promise.all([
    listAllAccounts(signal),
    authedFetch(WORKSPACES_PATH, { signal }),
  ]);
  const workspaces =
    ((await workspacesResp.json()) as WorkspacesResponse).workspaces ?? [];
  return { channels: filterYouTube(accounts), workspaces };
}
