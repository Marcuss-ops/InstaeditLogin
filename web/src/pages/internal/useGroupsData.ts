import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { authedFetch, AuthError, ApiError, fetchSession } from "../../lib/auth";
import { isPublishableAccount } from "../../types/uploads";
import { listAllAccounts } from "../../features/channels/api/channelsApi";
import {
  buildTree,
  type FetchState,
  type Group,
  type PlatformAccount,
  type TreeNode,
} from "./groupsTypes";

export function useGroupsData() {
    const LAST_GROUP_KEY = "instaedit:last-group-id";
    const navigate = useNavigate();
    const { groupId: routeGroupId } = useParams<{ groupId?: string }>();
    const abortRef = useRef<AbortController | null>(null);
    const [state, setState] = useState<FetchState>({ kind: "loading" });
    const [selectedGroupId, setSelectedGroupIdState] = useState<number | null>(() => {
      if (typeof window === "undefined") return null;
      const stored = Number(window.localStorage.getItem(LAST_GROUP_KEY));
      return Number.isFinite(stored) && stored > 0 ? stored : null;
    });
    const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null);
    const [newGroupName, setNewGroupName] = useState("");
    const [creatingGroup, setCreatingGroup] = useState(false);
    const [busyAccountId, setBusyAccountId] = useState<number | null>(null);
  
    // `silent` skips the intermediate loading state so assignment drops
    // reconcile smoothly without flashing the whole page to a skeleton.
    const load = useCallback(async (silent = false) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      if (!silent) setState({ kind: "loading" });
  
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
        const [groupsResp, accounts] = await Promise.all([
          authedFetch("/api/v1/groups/aggregate", { signal: controller.signal }),
          listAllAccounts({ signal: controller.signal }),
        ]);
        if (controller.signal.aborted) return;
        const groupsData = (await groupsResp.json()) as {
          groups: Array<Group & { account_ids?: number[] }>;
        };
        const accountsData = { accounts };
        // The groups UI is an operational publishing view: accounts that are
        // not active (revoked, suspended or requiring re-auth) must not be
        // selectable or displayed as usable channels.
        const activeAccounts = (accountsData.accounts ?? []).filter(isPublishableAccount);
        // The aggregate endpoint returns groups and direct memberships in
        // one workspace-scoped response. Resolve account IDs locally so the
        // tree never fans out into one request per group.
        const accountIndex = new Map(activeAccounts.map((account) => [account.id, account]));
        // Raw account_ids per group (kept alongside the resolved map): the
        // PUT /groups/{id}/accounts endpoint has wipe+re-insert semantics,
        // so membership writes must be built from the FULL id list — not
        // just the publishable accounts resolved above.
        const groupAccountIDs = new Map(
          (groupsData.groups ?? []).map((group) => [
            group.id,
            group.account_ids ?? [],
          ] as const),
        );
        const accountsByGroup = new Map(
          (groupsData.groups ?? []).map((group) => [
            group.id,
            (group.account_ids ?? [])
              .map((accountId) => accountIndex.get(accountId))
              .filter((account): account is PlatformAccount => account != null),
          ] as const),
        );
        const groups = (groupsData.groups ?? []).map(({ account_ids: _accountIDs, ...group }) => group);
        setState({
          kind: "ready",
          groups,
          accounts: activeAccounts,
          workspaceId,
          accountsByGroup,
          groupAccountIDs,
        });
        const requestedGroupId = Number(routeGroupId);
        const storedGroupId = typeof window === "undefined" ? NaN : Number(window.localStorage.getItem(LAST_GROUP_KEY));
        const nextGroupId = Number.isFinite(requestedGroupId) && groups.some((g) => g.id === requestedGroupId)
          ? requestedGroupId
          : groups.find((g) => g.id === storedGroupId)?.id ?? groups[0]?.id ?? null;
        setSelectedGroupIdState(nextGroupId);
        if (nextGroupId != null) window.localStorage.setItem(LAST_GROUP_KEY, String(nextGroupId));
      } catch (err) {
        if (controller.signal.aborted) return;
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        // Silent reloads (background reconcile after an assignment) must not
        // blow away the ready UI on a transient failure — only full loads
        // transition to the error state.
        if (!silent) {
          const message = err instanceof ApiError ? err.message : "Unable to load groups.";
          setState({ kind: "error", message });
        }
      }
  }, [navigate, routeGroupId]);

    const setSelectedGroupId = useCallback((id: number | null) => {
      setSelectedGroupIdState(id);
      if (typeof window !== "undefined") {
        if (id == null) window.localStorage.removeItem(LAST_GROUP_KEY);
        else window.localStorage.setItem(LAST_GROUP_KEY, String(id));
      }
    }, []);
  
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
  
    // Publishable accounts that are not members of ANY group. Derived from
    // the raw membership ids so a non-publishable member still counts as
    // "grouped" (and keeps its group out of the ungrouped pool).
    const ungroupedAccounts = useMemo(() => {
      if (state.kind !== "ready") return [];
      const grouped = new Set<number>();
      for (const ids of state.groupAccountIDs.values()) {
        for (const id of ids) grouped.add(id);
      }
      return state.accounts.filter((account) => !grouped.has(account.id));
    }, [state]);

    // Assigns a channel to a group. Optimistically moves the account into
    // the group's resolved map so the UI updates instantly, then persists
    // via PUT (wipe+re-insert with the FULL raw id list) and finally
    // reconciles against the server with a silent reload.
    const assignAccountToGroup = useCallback(async (accountId: number, groupId: number) => {
      if (state.kind !== "ready") return;
      const account = state.accounts.find((a) => a.id === accountId);
      if (!account) return;
      setBusyAccountId(accountId);
      // Compute the merged raw id list inside the functional update so the
      // optimistic UI and the PUT body always agree — even for rapid
      // successive assignments to the same group. The endpoint has
      // wipe+re-insert semantics, so a stale body would silently drop a
      // just-added member.
      let nextIDs: number[] | null = null;
      setState((prev) => {
        if (prev.kind !== "ready") return prev;
        const current = prev.groupAccountIDs.get(groupId) ?? [];
        if (current.includes(accountId)) return prev;
        nextIDs = [...current, accountId];
        const accountsByGroup = new Map(prev.accountsByGroup);
        accountsByGroup.set(groupId, [...(accountsByGroup.get(groupId) ?? []), account]);
        const groupAccountIDs = new Map(prev.groupAccountIDs);
        groupAccountIDs.set(groupId, nextIDs);
        return { ...prev, accountsByGroup, groupAccountIDs };
      });
      if (!nextIDs) {
        setBusyAccountId(null);
        return;
      }
      let persisted = false;
      try {
        const response = await authedFetch(`/api/v1/groups/${groupId}/accounts`, {
          method: "PUT",
          body: JSON.stringify({ account_ids: nextIDs }),
        });
        persisted = response.ok;
      } finally {
        // Successful write → silent reconcile; failure → non-silent reload
        // to revert the optimistic move and surface the error state.
        await load(!persisted);
        setBusyAccountId(null);
      }
    }, [state, load]);

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
    busyAccountId,
    load,
    handleCreateGroup,
    assignAccountToGroup,
    ungroupedAccounts,
    tree,
    selectedGroup,
    selectedAccount,
  };
}
