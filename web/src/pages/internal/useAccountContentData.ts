import { useCallback, useState } from "react";
import { authedFetch } from "../../lib/auth";
import type { ContentItem } from "./AccountDetailsVideoCard";

export type AccountContentPage = {
  items: ContentItem[];
  next_cursor?: string;
};

export type AccountContentState =
  | { kind: "idle" }
  | { kind: "loading" }
  | {
      kind: "ready";
      items: ContentItem[];
      nextCursor?: string;
      isLoadingMore?: boolean;
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };

export function useAccountContentData(
  accountId: string | undefined,
  currentPlatform: string | undefined,
) {
  const [contentState, setContentState] = useState<AccountContentState>({
    kind: "idle",
  });
  const [contentCacheBust, setContentCacheBust] = useState(0);

  const loadContent = useCallback(
    async (cursor?: string) => {
      const isAppend = !!cursor;
      if (isAppend) {
        setContentState((prev) =>
          prev.kind === "ready"
            ? { ...prev, isLoadingMore: true, loadMoreError: undefined }
            : { kind: "loading" },
        );
      } else {
        setContentState({ kind: "loading" });
      }
      try {
        // YouTube content is filtered to private videos only so the user can
        // pick a video to edit the thumbnail before publishing.
        const privacy = currentPlatform === "youtube" ? "private" : "";
        const url = `/api/v1/accounts/${accountId}/content?limit=20${cursor ? `&cursor=${cursor}` : ""}${privacy ? `&privacy=${privacy}` : ""}`;
        const response = await authedFetch(url);
        const data = (await response.json()) as AccountContentPage;
        setContentState((prev) => ({
          kind: "ready",
          items:
            isAppend && prev.kind === "ready"
              ? [...prev.items, ...data.items]
              : data.items,
          nextCursor: data.next_cursor,
          isLoadingMore: false,
          loadMoreError: undefined,
        }));
        // Keep the bust linked to a successful content update so unrelated
        // renders do not invalidate thumbnail URLs.
        setContentCacheBust(Date.now());
      } catch (err) {
        const message = err instanceof Error ? err.message : "Unable to load content.";
        setContentState((prev) => {
          if (isAppend && prev.kind === "ready") {
            return { ...prev, isLoadingMore: false, loadMoreError: message };
          }
          return { kind: "error", message };
        });
      }
    },
    [accountId, currentPlatform],
  );

  return { contentState, loadContent, contentCacheBust };
}
