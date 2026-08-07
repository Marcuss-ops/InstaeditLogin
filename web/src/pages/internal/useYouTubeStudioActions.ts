import { useCallback, useEffect, useRef, useState } from "react";
import { AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import {
  createYouTubeEditorSession,
  attachYouTubeEditorSessionThumbnail,
  publishYouTubeEditorSession,
  getYouTubeEditorSession,
  openInstaEditorInNewTab,
} from "../../features/youtube/api/editorSessionsApi";
import { isScheduleInPast, localToUTC } from "./youtubeStudioTime";
import type { ActionState } from "./youtubeStudioTypes";
import type { EditorSession, YouTubePublishResult } from "../../types/uploads";

export type PublishResultState = {
  sessionId: string;
  result: YouTubePublishResult;
  checking: boolean;
} | null;

/**
 * useYouTubeStudioActions owns every write-path interaction of the
 * YouTube Studio page: creating editor sessions, attaching thumbnails,
 * publishing now / scheduling, and the async publish-verification loop.
 * The read path (load state, filters, refresh) stays in
 * useYouTubeStudioData; this hook receives refresh + patchSession so the
 * action results can re-list and update individual session rows.
 */
export function useYouTubeStudioActions({
  selectedWorkspaceId,
  selectedChannelId,
  refresh,
  patchSession,
}: {
  selectedWorkspaceId: number | "";
  selectedChannelId: number | "";
  refresh: () => Promise<void>;
  patchSession: (sessionId: string, patch: Partial<EditorSession>) => void;
}) {
  const toast = useToast();
  const [manualVideoId, setManualVideoId] = useState("");
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [thumbnailMediaId, setThumbnailMediaId] = useState("");
  const [scheduleAt, setScheduleAt] = useState("");
  const [action, setAction] = useState<ActionState>({ kind: "idle" });
  const verificationRunRef = useRef(0);
  const [publishResult, setPublishResult] = useState<PublishResultState>(null);

  useEffect(() => {
    return () => {
      verificationRunRef.current += 1;
    };
  }, []);

  const handleCreateSession = useCallback(async () => {
    if (
      selectedWorkspaceId === "" ||
      selectedChannelId === "" ||
      !manualVideoId.trim()
    ) {
      return;
    }
    const videoId = manualVideoId.trim();
    setAction({ kind: "creating" });
    try {
      // Narrow before the call. `canCreate` already guards against
      // `selectedWorkspaceId === ""`, but tsc types the field as
      // `number | ""` (whole form) so we narrow explicitly instead
      // of casting via `as number` (which was a pre-existing latent
      // bug: JSON.stringify would have injected `""` if a stale
      // state ever slipped past canCreate).
      if (
        typeof selectedWorkspaceId !== "number" ||
        typeof selectedChannelId !== "number"
      ) {
        setAction({ kind: "idle" });
        return;
      }
      const session = await createYouTubeEditorSession({
        workspace_id: selectedWorkspaceId,
        platform_account_id: selectedChannelId,
        youtube_video_id: videoId,
      });
      toast.success("Editor session created — opening Velox…");
      setManualVideoId("");
      // Reset to idle immediately so the form re-enables for the next
      // submission. The opened tab is the user's confirmation; we don't
      // gate further form interaction on it.
      setAction({ kind: "idle" });
      openInstaEditorInNewTab(session.editor_url);
      void refresh();
    } catch (err) {
      if (err instanceof AuthError) return;
      setAction({ kind: "idle" });
      // authedFetch already toasts on non-OK responses; keep the form
      // mounted so the user can retry.
    }
  }, [
    manualVideoId,
    refresh,
    selectedChannelId,
    selectedWorkspaceId,
    toast,
  ]);

  const handleAttachThumbnail = useCallback(
    async (sessionId: string) => {
      const mediaId = thumbnailMediaId.trim();
      if (!mediaId) return;
      setAction({ kind: "attaching", sessionId });
      try {
        await attachYouTubeEditorSessionThumbnail(sessionId, {
          thumbnail_media_id: mediaId,
        });
        toast.success("Thumbnail attached.");
        setThumbnailMediaId("");
        setActiveSessionId(null);
        void refresh();
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [refresh, thumbnailMediaId, toast],
  );

  const verifyPublishedSession = useCallback(
    async (sessionId: string, initial: YouTubePublishResult) => {
      const run = ++verificationRunRef.current;
      setPublishResult({ sessionId, result: initial, checking: true });

      // The backend already performs a videos.list read-back. This short
      // follow-up is only for the rare `pending` case, when the reconciler
      // still needs to confirm YouTube's final privacy asynchronously.
      if (initial.youtube_sync_status !== "pending") {
        setPublishResult({ sessionId, result: initial, checking: false });
        return;
      }

      for (let attempt = 0; attempt < 12; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 5_000));
        if (verificationRunRef.current !== run) return;
        try {
          const detail = await getYouTubeEditorSession(sessionId);
          const next: YouTubePublishResult = {
            ...initial,
            status: detail.status,
            privacy_status: detail.desired_privacy as YouTubePublishResult["privacy_status"],
            actual_privacy: detail.actual_privacy ?? undefined,
            youtube_sync_status: detail.youtube_sync_status ?? undefined,
          };
          setPublishResult({
            sessionId,
            result: next,
            checking: next.youtube_sync_status === "pending",
          });
          patchSession(sessionId, {
            status: detail.status,
            desired_privacy: detail.desired_privacy,
            publish_at: detail.publish_at,
            actual_privacy: detail.actual_privacy,
            youtube_sync_status: detail.youtube_sync_status,
          });
          if (next.youtube_sync_status !== "pending") return;
        } catch {
          // Keep the successful publish card visible. The backend
          // reconciler remains the source of truth if this read fails.
        }
      }

      if (verificationRunRef.current === run) {
        setPublishResult((current) =>
          current?.sessionId === sessionId
            ? { ...current, checking: false }
            : current,
        );
      }
    },
    [patchSession],
  );

  const handlePublishNow = useCallback(
    async (sessionId: string) => {
      setAction({ kind: "publishing", sessionId });
      try {
        const result = await publishYouTubeEditorSession(sessionId, {
          privacy_status: "public",
        });
        toast.success("Video published — verifying YouTube status…");
        void refresh();
        void verifyPublishedSession(sessionId, result);
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [refresh, toast, verifyPublishedSession],
  );

  const handleSchedule = useCallback(
    async (sessionId: string) => {
      if (!scheduleAt) return;
      const publishAtDate = new Date(scheduleAt);
      if (isNaN(publishAtDate.getTime())) {
        toast.error("Pick a valid publish date.");
        return;
      }
      if (isScheduleInPast(scheduleAt)) {
        toast.error("La data di pubblicazione deve essere nel futuro.");
        return;
      }
      const utcISO = localToUTC(scheduleAt);
      setAction({ kind: "publishing", sessionId });
      try {
        const result = await publishYouTubeEditorSession(sessionId, {
          privacy_status: "private",
          publish_at: utcISO,
        });
        toast.success("Publication scheduled — verifying YouTube status…");
        setScheduleAt("");
        setActiveSessionId(null);
        void refresh();
        void verifyPublishedSession(sessionId, result);
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [refresh, scheduleAt, toast, verifyPublishedSession],
  );

  const canCreate =
    action.kind !== "creating" &&
    selectedWorkspaceId !== "" &&
    selectedChannelId !== "" &&
    manualVideoId.trim().length > 0;

  return {
    manualVideoId,
    setManualVideoId,
    activeSessionId,
    setActiveSessionId,
    thumbnailMediaId,
    setThumbnailMediaId,
    scheduleAt,
    setScheduleAt,
    action,
    publishResult,
    setPublishResult,
    handleCreateSession,
    handleAttachThumbnail,
    handlePublishNow,
    handleSchedule,
    canCreate,
  };
}
