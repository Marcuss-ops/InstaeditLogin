import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useSharedPolling } from "../../lib/queryRegistry";
import { ApiError, authedFetch, AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import {
  createYouTubeEditorSession,
  createEditorSessionAndOpen,
  openInstaEditorWithLaunch,
} from "../../features/youtube/api/editorSessionsApi";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";
import {
  DEFAULT_PAGE_SIZE,
  type GroupYouTubeVideo,
  type LoadState,
  type VideoPreview,
} from "./groupYouTubeVideosTypes";

interface GroupWorkspaceResponse {
  workspace_id?: number;
}

async function resolveGroupWorkspace(groupId: number): Promise<number> {
  const response = await authedFetch(`/api/v1/groups/${groupId}`);
  const data = (await response.json()) as GroupWorkspaceResponse;
  const workspaceID = data.workspace_id;
  if (!workspaceID) throw new Error("Il gruppo non ha un workspace valido.");
  return workspaceID;
}

export function useGroupYouTubeVideos(groupId: number, enabled = true) {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const pollingAttemptsRef = useRef(0);
  const openingVideoRef = useRef(false);
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [recencyDays, setRecencyDays] = useState<number>(90);
  const [openingVideoID, setOpeningVideoID] = useState<string | null>(null);
  const [preview, setPreview] = useState<VideoPreview | null>(null);
  const [draftTitle, setDraftTitle] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [savingMetadata, setSavingMetadata] = useState(false);
  const toast = useToast();

  // Resolves to true only when the editor was actually opened (or the
  // create-session request succeeded); callers such as the covers-hub
  // create dialog use it to decide whether to refresh the grid — the
  // hook never rejects, it surfaces failures via the toast.
  const openThumbnailEditor = useCallback(async (video: GroupYouTubeVideo): Promise<boolean> => {
    if (openingVideoRef.current) return false;
    openingVideoRef.current = true;
    setOpeningVideoID(video.youtube_video_id);
    try {
      // The group-video projection already carries the authoritative
      // InstaEditor project handle and URL when this video was opened
      // before. Reuse them directly: no workspace lookup and no duplicate
      // create request are needed for the common Groups → Modifica path.
      // The editor always opens in a NEW TAB — the SPA never navigates
      // away from the Copertine hub / Groups workspace.
      if (video.velox_project_id && video.editor_url) {
        await openInstaEditorWithLaunch(video.editor_url, video.velox_project_id);
        toast.success("InstaEditor aperto in una nuova scheda: il video resta privato finché non scegli di pubblicarlo.");
        return true;
      }

      // First open: resolve the group-owned workspace, then use the
      // idempotent editor-session endpoint. The server validates the
      // workspace/account/video binding and returns the stable
      // velox_project_id + project URL, which this helper mints a
      // launch token for and opens in a new tab (no SPA navigation).
      const workspaceID = await resolveGroupWorkspace(groupId);
      await createEditorSessionAndOpen({
        workspace_id: workspaceID,
        platform_account_id: video.platform_account_id,
        youtube_video_id: video.youtube_video_id,
        ...(safeAssetUrl(video.thumbnail_url) ? { source_thumbnail_url: safeAssetUrl(video.thumbnail_url) } : {}),
      });
      return true;
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return false;
      }
      toast.error(error instanceof Error ? error.message : "Impossibile aprire InstaEditor.");
      return false;
    } finally {
      openingVideoRef.current = false;
      setOpeningVideoID(null);
    }
  }, [groupId, navigate, toast]);

  const openVideoPreview = useCallback((video: GroupYouTubeVideo) => {
    // Preview is deliberately local and deterministic: no NVIDIA metadata
    // request is needed just to inspect the thumbnail.
    setPreview({ video });
    setDraftTitle(video.title ?? "");
    setDraftDescription(video.draft_description ?? video.description ?? "");
  }, []);

  const saveVideoMetadata = useCallback(async () => {
    if (!preview || savingMetadata) return;
    setSavingMetadata(true);
    try {
      let projectId = preview.video.velox_project_id;
      if (!projectId) {
        const workspaceID = await resolveGroupWorkspace(groupId);
        const session = await createYouTubeEditorSession({
          workspace_id: workspaceID,
          platform_account_id: preview.video.platform_account_id,
          youtube_video_id: preview.video.youtube_video_id,
          ...(safeAssetUrl(preview.video.thumbnail_url) ? { source_thumbnail_url: safeAssetUrl(preview.video.thumbnail_url) } : {}),
        });
        projectId = session.velox_project_id;
      }
      await authedFetch(`/api/v1/youtube/editor-sessions/by-project/${encodeURIComponent(projectId)}/draft`, {
        method: "PUT",
        body: JSON.stringify({
          title: draftTitle,
          description: draftDescription,
          tags: [],
          desired_privacy: preview.video.desired_privacy || "private",
          publish_at: preview.video.publish_at ?? null,
        }),
      });
      setPreview({
        video: {
          ...preview.video,
          title: draftTitle,
          description: draftDescription,
          draft_description: draftDescription,
          velox_project_id: projectId,
        },
      });
      toast.success("Titolo e descrizione salvati.");
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      toast.error(error instanceof Error ? error.message : "Impossibile salvare i metadati.");
    } finally {
      setSavingMetadata(false);
    }
  }, [draftDescription, draftTitle, groupId, navigate, preview, savingMetadata, toast]);

  const loadVideos = useCallback(
    async (signal: AbortSignal, offset = 0, append = false, forceRefresh = false): Promise<void> => {
      try {
        const params = new URLSearchParams({
          include_subgroups: "true",
          limit: String(DEFAULT_PAGE_SIZE),
          offset: String(offset),
          days: String(recencyDays),
        });
        if (forceRefresh) params.set("refresh", "true");
        const response = await authedFetch(
          `/api/v1/groups/${groupId}/youtube/videos?${params.toString()}`,
          { signal },
        );
        if (signal.aborted) return;
        const data = (await response.json()) as {
          videos?: GroupYouTubeVideo[];
          warnings?: string[];
          has_more?: boolean;
          next_offset?: number;
        };
        const videos = (data.videos ?? []).filter((video) => {
          const privacy = String(video.actual_privacy ?? video.privacy_status ?? "").toLowerCase();
          return privacy === "private" && video.phantom !== true;
        });
        setState((previous) => {
          const previousVideos = append && previous.kind === "ready" ? previous.videos : [];
          return {
            kind: "ready",
            videos: [...previousVideos, ...videos],
            warnings: data.warnings ?? (append && previous.kind === "ready" ? previous.warnings : []),
            hasMore: data.has_more === true,
            nextOffset: data.has_more === true && data.next_offset != null ? data.next_offset : null,
            isLoadingMore: false,
          };
        });
      } catch (error) {
        if (signal.aborted) return;
        if (error instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const upstream = error instanceof ApiError && error.status === 502;
        setState({
          kind: "error",
          upstream,
          message: upstream
            ? "YouTube non risponde temporaneamente. Riprova tra poco."
            : error instanceof Error
              ? error.message
              : "Impossibile caricare i video YouTube.",
        });
      }
    },
    [groupId, navigate, recencyDays],
  );

  const refreshVideos = useCallback(
    (resetPolling = true, forceRefresh = false, sharedSignal?: AbortSignal): Promise<void> => {
      if (resetPolling) pollingAttemptsRef.current = 0;
      abortRef.current?.abort();
      const controller = sharedSignal ? null : new AbortController();
      const signal = sharedSignal ?? controller!.signal;
      if (controller) abortRef.current = controller;
      if (resetPolling) {
        setState({ kind: "loading" });
      }
      return loadVideos(signal, 0, false, forceRefresh);
    },
    [loadVideos],
  );

  const loadMoreVideos = useCallback((): void => {
    if (state.kind !== "ready" || !state.hasMore || state.nextOffset == null || state.isLoadingMore) {
      return;
    }
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState((previous) =>
      previous.kind === "ready" ? { ...previous, isLoadingMore: true } : previous,
    );
    void loadVideos(controller.signal, state.nextOffset, true);
  }, [loadVideos, state]);

  useEffect(() => {
    if (!enabled) {
      abortRef.current?.abort();
      setState({ kind: "loading" });
      return () => abortRef.current?.abort();
    }
    refreshVideos(false);
    return () => abortRef.current?.abort();
  }, [enabled, refreshVideos]);

  const hasPendingVideos =
    state.kind === "ready" &&
    state.videos.some((video) => video.youtube_sync_status === "pending");

  const pollPendingVideos = useSharedPolling(`group-youtube-videos:${groupId}:${recencyDays}`, {
    enabled: hasPendingVideos && pollingAttemptsRef.current < 12,
    interval: 10_000,
    task: async (signal) => {
      if (pollingAttemptsRef.current >= 12) return;
      pollingAttemptsRef.current += 1;
      await refreshVideos(false, false, signal);
    },
  });

  useEffect(() => {
    if (hasPendingVideos) void pollPendingVideos();
  }, [hasPendingVideos, pollPendingVideos]);

  return {
    state,
    recencyDays,
    setRecencyDays,
    openingVideoID,
    preview,
    setPreview,
    draftTitle,
    setDraftTitle,
    draftDescription,
    setDraftDescription,
    savingMetadata,
    openThumbnailEditor,
    openVideoPreview,
    saveVideoMetadata,
    refreshVideos,
    loadMoreVideos,
  };
}
