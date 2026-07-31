import { useCallback, useEffect, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { createEditorSessionAndOpen } from "../../features/youtube/api/editorSessionsApi";
import type { ContentItem, ContentPage, VideoState } from "./calendarTypes";

export function usePrivateVideos(accountId: string | null, enabled: boolean) {
  const toast = useToast();
  const [videoState, setVideoState] = useState<VideoState>({ kind: "idle" });

  const loadVideos = useCallback(
    async (cursor?: string) => {
      if (!accountId) return;
      const isAppend = !!cursor;
      if (isAppend) {
        setVideoState((prev) =>
          prev.kind === "ready"
            ? { ...prev, isLoadingMore: true, loadMoreError: undefined }
            : { kind: "loading" },
        );
      } else {
        setVideoState({ kind: "loading" });
      }
      try {
        const url = `/api/v1/accounts/${accountId}/content?limit=20${cursor ? `&cursor=${cursor}` : ""}&privacy=private`;
        const response = await authedFetch(url);
        const data = (await response.json()) as ContentPage;
        setVideoState((prev) => ({
          kind: "ready",
          items:
            isAppend && prev.kind === "ready"
              ? [...prev.items, ...data.items]
              : data.items,
          nextCursor: data.next_cursor,
          isLoadingMore: false,
          loadMoreError: undefined,
        }));
      } catch (err) {
        const message = err instanceof Error ? err.message : "Unable to load videos.";
        setVideoState((prev) =>
          isAppend && prev.kind === "ready"
            ? { ...prev, isLoadingMore: false, loadMoreError: message }
            : { kind: "error", message },
        );
      }
    },
    [accountId],
  );

  useEffect(() => {
    if (accountId && enabled && videoState.kind === "idle") {
      void loadVideos();
    }
  }, [accountId, enabled, videoState, loadVideos]);

  const handleEditThumbnail = useCallback(
    async (item: ContentItem) => {
      if (!accountId) return;
      try {
        const wsResp = await authedFetch("/api/v1/workspaces");
        const { workspaces } = (await wsResp.json()) as {
          workspaces: { id: number }[];
        };
        if (!workspaces.length) {
          toast.error("No workspaces found. Create one first.");
          return;
        }
        await createEditorSessionAndOpen({
          workspace_id: workspaces[0].id,
          platform_account_id: Number(accountId),
          youtube_video_id: item.external_id,
        });
        toast.success("Editor session created — opening Velox…");
      } catch (err) {
        if (err instanceof AuthError) return;
      }
    },
    [accountId, toast],
  );

  return { videoState, loadVideos, handleEditThumbnail };
}
