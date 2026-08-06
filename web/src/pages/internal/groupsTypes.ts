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
