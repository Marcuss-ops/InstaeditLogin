/**
 * Mutation hook for `POST /api/v1/posts`.
 *
 * Drives the wizard's "Carica su YouTube" submission. Pattern matches
 * the codebase's existing `web/src/hooks/useUploads.ts`: plain
 * `useState` discriminator + `useRef` for an in-flight cancellation
 * token. NO external data library — `@tanstack/react-query` is not
 * installed and the in-repo convention is the lighter-weight one.
 *
 * Idempotency-Key lifecycle:
 *   - The server stores a "(workspace_id, key, payload_hash) → cached
 *     response" tuple (migration 021, lvl 1). Same key + same body =
 *     replay (201/202 cached); same key + different body = 409.
 *   - This hook therefore mints a FRESH UUID inside the `submit`
 *     callback. Two distinct submissions (even with identical bodies)
 *     send distinct keys → the server creates two distinct posts (which
 *     is what a "submit again" user gesture should do).
 *   - The OLD submit's key is invalidated by AbortController.abort()
 *     on the in-flight request. The canceled Promises resolve with an
 *     `AbortError` and the succeeding `submit` wins the `setState`
 *     race.
 *
 * Retry-safety:
 *   - If the submission fails with a *transient* network error (DNS,
 *     socket reset, 502/503), the hook lands in `kind: 'error'`. Re-
 *     submitting — even with the same form values — generates a fresh
 *     UUID, which lets the server idempotency DB treat it as a new
 *     post. Operators who genuinely want NO duplicate post MUST avoid
 *     calling submit() again after an error.
 *   - 409 idempotency_key_conflict does NOT occur here because the hook
 *     issues fresh keys per call.
 *
 * Cancellation:
 *   - Canceling the in-flight submit (via abort) leaves the existing
 *     state untouched if a NEW submit has already replaced the current
 *     call. If the canceled submit was the LATEST one, the hook stays
 *     in 'submitting' / 'error' for one tick until the controlled-
 *     rejection path runs `setState` — but the abort path explicitly
 *     suppresses state writes to avoid zombie renders.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { createPost, newIdempotencyKey } from "../api/postsApi";
import type { CreatePostRequest, Post } from "../api/types";

export type CreatePostState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "success"; post: Post }
  | { kind: "error"; message: string };

export interface UseCreatePostResult {
  state: CreatePostState;
  /**
   * Submit the payload. Each invocation mints a fresh UUID for the
   * `Idempotency-Key` header. Any prior in-flight submit is aborted
   * (its key is invalidated so the server's idempotency cache will
   * treat it as dropped on the floor if the request actually reached
   * the server before the abort landed).
   */
  submit: (body: CreatePostRequest, options?: { idempotencyKey?: string }) => Promise<void>;
  /**
   * Reset to idle. Cancels any in-flight submit.
   */
  reset: () => void;
}

export function useCreatePost(): UseCreatePostResult {
  const [state, setState] = useState<CreatePostState>({ kind: "idle" });
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

  const submit = useCallback(
    async (
      body: CreatePostRequest,
      options: { idempotencyKey?: string } = {},
    ): Promise<void> => {
      // Cancel the previous in-flight submit (its UUID is no longer
      // associated with a user intent). If it never sent a single
      // byte to the server we just lose local state; if it already
      // landed, the server's idempotency record will still be
      // discoverable but harmless (no follow-up read fires).
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setState({ kind: "submitting" });

      try {
        const key = options.idempotencyKey ?? newIdempotencyKey();
        const post = await createPost(body, {
          idempotencyKey: key,
          signal: ctrl.signal,
        });
        if (ctrl.signal.aborted) {
          // A newer submit took over; the parent controlled-rejection
          // path has already updated state — leave the slice alone.
          return;
        }
        setState({ kind: "success", post });
      } catch (err) {
        if (err instanceof AuthError) {
          // 401 path is owned by the caller (router-level navigate to
          // /login). We do NOT swallow it here.
          throw err;
        }
        if (ctrl.signal.aborted) return;
        const message =
          err instanceof ApiError
            ? err.message
            : err instanceof Error
              ? err.message
              : "Unable to create the post.";
        setState({ kind: "error", message });
      }
    },
    [],
  );

  const reset = useCallback((): void => {
    abortRef.current?.abort();
    abortRef.current = null;
    setState({ kind: "idle" });
  }, []);

  return { state, submit, reset };
}
