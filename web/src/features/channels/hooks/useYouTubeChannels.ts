/**
 * useYouTubeChannels — loads the wizard Step 2 channel list.
 *
 * Calls `GET /api/v1/accounts` + `GET /api/v1/workspaces` in
 * parallel (shared AbortSignal). The accounts response is filtered
 * client-side to YouTube channels only because the endpoint returns
 * accounts for multiple platforms.
 *
 * State machine (mirrors `useUploads`' LoadState union so callers
 * share the same mental model):
 *
 *   loading → ready { channels, workspaces, defaultChannelId, defaultWorkspaceId }
 *          ↘ error { message }
 *
 * Auto-defaults: when the response has exactly one channel OR one
 * workspace, `defaultChannelId` / `defaultWorkspaceId` is set so
 * the wizard UI can pre-select without a second round-trip.
 *
 *   - AuthError is RE-THROWN so the caller can navigate to /login
 *   - ApiError surfaces the server's typed message in kind='error'
 *
 * AbortController lifecycle:
 *   - A fresh controller on mount AND on refetch()
 *   - Unmount handler aborts to prevent zombie setState
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { listYouTubeChannelsAndWorkspaces } from "../api/channelsApi";
import type { PlatformAccount, Workspace } from "../api/channelsApi";

export type ChannelsLoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      channels: PlatformAccount[];
      workspaces: Workspace[];
      /** Set only when exactly one channel is available. */
      defaultChannelId: number | null;
      /** Set only when exactly one workspace is available. */
      defaultWorkspaceId: number | null;
    }
  | { kind: "error"; message: string };

export interface UseYouTubeChannelsResult {
  state: ChannelsLoadState;
  /** Aborts the in-flight load and starts a fresh one. */
  refetch: () => Promise<void>;
}

function deriveDefault<T extends { id: number }>(
  list: T[],
): number | null {
  return list.length === 1 ? list[0]!.id : null;
}

export function useYouTubeChannels(): UseYouTubeChannelsResult {
  const [state, setState] = useState<ChannelsLoadState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const runFetch = useCallback(async (signal: AbortSignal) => {
    try {
      const { channels, workspaces } =
        await listYouTubeChannelsAndWorkspaces(signal);
      if (signal.aborted) return;
      setState({
        kind: "ready",
        channels,
        workspaces,
        defaultChannelId: deriveDefault(channels),
        defaultWorkspaceId: deriveDefault(workspaces),
      });
    } catch (err) {
      if (signal.aborted) return;
      if (err instanceof AuthError) {
        // Re-thrown so the caller's router-level ProtectedRoute
        // redirects to /login. Same contract as useCreatePost and
        // useUploadMedia.
        throw err;
      }
      setState({
        kind: "error",
        message:
          err instanceof ApiError
            ? err.message
            : "Unable to load YouTube channels.",
      });
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    void runFetch(ctrl.signal).catch(() => {});
    return () => {
      ctrl.abort();
    };
  }, [runFetch]);

  const refetch = useCallback(async (): Promise<void> => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState({ kind: "loading" });
    await runFetch(ctrl.signal);
  }, [runFetch]);

  return { state, refetch };
}
