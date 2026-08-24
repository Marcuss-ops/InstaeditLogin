import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, AuthError } from "../../lib/auth";
import { retryPostTarget } from "../../features/publishing/api/postTargetsApi";
import type { PostTarget } from "../../features/publishing/api/types";

/**
 * useContentPublishRetry — per-failed-target retry state + handler.
 *
 * Extracted from ContentPublish.tsx (pattern: YouTubeStudio →
 * useYouTubeStudioActions). Owns:
 *   - `retryingIds` — Set of target ids currently re-arming (spinner);
 *   - `retryErrorById` — map target.id → inline error surfaced in the row;
 *   - `handleRetry` — POST /api/v1/post-targets/{id}/retry, refetch on
 *     success, navigate to /login on AuthError, inline message on
 *     ApiError / generic failure.
 *
 * The `force` flag is passed automatically when status is
 * `waiting_provider` or `partially_published` (the server requires
 * `?force=true` for non-failed states per openapi.yaml).
 */
export function useContentPublishRetry(onRetried: () => Promise<void>) {
  const navigate = useNavigate();

  const [retryingIds, setRetryingIds] = useState<Set<number>>(() => new Set());
  const [retryErrorById, setRetryErrorById] = useState<Record<number, string>>({});

  const handleRetry = useCallback(
    async (target: PostTarget): Promise<void> => {
      setRetryingIds((prev) => new Set(prev).add(target.id));
      setRetryErrorById((prev) => {
        const next = { ...prev };
        delete next[target.id];
        return next;
      });
      try {
        await retryPostTarget(target.id, {
          force:
            target.status === "waiting_provider" ||
            target.status === "partially_published",
        });
        await onRetried();
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        setRetryErrorById((prev) => ({
          ...prev,
          [target.id]:
            err instanceof ApiError
              ? err.message
              : err instanceof Error
                ? err.message
                : "Riprova non riuscito.",
        }));
      } finally {
        setRetryingIds((prev) => {
          const next = new Set(prev);
          next.delete(target.id);
          return next;
        });
      }
    },
    [navigate, onRetried],
  );

  return { retryingIds, retryErrorById, handleRetry };
}
