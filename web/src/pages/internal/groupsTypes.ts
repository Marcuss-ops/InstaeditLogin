export type Group = {
  id: number;
  workspace_id: number;
  parent_group_id?: number | null;
  name: string;
};

export type GroupAccountSummary = Group & {
  account_ids: number[];
};

export type PlatformAccount = {
  id: number;
  workspace_id?: number;
  platform: string;
  username: string;
  /** Cached channel avatar returned by GET /api/v1/accounts. */
  avatar_url?: string;
  platform_user_id: string;
  language?: string;
  status: string;
  account_state?: "valid" | "reconnect_required" | "suspended" | "deleted";
  is_publishable?: boolean;
  metadata?: Record<string, unknown>;
  created_at: string;
};

export type FetchState =
  | { kind: "loading" }
  | {
      kind: "ready";
      groups: Group[];
      accounts: PlatformAccount[];
      workspaceId: number;
      /** Resolved publishable accounts per group (drives the tree). */
      accountsByGroup: Map<number, PlatformAccount[]>;
      /** Raw account_ids per group from the aggregate — includes non-publishable members that accountsByGroup filters out. */
      groupAccountIDs: Map<number, number[]>;
    }
  | { kind: "error"; message: string };

export type TreeNode = Group & { children: TreeNode[]; accounts: PlatformAccount[] };

export const PLATFORM_GRADIENT: Record<string, string> = {
  facebook: "from-blue-500 to-blue-700",
  instagram: "from-pink-500 to-amber-500",
  threads: "from-zinc-700 to-zinc-900",
  tiktok: "from-fuchsia-500 to-rose-500",
  twitter: "from-sky-400 to-sky-600",
  youtube: "from-red-500 to-red-700",
  linkedin: "from-blue-600 to-indigo-700",
  google_drive: "from-emerald-500 to-emerald-700",
  "google-drive": "from-emerald-500 to-emerald-700",
};

export function buildTree(
  groups: Group[],
  accountsByGroup: Map<number, PlatformAccount[]>,
): TreeNode[] {
  const map = new Map<number, TreeNode>();
  groups.forEach((g) => map.set(g.id, { ...g, children: [], accounts: [] }));
  const roots: TreeNode[] = [];
  groups.forEach((g) => {
    const node = map.get(g.id)!;
    node.accounts = accountsByGroup.get(g.id) ?? [];
    if (g.parent_group_id) {
      const parent = map.get(g.parent_group_id);
      if (parent) parent.children.push(node);
      else roots.push(node); // orphan parent (deleted) → root
    } else {
      roots.push(node);
    }
  });
  return roots;
}
