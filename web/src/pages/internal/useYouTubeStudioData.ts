import { useCallback, useEffect, useRef, useState } from "react";
import { AuthError, authedFetch } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { listYouTubeEditorSessions } from "../../features/youtube/api/editorSessionsApi";
import { isPublishableAccount } from "../../types/uploads";
import type { LoadState } from "./youtubeStudioTypes";
import type { EditorSession, PlatformAccount, Workspace } from "../../types/uploads";

/**
 * useYouTubeStudioData owns the YouTube Studio read path: the initial
 * workspace/channel/sessions load, the filter-change re-fetch and the
 * manual refresh. It also exposes patchSession so the publish-verification
 * flow (useYouTubeStudioActions) can update a single session row in the
 * ready state without reaching into the load state directly.
 */
export function useYouTubeStudioData() {
  const toast = useToast();
  const abortRef = useRef<AbortController | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<number | "">(
    "",
  );
  const [selectedChannelId, setSelectedChannelId] = useState<number | "">("");
  const [refreshing, setRefreshing] = useState(false);

  const fetchSessions = useCallback(
    async (
      workspaceId: number | "",
      accountId: number | "",
      signal?: AbortSignal,
    ): Promise<EditorSession[]> => {
      // The list helper composes the workspace_id + account_id query
      // string into /api/v1/youtube/editor-sessions. We narrow
      // `number | ""` to `number | undefined` for the helper's
      // optional-key shape (it does the `if (x !== undefined)` check
      // internally so we don't need to pre-filter empty strings).
      return listYouTubeEditorSessions({
        workspace_id: workspaceId === "" ? undefined : workspaceId,
        account_id: accountId === "" ? undefined : accountId,
        include_terminal: true,
        signal,
      });
    },
    [],
  );

  const load = useCallback(async () => {
    setLoadState({ kind: "loading" });
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      const [wsR, acctsR] = await Promise.all([
        authedFetch("/api/v1/workspaces", { signal: ctrl.signal }),
        authedFetch("/api/v1/accounts", { signal: ctrl.signal }),
      ]);
      if (ctrl.signal.aborted) return;

      const ws =
        ((await wsR.json()) as { workspaces: Workspace[] }).workspaces ?? [];
      const accts =
        ((await acctsR.json()) as { accounts: PlatformAccount[] }).accounts ??
        [];
      const youtubeChannels = accts.filter(
        (a) => a.platform === "youtube" && isPublishableAccount(a),
      );

      const resolvedWorkspaceId =
        ws.length === 1 ? ws[0].id : "";
      const resolvedChannelId =
        youtubeChannels.length === 1 ? youtubeChannels[0].id : "";

      // First list fetch uses the auto-resolved single-workspace/single-channel
      // (if any). If both are empty the user must pick before we can list;
      // we still set ready so the filters render.
      const sessions = resolvedWorkspaceId
        ? await fetchSessions(resolvedWorkspaceId, resolvedChannelId, ctrl.signal)
        : [];

      if (ctrl.signal.aborted) return;
      setLoadState({
        kind: "ready",
        workspaces: ws,
        youtubeChannels,
        sessions,
      });
      setSelectedWorkspaceId(resolvedWorkspaceId);
      setSelectedChannelId(resolvedChannelId);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      if (err instanceof AuthError) {
        toast.error("Session expired — please sign in again.");
        return;
      }
      setLoadState({
        kind: "error",
        message:
          err instanceof Error
            ? err.message
            : "Unable to load YouTube editor sessions.",
      });
    }
  }, [fetchSessions, toast]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  // Re-fetch sessions whenever the workspace or channel filter changes.
  // The list endpoint filters server-side, so we don't need client-side
  // filtering on the response payload. Skip during the initial `loading`
  // fetch (load() owns that) and abort in-flight requests to avoid races.
  useEffect(() => {
    if (loadState.kind !== "ready") return;
    if (selectedWorkspaceId === "") {
      setLoadState((prev) =>
        prev.kind === "ready" ? { ...prev, sessions: [] } : prev,
      );
      return;
    }
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    void (async () => {
      try {
        const sessions = await fetchSessions(
          selectedWorkspaceId,
          selectedChannelId,
          ctrl.signal,
        );
        if (ctrl.signal.aborted) return;
        setLoadState((prev) =>
          prev.kind === "ready" ? { ...prev, sessions } : prev,
        );
      } catch {
        // Non-fatal: keep the previous list visible.
      }
    })();
  }, [fetchSessions, loadState.kind, selectedChannelId, selectedWorkspaceId]);

  const handleRefresh = useCallback(async () => {
    if (refreshing || selectedWorkspaceId === "") return;
    setRefreshing(true);
    try {
      const sessions = await fetchSessions(
        selectedWorkspaceId,
        selectedChannelId,
      );
      setLoadState((prev) =>
        prev.kind === "ready" ? { ...prev, sessions } : prev,
      );
    } catch {
      // toast surfaced by authedFetch
    } finally {
      setRefreshing(false);
    }
  }, [fetchSessions, refreshing, selectedChannelId, selectedWorkspaceId]);

  const patchSession = useCallback(
    (sessionId: string, patch: Partial<EditorSession>) => {
      setLoadState((prev) => {
        if (prev.kind !== "ready") return prev;
        return {
          ...prev,
          sessions: prev.sessions.map((session) =>
            session.id === sessionId
              ? { ...session, ...patch }
              : session,
          ),
        };
      });
    },
    [],
  );

  return {
    loadState,
    selectedWorkspaceId,
    setSelectedWorkspaceId,
    selectedChannelId,
    setSelectedChannelId,
    refreshing,
    load,
    handleRefresh,
    patchSession,
  };
}
