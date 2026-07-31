import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError, ApiError, fetchSession } from "../../lib/auth";
import {
  buildTree,
  type FetchState,
  type Group,
  type PlatformAccount,
  type TreeNode,
} from "./groupsTypes";

async function loadAccountsByGroup(
  groups: Group[],
  allAccounts: PlatformAccount[],
  signal: AbortSignal,
): Promise<Map<number, PlatformAccount[]>> {
  if (groups.length === 0) {
    return new Map();
  }
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const accountIndex = new Map(allAccounts.map((a) => [a.id, a]));
  const respLists = await Promise.all(
    groups.map(async (g) => {
      try {
        const r = await authedFetch(`/api/v1/groups/${g.id}/accounts`, { signal });
        const d = (await r.json()) as { account_ids: number[] };
        const mapped: PlatformAccount[] = [];
        for (const id of d.account_ids ?? []) {
          const acc = accountIndex.get(id);
          if (acc) mapped.push(acc);
        }
        return [g.id, mapped] as const;
      } catch {
        return [g.id, [] as PlatformAccount[]] as const;
      }
    }),
  );
  return new Map(respLists);
}

export function useGroupsData() {
  
    const navigate = useNavigate();
    const abortRef = useRef<AbortController | null>(null);
    const [state, setState] = useState<FetchState>({ kind: "loading" });
    const [selectedGroupId, setSelectedGroupId] = useState<number | null>(null);
    const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null);
    const [newGroupName, setNewGroupName] = useState("");
    const [creatingGroup, setCreatingGroup] = useState(false);
  
    const load = useCallback(async () => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setState({ kind: "loading" });
  
      try {
        const session = await fetchSession();
        if (controller.signal.aborted) return;
        if (!session) {
          navigate("/login", { replace: true });
          return;
        }
        const meResp = await authedFetch("/api/v1/auth/me", { signal: controller.signal });
        if (controller.signal.aborted) return;
        const meData = (await meResp.json()) as { workspace_id: number };
        const workspaceId = meData.workspace_id;
        const [groupsResp, accountsResp] = await Promise.all([
          authedFetch(`/api/v1/groups/?workspace_id=${workspaceId}`, { signal: controller.signal }),
          authedFetch("/api/v1/accounts", { signal: controller.signal }),
        ]);
        if (controller.signal.aborted) return;
        const groupsData = (await groupsResp.json()) as { groups: Group[] };
        const accountsData = (await accountsResp.json()) as { accounts: PlatformAccount[] };
        // Bulk-load group accounts in parallel so the tree can render
        // each node's chip list without an extra fetch on group-click.
        // Returned as a Map<groupID, PlatformAccount[]> — passed to
        // buildTree so TreeNode.accounts is populated for every node.
        const accountsByGroup = await loadAccountsByGroup(
          groupsData.groups ?? [],
          accountsData.accounts ?? [],
          controller.signal,
        );
        setState({
          kind: "ready",
          groups: groupsData.groups ?? [],
          accounts: accountsData.accounts ?? [],
          workspaceId,
          accountsByGroup,
        });
      } catch (err) {
        if (controller.signal.aborted) return;
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const message = err instanceof ApiError ? err.message : "Unable to load groups.";
        setState({ kind: "error", message });
      }
    }, [navigate]);
  
    useEffect(() => {
      void load();
      return () => abortRef.current?.abort();
    }, [load]);
  
    const tree = useMemo(() => {
      if (state.kind !== "ready") return [];
      return buildTree(state.groups, state.accountsByGroup);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [state]);
  
    const selectedGroup = useMemo(() => {
      if (state.kind !== "ready" || selectedGroupId == null) return null;
      const findNode = (nodes: TreeNode[]): TreeNode | null => {
        for (const n of nodes) {
          if (n.id === selectedGroupId) return n;
          const child = findNode(n.children);
          if (child) return child;
        }
        return null;
      };
      return findNode(tree);
    }, [state, tree, selectedGroupId]);
  
    const selectedAccount = useMemo(() => {
      if (state.kind !== "ready" || selectedAccountId == null) return null;
      return state.accounts.find((a) => a.id === selectedAccountId) ?? null;
    }, [state, selectedAccountId]);
  
    const handleCreateGroup = useCallback(async (parentId?: number) => {
      if (!newGroupName.trim() || state.kind !== "ready") return;
      setCreatingGroup(true);
      try {
        const body = {
          workspace_id: state.workspaceId,
          parent_group_id: parentId ?? null,
          name: newGroupName.trim(),
        };
        await authedFetch("/api/v1/groups/", {
          method: "POST",
          body: JSON.stringify(body),
        });
        setNewGroupName("");
        await load();
      } finally {
        setCreatingGroup(false);
      }
    }, [newGroupName, state, load]);

  return {
    state,
    setState,
    selectedGroupId,
    setSelectedGroupId,
    selectedAccountId,
    setSelectedAccountId,
    newGroupName,
    setNewGroupName,
    creatingGroup,
    load,
    handleCreateGroup,
    tree,
    selectedGroup,
    selectedAccount,
  };
}
