import { useCallback } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import { useSharedQuery } from "../../../lib/queryRegistry";
import { getPostTargets } from "../api/postTargetsApi";
import type { PostStatus, PostTarget } from "../api/types";

const TERMINAL_STATUSES: ReadonlySet<PostStatus> = new Set<PostStatus>([
  "draft",
  "published",
  "failed",
  "partially_published",
  "dlq",
]);

export type PostTargetPollingStatus =
  | "idle"
  | "loading"
  | "polling"
  | "terminal"
  | "error";

export interface UsePostTargetStatusResult {
  targets: PostTarget[];
  status: PostTargetPollingStatus;
  error: string | null;
  refetch: () => Promise<void>;
}

function classifyTargets(targets: PostTarget[]): PostTargetPollingStatus {
  if (targets.length === 0) return "polling";
  return targets.every((target) => TERMINAL_STATUSES.has(target.status))
    ? "terminal"
    : "polling";
}

function deriveErrorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof Error) return error.message;
  return "Unable to fetch target status.";
}

async function fetchPostTargets(postId: number, signal: AbortSignal): Promise<PostTarget[]> {
  return getPostTargets(postId, signal);
}

/**
 * Shared post-target query. Multiple status surfaces for the same post
 * now share one cache, one in-flight request, and one adaptive timer.
 * Active targets poll every 3s; terminal target sets stop polling.
 */
export function usePostTargetStatus(postId: number | null): UsePostTargetStatusResult {
  const query = useSharedQuery<PostTarget[]>(
    `post-target-status:${postId ?? "none"}`,
    {
      enabled: postId != null,
      staleTime: 1_000,
      pollingInterval: (targets) =>
        targets && targets.length > 0 && targets.every((target) => TERMINAL_STATUSES.has(target.status))
          ? null
          : 3_000,
      fetcher: (signal) => fetchPostTargets(postId as number, signal),
    },
  );

  const targets = query.data ?? [];
  const authError = query.error instanceof AuthError;
  const status = postId == null
    ? "idle"
    : authError
      ? "polling"
      : query.error
        ? "error"
        : query.isLoading && query.data === undefined
          ? "loading"
          : classifyTargets(targets);
  const error = query.error && !authError ? deriveErrorMessage(query.error) : null;

  const refetch = useCallback(async (): Promise<void> => {
    try {
      await query.refetch();
    } catch (error) {
      // AuthError remains observable to callers exactly as before; the
      // shared registry retains non-auth errors in its snapshot.
      if (error instanceof AuthError) throw error;
    }
  }, [query.refetch]);

  return { targets, status, error, refetch };
}
