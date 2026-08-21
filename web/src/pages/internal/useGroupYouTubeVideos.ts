import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useSharedPolling } from "../../lib/queryRegistry";
import { ApiError, authedFetch, AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import {
  createYouTubeEditorSession,
  createEditorSessionAndOpen,
  coversHubReturnTo,
  openInstaEditorWithLaunch,
} from "../../features/youtube/api/editorSessionsApi";
import { patchGroupVideoMetadata } from "../../features/youtube/api/videosApi";
import { useGroupVideosInvalidation } from "../../features/youtube/hooks/useGroupVideosInvalidation";
import { publishGroupThumbnail, uploadThumbnailFile } from "../../features/youtube/api/thumbnailApi";

// Same naming style as the InstaEditor's own generateRandomName, so a
// freshly created cover reads as a proper project name (not the video's
// E2E title) in both the editor and the Copertine hub card.
const COVER_NAME_ADJECTIVES = ["Vibrant", "Neon", "Cosmic", "Electric", "Stealth", "Hyper", "Sonic", "Golden", "Pixel", "Astro"];
const COVER_NAME_NOUNS = ["Nebula", "Blade", "Vortex", "Spark", "Zenith", "Echo", "Pulse", "Wave", "Grid", "Forge"];

// Turns a group display name into a name-safe segment for the random
// cover title: accents stripped, non-alphanumerics folded to hyphens,
// original casing preserved (e.g. "Wrestling Insider RU" →
// "Wrestling-Insider-RU").
export function slugifyGroupName(name: string): string {
  const slug = name
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
  return slug;
}

export function generateCoverName(groupName?: string): string {
  const noun = COVER_NAME_NOUNS[Math.floor(Math.random() * COVER_NAME_NOUNS.length)];
  const number = Math.floor(Math.random() * 99) + 1;
  if (groupName) {
    const slug = slugifyGroupName(groupName);
    if (slug) return `${slug}-${noun}-${number}`;
  }
  const adjective = COVER_NAME_ADJECTIVES[Math.floor(Math.random() * COVER_NAME_ADJECTIVES.length)];
  return `${adjective}-${noun}-${number}`;
}
import { safeAssetUrl, videoAvailability } from "./groupYouTubeVideosVisual";
import {
  DEFAULT_PAGE_SIZE,
  isYouTubePrivacyStatus,
  type GroupYouTubeVideo,
  type LoadState,
  type VideoPreview,
  type YouTubePrivacyStatus,
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

// Shared editor-session payload for a group video: the binding the
// idempotent create-or-resolve endpoint validates (workspace/account/video)
// plus the thumbnail we hand the editor as its starting canvas.
function buildSessionPayload(video: GroupYouTubeVideo, workspaceID: number) {
  return {
    workspace_id: workspaceID,
    platform_account_id: video.platform_account_id,
    youtube_video_id: video.youtube_video_id,
    ...(safeAssetUrl(video.thumbnail_url)
      ? { source_thumbnail_url: safeAssetUrl(video.thumbnail_url) }
      : {}),
  };
}

export function useGroupYouTubeVideos(groupId: number, enabled = true, groupName?: string) {
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
  const [editCategoryID, setEditCategoryID] = useState("");
  const [editPrivacyStatus, setEditPrivacyStatus] = useState<YouTubePrivacyStatus>("private");
  const [savingMetadata, setSavingMetadata] = useState(false);
  const [thumbnailVideoID, setThumbnailVideoID] = useState<string | null>(null);
  const toast = useToast();

  // Resolves to true only when the editor was actually opened (or the
  // create-session request succeeded); callers such as the covers-hub
  // quick-create use it to decide whether to refresh the grid — the
  // hook never rejects, it surfaces failures via the toast.
  const openThumbnailEditor = useCallback(async (video: GroupYouTubeVideo, opts?: { draftTitle?: string; tab?: Window | null }): Promise<boolean> => {
    if (openingVideoRef.current) return false;
    openingVideoRef.current = true;
    setOpeningVideoID(video.youtube_video_id);
    try {
      // The group-video projection already carries the authoritative
      // InstaEditor project handle and URL when this video was opened
      // before. Reuse them directly: no workspace lookup and no duplicate
      // create request are needed for the common Groups → Modifica path.
      // The editor always opens in a NEW TAB — the SPA never navigates
      // away from the Copertine hub / Groups workspace. The launch URL
      // carries a relative return_to so the editor Home pill lands back
      // on this group's Copertine hub.
      if (video.velox_project_id && video.editor_url) {
        await openInstaEditorWithLaunch(video.editor_url, video.velox_project_id, {
          returnTo: coversHubReturnTo(groupId),
          tab: opts?.tab,
        });
        toast.success("InstaEditor aperto in una nuova scheda: il video resta privato finché non scegli di pubblicarlo.");
        return true;
      }

      // First open: resolve the group-owned workspace, then use the
      // idempotent editor-session endpoint. The server validates the
      // workspace/account/video binding and returns the stable
      // velox_project_id + project URL.
      const workspaceID = await resolveGroupWorkspace(groupId);
      // Quick-create path: the caller asks for a random project name, so
      // stamp it as the session draft BEFORE opening the editor — the
      // covers card renders draft_title and the editor pre-fills it. The
      // session is created standalone (not via createEditorSessionAndOpen)
      // so the draft write happens before the tab opens.
      if (opts?.draftTitle) {
        const session = await createYouTubeEditorSession(buildSessionPayload(video, workspaceID));
        await authedFetch(
          `/api/v1/youtube/editor-sessions/by-project/${encodeURIComponent(session.velox_project_id)}/draft`,
          {
            method: "PUT",
            body: JSON.stringify({
              title: opts.draftTitle,
              description: "",
              tags: [],
              desired_privacy: video.desired_privacy || "private",
              publish_at: video.publish_at ?? null,
            }),
          },
        );
        await openInstaEditorWithLaunch(session.editor_url, session.velox_project_id, {
          returnTo: coversHubReturnTo(groupId),
          tab: opts?.tab,
        });
        toast.success("InstaEditor aperto in una nuova scheda: il video resta privato finché non scegli di pubblicarlo.");
        return true;
      }
      await createEditorSessionAndOpen(buildSessionPayload(video, workspaceID), {}, {
        returnTo: coversHubReturnTo(groupId),
        tab: opts?.tab,
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

  // One-click "Crea copertina" for the hub: open InstaEditor straight
  // away for the group's most recent private video and save the new
  // cover under a random name. Resolves to true when the editor opened;
  // surfaces failures (no private videos, workspace issues) via toast.
  const quickCreateCover = useCallback(async (tab?: Window | null): Promise<boolean> => {
    if (state.kind === "loading") {
      toast.info("Caricamento video del gruppo…");
      return false;
    }
    if (state.kind === "error") {
      toast.error(state.message);
      return false;
    }
    // Derived filter on the canonical list: the one-click create draws
    // its canvas from the group's newest PRIVATE video. The manifest is
    // date-descending, so the first private (non-phantom) row is the
    // most recent one.
    const firstVideo = state.videos.find((video) => {
      const privacy = String(video.actual_privacy ?? video.privacy_status ?? "").toLowerCase();
      return privacy === "private" && video.phantom !== true;
    });
    if (!firstVideo) {
      toast.error("Nessun video privato nel gruppo: carica un video su YouTube per crearci la copertina.");
      return false;
    }
    // The random title embeds the group name (e.g. "Amish-Nebula-42") so
    // the cover reads as belonging to this group at a glance.
    return openThumbnailEditor(firstVideo, { draftTitle: generateCoverName(groupName), tab });
  }, [groupName, openThumbnailEditor, state, toast]);

  const openVideoPreview = useCallback((video: GroupYouTubeVideo) => {
    // Preview is deliberately local and deterministic: no NVIDIA metadata
    // request is needed just to inspect the thumbnail.
    setPreview({ video });
    setDraftTitle(video.title ?? "");
    setDraftDescription(video.draft_description ?? video.description ?? "");
    setEditCategoryID(video.category_id ?? "");
    setEditPrivacyStatus(
      video.privacy_status
        ?? (isYouTubePrivacyStatus(video.actual_privacy) ? video.actual_privacy : "private"),
    );
  }, []);

  // "Modifica video" drawer save: PATCH the single metadata endpoint
  // (title/description/category) under the group's owning channel via
  // the shared videosApi — the backend merges into the canonical
  // YouTube snippet (preserving tags and omitted fields), and the API
  // layer invalidates ONLY the group-videos cache afterwards so the
  // cards refresh without reloading the rest of InstaEdit. The
  // editor-session draft is untouched: it stays the cover project's
  // own content.
  const saveVideoMetadata = useCallback(async () => {
    if (!preview || savingMetadata) return;
    setSavingMetadata(true);
    try {
      await patchGroupVideoMetadata(groupId, preview.video.youtube_video_id, {
        platform_account_id: preview.video.platform_account_id,
        title: draftTitle,
        description: draftDescription,
        category_id: editCategoryID,
        privacy_status: editPrivacyStatus,
      });
      setPreview({
        video: {
          ...preview.video,
          title: draftTitle,
          description: draftDescription,
          category_id: editCategoryID,
          privacy_status: editPrivacyStatus,
          actual_privacy: editPrivacyStatus,
        },
      });
      toast.success("Metadati video salvati.");
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      toast.error(error instanceof Error ? error.message : "Impossibile salvare i metadati.");
    } finally {
      setSavingMetadata(false);
    }
  }, [draftDescription, draftTitle, editCategoryID, editPrivacyStatus, groupId, navigate, preview, savingMetadata, toast]);

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
        // Canonical query: every manageable video (private, public,
        // unlisted, phantom published sessions) reaches state as-is.
        // Visibility filters are DERIVED by consumers on privacyStatus;
        // the backend stays a single resource — no per-visibility
        // endpoints, one cache, one list. Each row is normalized once:
        // privacy_status coerced to the strict union (unknown values
        // become undefined) and the availability projection stamped.
        const videos = (data.videos ?? []).map((video) => ({
          ...video,
          privacy_status: isYouTubePrivacyStatus(video.privacy_status) ? video.privacy_status : undefined,
          availability: videoAvailability(video),
        }));
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

  const applyThumbnailFile = useCallback(async (videoId: string, platformAccountId: number, file: File) => {
    if (thumbnailVideoID) return;
    setThumbnailVideoID(videoId);
    try {
      const asset = await uploadThumbnailFile(file);
      await publishGroupThumbnail(groupId, videoId, platformAccountId, asset.id);
      toast.success("Copertina salvata e pubblicata su YouTube.");
      await refreshVideos(false, true);
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile salvare la copertina.");
    } finally {
      setThumbnailVideoID(null);
    }
  }, [groupId, navigate, refreshVideos, thumbnailVideoID, toast]);

  const applyThumbnailMedia = useCallback(async (videoId: string, platformAccountId: number, mediaId: string) => {
    if (thumbnailVideoID) return;
    setThumbnailVideoID(videoId);
    try {
      await publishGroupThumbnail(groupId, videoId, platformAccountId, mediaId);
      toast.success("Copertina bozza applicata e pubblicata su YouTube.");
      await refreshVideos(false, true);
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile applicare la bozza.");
    } finally {
      setThumbnailVideoID(null);
    }
  }, [groupId, navigate, refreshVideos, thumbnailVideoID, toast]);

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

  // Focus/visibility refetch: the cover publish happens in InstaEditor
  // on a DIFFERENT origin, so the same-origin invalidation bus cannot
  // reach this tab (BroadcastChannel is origin-scoped). When the user
  // returns here — tab refocused / made visible again — refetch the
  // canonical list so the card thumbnails reflect the cover just
  // published. forceRefresh=false is enough: the backend already
  // dropped its per-account cache on publish, so a plain list fetch
  // returns the fresh thumbnail URL.
  useEffect(() => {
    if (!enabled) return;
    const refetchWhenVisible = () => {
      if (typeof document !== "undefined" && (document.hidden || document.visibilityState === "hidden")) return;
      void refreshVideos(false, false);
    };
    window.addEventListener("focus", refetchWhenVisible);
    document.addEventListener("visibilitychange", refetchWhenVisible);
    return () => {
      window.removeEventListener("focus", refetchWhenVisible);
      document.removeEventListener("visibilitychange", refetchWhenVisible);
    };
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

  // Targeted invalidation subscriber: when `invalidateGroupVideos` fires
  // (a metadata/cover save in this tab or another tab), refresh ONLY the
  // canonical video list. resetPolling=false keeps the current rows
  // rendered while the refresh happens in the background; forceRefresh
  // bypasses the backend list cache so the cards pick up the new state.
  const handleGroupVideosInvalidated = useCallback(() => {
    void refreshVideos(false, true);
  }, [refreshVideos]);
  useGroupVideosInvalidation(groupId, handleGroupVideosInvalidated);

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
    editCategoryID,
    setEditCategoryID,
    editPrivacyStatus,
    setEditPrivacyStatus,
    savingMetadata,
    openThumbnailEditor,
    quickCreateCover,
    openVideoPreview,
    saveVideoMetadata,
    applyThumbnailFile,
    applyThumbnailMedia,
    thumbnailVideoID,
    refreshVideos,
    loadMoreVideos,
  };
}
