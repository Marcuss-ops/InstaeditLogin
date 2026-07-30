/**
 * Mutation hook for `POST /api/v1/youtube/editor-sessions`.
 *
 * Drives the "Modifica copertina" button across
 * AccountDetails / YouTubeStudio / Calendar channels. Pattern
 * matches `useCreatePost` (the prior session's most-similar hook):
 *
 *   - vanilla `useState` discriminator state machine,
 *     NO external data library,
 *   - `useRef<AbortController>` for in-flight cancellation,
 *   - `AuthError` is re-thrown so the caller page can navigate
 *     to `/login` (matches the convention used by every other
 *     mutation hook in the codebase),
 *   - `ApiError` and other errors land in `kind: "error"` with the
 *     raw `err.message` preserved so the page can show a typed
 *     message like "Velox session limit reached (429)".
 *
 * NOT owning `window.open(editor_url, ...)`:
 *   The hook returns the fully-resolved session response on success
 *   so the caller decides when (and whether) to open the popup.
 *   YouTubeStudio chains `void handleRefresh()` after the open;
 *   AccountDetails/Calendar just want the open with no follow-up.
 *   Centralizing the open decision in the hook would couple the
 *   hook to a side effect callers can't suppress, so we keep it
 *   explicit at the call site.
 *
 *   Helper: {@link import("../api/editorSessionsApi").createEditorSessionAndOpen}
 *   bundles both calls for the two callsites that don't have
 *   follow-up logic (AccountDetails, Calendar).
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import {
  createYouTubeEditorSession,
  type CreateYouTubeEditorSessionRequest,
  type CreateYouTubeEditorSessionResponse,
} from "../api/editorSessionsApi";

export type CreateYouTubeEditorSessionState =
  | { kind: "idle" }
  | { kind: "creating" }
  | { kind: "success"; session: CreateYouTubeEditorSessionResponse }
  | { kind: "error"; message: string };

export interface UseCreateYouTubeEditorSessionResult {
  state: CreateYouTubeEditorSessionState;
  /**
   * Fire the POST. Each invocation aborts any prior in-flight
   * submit (its request is canceled before it can mutate
   * server state) and mints no Idempotency-Key (this endpoint
   * isn't idempotent at the transport layer — see the api file
   * header). A successful navigation to the editor page should
   * reset() the hook once the user has confirmed they're done.
   */
  create: (
    input: CreateYouTubeEditorSessionRequest,
  ) => Promise<CreateYouTubeEditorSessionResponse | null>;
  /** Clears the hook back to idle. */
  reset: () => void;
}

export function useCreateYouTubeEditorSession(): UseCreateYouTubeEditorSessionResult {
  const [state, setState] = useState<CreateYouTubeEditorSessionState>({
    kind: "idle",
  });
  const abortRef = useRef<AbortController | null>(null);

  // Cancel on unmount: throwaway-state updates from a late-arriving
  // promise would otherwise warn in dev (`Can't perform a React state
  // update on an unmounted component`) and tax the GC during nav.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, []);

  const create = useCallback(
    async (
      input: CreateYouTubeEditorSessionRequest,
    ): Promise<CreateYouTubeEditorSessionResponse | null> => {
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setState({ kind: "creating" });

      try {
        const session = await createYouTubeEditorSession(input, {
          signal: ctrl.signal,
        });
        if (ctrl.signal.aborted) return null;
        setState({ kind: "success", session });
        return session;
      } catch (err) {
        if (err instanceof AuthError) {
          // 401 path is owned by the caller (router-level navigate
          // to /login). We re-throw to keep the existing convention
          // — caller pages already wrap their useCreatePost calls
          // the same way.
          throw err;
        }
        if (ctrl.signal.aborted) return null;
        const message =
          err instanceof ApiError
            ? err.message
            : err instanceof Error
              ? err.message
              : "Unable to create the editor session.";
        setState({ kind: "error", message });
        return null;
      }
    },
    [],
  );

  const reset = useCallback((): void => {
    abortRef.current?.abort();
    abortRef.current = null;
    setState({ kind: "idle" });
  }, []);

  return { state, create, reset };
}
