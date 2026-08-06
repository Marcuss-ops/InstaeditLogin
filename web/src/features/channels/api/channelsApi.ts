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

// Shared accounts-manifest cache (N+1 DoD — "no duplicate refetch between
// header and page"). The header switcher (AccountSwitcher) and the Linking
// page both load the account manifest on mount; without sharing they fire
// TWO identical GET /accounts requests. This module-level cache collapses
// them into ONE network request: within a 60s stale window callers reuse
// the last value, and concurrent callers share the in-flight promise
// instead of double-fetching. force:true bypasses the cache for
// user-initiated refreshes. clearAccountsCache resets it (logout, tests).
const ACCOUNTS_STALE_MS = 60_000;

let accountsCache: { value: PlatformAccount[]; at: number } | null = null;
let accountsInFlight: Promise<PlatformAccount[]> | null = null;

export function clearAccountsCache(): void {
  accountsCache = null;
  accountsInFlight = null;
}

async function fetchAllAccountPages(): Promise<PlatformAccount[]> {
  const accounts: PlatformAccount[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  for (let pageNumber = 0; ; pageNumber += 1) {
    if (pageNumber >= 10_000) {
      throw new Error("account pagination exceeded the maximum page count");
    }
    const params = new URLSearchParams({ limit: "100" });
    if (cursor) params.set("cursor", cursor);
    const response = await authedFetch(`${ACCOUNTS_PATH}?${params.toString()}`);
    const page = (await response.json()) as AccountsResponse;
    accounts.push(...(page.accounts ?? []));
    if (!page.has_more) return accounts;
    if (!page.next_cursor || seenCursors.has(page.next_cursor)) {
      throw new Error("account pagination returned an invalid continuation cursor");
    }
    seenCursors.add(page.next_cursor);
    cursor = page.next_cursor;
  }
}

export interface ListAccountsOptions {
  /**
   * Caller unmount abort. The shared request itself is never aborted by a
   * single caller (it serves every concurrent caller); callers must keep
   * their own post-await aborted guard for unmount safety.
   */
  signal?: AbortSignal;
  /** Bypass the 60s cache — used by explicit user-initiated refreshes. */
  force?: boolean;
}

/** Follow account cursors so callers keep seeing the complete manifest. */
export async function listAllAccounts(
  options: ListAccountsOptions = {},
): Promise<PlatformAccount[]> {
  const now = Date.now();
  if (!options.force && accountsCache && now - accountsCache.at < ACCOUNTS_STALE_MS) {
    return accountsCache.value;
  }
  if (!accountsInFlight) {
    accountsInFlight = fetchAllAccountPages()
      .then((value) => {
        accountsCache = { value, at: Date.now() };
        return value;
      })
      .finally(() => {
        accountsInFlight = null;
      });
  }
  return accountsInFlight;
}

if (typeof window !== "undefined") {
  window.addEventListener("instaedit:session-cleared", clearAccountsCache);
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
    listAllAccounts({ signal }),
    authedFetch(WORKSPACES_PATH, { signal }),
  ]);
  const workspaces =
    ((await workspacesResp.json()) as WorkspacesResponse).workspaces ?? [];
  return { channels: filterYouTube(accounts), workspaces };
}
