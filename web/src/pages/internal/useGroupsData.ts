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
    const assignmentQueueRef = useRef(Promise.resolve());
    const stateRef = useRef<FetchState>({ kind: "loading" });
    const [state, setState] = useState<FetchState>({ kind: "loading" });
    stateRef.current = state;
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
    const load = useCallback(async (silent = false, forceAccounts = false) => {
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
          listAllAccounts({ signal: controller.signal, force: forceAccounts }),
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
  
    // The Groups page is specifically used to organize YouTube publishing
    // destinations. Keep every active YouTube channel in the source tray,
    // including channels already assigned to a group: a channel may belong to
    // more than one group and users need to be able to add it to another one
    // without first removing it from its current group.
    const availableYouTubeAccounts = useMemo(() => {
      if (state.kind !== "ready") return [];
      return state.accounts.filter((account) => account.platform === "youtube");
    }, [state]);

    // Membership writes use replace-all semantics, so every operation is
    // serialized and always sends the complete raw ID list. This prevents a
    // fast second action from erasing the first one while keeping hidden,
    // non-publishable memberships intact.
    const updateGroupMembership = useCallback((
      groupId: number,
      accountIDsOrUpdater: number[] | ((currentIDs: number[]) => number[]),
      busyAccountId?: number,
    ) => {
      const operation = assignmentQueueRef.current.then(async () => {
        abortRef.current?.abort();
        const currentState = stateRef.current;
        if (currentState.kind !== "ready") return;
        if (busyAccountId != null && !currentState.accounts.some((account) => account.id === busyAccountId)) return;

        const currentIDs = currentState.groupAccountIDs.get(groupId) ?? [];
        const requestedIDs = typeof accountIDsOrUpdater === "function"
          ? accountIDsOrUpdater(currentIDs)
          : accountIDsOrUpdater;
        const nextIDs = Array.from(new Set(requestedIDs.filter((id) => Number.isInteger(id) && id > 0)));
        if (busyAccountId != null && currentIDs.includes(busyAccountId) && nextIDs.length === currentIDs.length && nextIDs.every((id, index) => id === currentIDs[index])) return;
        if (busyAccountId != null) setBusyAccountId(busyAccountId);

        const accountIndex = new Map(currentState.accounts.map((account) => [account.id, account]));
        const resolvedAccounts = nextIDs
          .map((accountID) => accountIndex.get(accountID))
          .filter((account): account is PlatformAccount => account != null);
        setState((prev) => {
          if (prev.kind !== "ready") return prev;
          const accountsByGroup = new Map(prev.accountsByGroup);
          accountsByGroup.set(groupId, resolvedAccounts);
          const groupAccountIDs = new Map(prev.groupAccountIDs);
          groupAccountIDs.set(groupId, nextIDs);
          return { ...prev, accountsByGroup, groupAccountIDs };
        });

        let persisted = false;
        try {
          const response = await authedFetch(`/api/v1/groups/${groupId}/accounts`, {
            method: "PUT",
            body: JSON.stringify({ account_ids: nextIDs }),
          });
          persisted = response.ok;
        } finally {
          // Successful write → silent reconcile (stay in the group, no
          // loading flash); failure → full reload to revert the optimistic
          // membership and surface the error state.
          await load(persisted);
          if (busyAccountId != null) setBusyAccountId(null);
        }
      });
      assignmentQueueRef.current = operation.catch(() => undefined);
      return operation;
    }, [load]);

    const assignAccountToGroup = useCallback((accountId: number, groupId: number) => (
      updateGroupMembership(groupId, (currentIDs) => (
        currentIDs.includes(accountId) ? currentIDs : [...currentIDs, accountId]
      ), accountId)
    ), [updateGroupMembership]);

    const setGroupAccounts = useCallback((
      groupId: number,
      accountIDsOrUpdater: number[] | ((currentIDs: number[]) => number[]),
    ) => updateGroupMembership(groupId, accountIDsOrUpdater), [updateGroupMembership]);

    const renameGroup = useCallback(async (groupId: number, name: string) => {
      const trimmedName = name.trim();
      if (!trimmedName) throw new Error("Il nome del gruppo è obbligatorio.");
      if (trimmedName.length > 80) throw new Error("Il nome del gruppo può contenere al massimo 80 caratteri.");
      await authedFetch(`/api/v1/groups/${groupId}`, {
        method: "PATCH",
        body: JSON.stringify({ name: trimmedName }),
      });
      // Silent reload: the inline rename editor closes on success and the
      // panel must stay mounted in the current group (no loading flash).
      await load(true);
    }, [load]);

    const handleCreateGroup = useCallback(async () => {
      if (!newGroupName.trim() || state.kind !== "ready") return;
      setCreatingGroup(true);
      try {
        const body = {
          workspace_id: state.workspaceId,
          parent_group_id: null,
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
    setGroupAccounts,
    renameGroup,
    availableYouTubeAccounts,
    tree,
    selectedGroup,
    selectedAccount,
  };
}
