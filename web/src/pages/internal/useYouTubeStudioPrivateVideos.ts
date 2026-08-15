import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch } from "../../lib/auth";
import {
  checkYouTubeCopyright,
  listYouTubeCopyrightAlerts,
  type YouTubeCopyrightCheck,
} from "../../features/youtube/api/copyrightApi";
import type { ContentItem, CopyrightByVideoId } from "./youtubeStudioTypes";

/**
 * useYouTubeStudioPrivateVideos fetches the private videos of the
 * currently selected YouTube channel. It aborts the in-flight request
 * whenever the channel changes and clears the list on switch, so the
 * grid never shows videos from a stale channel.
 */
export function useYouTubeStudioPrivateVideos(selectedChannelId: number | "", enabled = false) {
  const [privateVideos, setPrivateVideos] = useState<ContentItem[]>([]);
  const [loadingVideos, setLoadingVideos] = useState(false);
  const [copyrightByVideoId, setCopyrightByVideoId] = useState<CopyrightByVideoId>({});
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
        // Alerts are best-effort here: a missing/disabled alert store must
        // not hide the private-video list. Missing alerts intentionally map
        // to the UI's "Copyright: None" state.
        try {
          const alerts = await listYouTubeCopyrightAlerts(signal);
          if (!signal?.aborted) {
            setCopyrightByVideoId(
              Object.fromEntries(
                alerts.map((alert) => [
                  alert.youtube_video_id,
                  {
                    status: alert.status,
                    message: alert.message,
                    processingStatus: alert.processing_status,
                    rejectionReason: alert.rejection_reason,
                    failureReason: alert.failure_reason,
                    licensedContent: alert.licensed_content,
                    blockedRegions: alert.blocked_regions,
                    allowedRegions: alert.allowed_regions,
                  },
                ]),
              ),
            );
          }
        } catch {
          // Keep the list usable when the optional alert read side is absent.
        }
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
    setCopyrightByVideoId({});
    if (!enabled || selectedChannelId === "") return;
    const ctrl = new AbortController();
    privateVideosAbortRef.current = ctrl;
    void fetchPrivateVideos(selectedChannelId, ctrl.signal);
    return () => ctrl.abort();
  }, [enabled, fetchPrivateVideos, selectedChannelId]);

  const recordCopyrightCheck = useCallback(
    (videoId: string, result: YouTubeCopyrightCheck) => {
      setCopyrightByVideoId((current) => ({ ...current, [videoId]: result }));
    },
    [],
  );

  const checkVideoCopyright = useCallback(
    async (videoId: string): Promise<YouTubeCopyrightCheck | null> => {
      if (selectedChannelId === "") return null;
      const result = await checkYouTubeCopyright(selectedChannelId, videoId);
      recordCopyrightCheck(videoId, result);
      return result;
    },
    [recordCopyrightCheck, selectedChannelId],
  );

  return {
    privateVideos,
    loadingVideos,
    copyrightByVideoId,
    recordCopyrightCheck,
    checkVideoCopyright,
  };
}
