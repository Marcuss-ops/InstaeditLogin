import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { useNavigate } from "react-router-dom";
import { useToast } from "../../components/toast";
import { publishAccountThumbnail, uploadThumbnailFile } from "../../features/youtube/api/thumbnailApi";
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
  const [thumbnailVideoID, setThumbnailVideoID] = useState<string | null>(null);
  const privateVideosAbortRef = useRef<AbortController | null>(null);
  const navigate = useNavigate();
  const toast = useToast();

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

  const applyThumbnailFile = useCallback(async (video: ContentItem, file: File) => {
    if (selectedChannelId === "" || thumbnailVideoID) return;
    if (!window.confirm("Sostituire la copertina di questo video privato? L'immagine verrà pubblicata su YouTube.")) return;
    setThumbnailVideoID(video.external_id);
    try {
      const asset = await uploadThumbnailFile(file);
      await publishAccountThumbnail(selectedChannelId, video.external_id, asset.id);
      toast.success("Copertina del video privato salvata.");
      const ctrl = new AbortController();
      await fetchPrivateVideos(selectedChannelId, ctrl.signal);
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile salvare la copertina.");
    } finally {
      setThumbnailVideoID(null);
    }
  }, [fetchPrivateVideos, navigate, selectedChannelId, thumbnailVideoID, toast]);

  return {
    privateVideos,
    loadingVideos,
    copyrightByVideoId,
    recordCopyrightCheck,
    checkVideoCopyright,
    applyThumbnailFile,
    thumbnailVideoID,
  };
}
