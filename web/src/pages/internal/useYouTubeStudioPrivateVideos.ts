import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch } from "../../lib/auth";
import type { ContentItem } from "./youtubeStudioTypes";

/**
 * useYouTubeStudioPrivateVideos fetches the private videos of the
 * currently selected YouTube channel. It aborts the in-flight request
 * whenever the channel changes and clears the list on switch, so the
 * grid never shows videos from a stale channel.
 */
export function useYouTubeStudioPrivateVideos(selectedChannelId: number | "", enabled = false) {
  const [privateVideos, setPrivateVideos] = useState<ContentItem[]>([]);
  const [loadingVideos, setLoadingVideos] = useState(false);
  const privateVideosAbortRef = useRef<AbortController | null>(null);

  const fetchPrivateVideos = useCallback(
    async (accountId: number, signal?: AbortSignal) => {
      setLoadingVideos(true);
      try {
        const resp = await authedFetch(
          `/api/v1/accounts/${accountId}/content?limit=50&privacy=private`,
          { signal },
        );
        const data = (await resp.json()) as { items: ContentItem[] };
        if (!signal?.aborted) setPrivateVideos(data.items ?? []);
      } catch {
        if (!signal?.aborted) setPrivateVideos([]);
      } finally {
        if (!signal?.aborted) setLoadingVideos(false);
      }
    },
    [],
  );

  useEffect(() => {
    privateVideosAbortRef.current?.abort();
    setPrivateVideos([]);
    if (!enabled || selectedChannelId === "") return;
    const ctrl = new AbortController();
    privateVideosAbortRef.current = ctrl;
    void fetchPrivateVideos(selectedChannelId, ctrl.signal);
    return () => ctrl.abort();
  }, [enabled, fetchPrivateVideos, selectedChannelId]);

  return { privateVideos, loadingVideos };
}
