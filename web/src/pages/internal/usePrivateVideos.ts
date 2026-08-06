import { useCallback, useEffect, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { listAllAccounts } from "../../features/channels/api/channelsApi";
import { useToast } from "../../components/toast";
import { createEditorSessionAndOpen } from "../../features/youtube/api/editorSessionsApi";
import type { ContentItem, ContentPage, VideoState } from "./calendarTypes";

export function usePrivateVideos(accountId: string | null, enabled: boolean) {
  const toast = useToast();
  const [videoState, setVideoState] = useState<VideoState>({ kind: "idle" });

  const loadVideos = useCallback(
    async (cursor?: string) => {
      let accountIDs: number[] = accountId ? [Number(accountId)] : [];
      if (!accountId) {
        const accounts = await listAllAccounts();
        accountIDs = accounts
          .filter((account) => account.platform === "youtube")
          .map((account) => account.id);
      }
      if (accountIDs.length === 0) {
        setVideoState({ kind: "ready", items: [], nextCursor: undefined, isLoadingMore: false });
        return;
      }
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
        const pages = await Promise.all(accountIDs.map(async (id) => {
          const url = `/api/v1/accounts/${id}/content?limit=20${accountId && cursor ? `&cursor=${cursor}` : ""}&privacy=private`;
          const response = await authedFetch(url);
          const data = (await response.json()) as ContentPage;
          return { id, data };
        }));
        const items = pages
          .flatMap(({ id, data }) => data.items.map((item) => ({ ...item, account_id: id })))
          .sort((a, b) => String(b.published_at ?? "").localeCompare(String(a.published_at ?? "")));
        const nextCursor = accountId ? pages[0]?.data.next_cursor : undefined;
        setVideoState((prev) => ({
          kind: "ready",
          items:
            isAppend && prev.kind === "ready"
              ? [...prev.items, ...items]
              : items,
          nextCursor,
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
      const targetAccountId = item.account_id ?? (accountId ? Number(accountId) : null);
      if (!targetAccountId) return;
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
          platform_account_id: targetAccountId,
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
