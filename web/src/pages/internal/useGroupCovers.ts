import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError, ApiError } from "../../lib/auth";
import { coversHubReturnTo, openInstaEditorWithLaunch } from "../../features/youtube/api/editorSessionsApi";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";
import type { CoversLoadState, GroupCover } from "./groupCoversTypes";

/**
 * Resolve the signed preview URL for a cover's rendered preview media
 * asset. The media library contract (GET /api/v1/media/{id}) mints the
 * signed URL server-side; we reuse it so the covers grid never has to
 * bundle storage credentials. Failures degrade to undefined — the card
 * falls back to the YouTube video thumbnail.
 */
async function resolveCoverPreview(
  mediaId: string,
  signal: AbortSignal,
): Promise<string | undefined> {
  try {
    const response = await authedFetch(`/api/v1/media/${encodeURIComponent(mediaId)}`, { signal });
    const data = (await response.json()) as { preview_url?: string };
    return safeAssetUrl(data.preview_url);
  } catch {
    return undefined;
  }
}

export function useGroupCovers(groupId: number) {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<CoversLoadState>({ kind: "loading" });
  const [openingCoverId, setOpeningCoverId] = useState<string | null>(null);

  const loadCovers = useCallback(
    async (signal: AbortSignal) => {
      try {
        const response = await authedFetch(`/api/v1/groups/${groupId}/covers`, { signal });
        if (signal.aborted) return;
        const data = (await response.json()) as { covers?: GroupCover[] };
        if (signal.aborted) return;
        const covers = data.covers ?? [];
        // Lazily resolve preview URLs for covers that have a rendered
        // preview; failures fall back to the video thumbnail in the card.
        const previewUrls: Record<string, string | undefined> = {};
        await Promise.all(
          covers
            .filter((cover) => cover.preview_media_id)
            .map(async (cover) => {
              if (!cover.preview_media_id) return;
              const url = await resolveCoverPreview(cover.preview_media_id, signal);
              if (!signal.aborted) previewUrls[cover.preview_media_id] = url;
            }),
        );
        if (signal.aborted) return;
        setState({ kind: "ready", covers, previewUrls });
      } catch (error) {
        if (signal.aborted) return;
        if (error instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const upstream = error instanceof ApiError && error.status === 502;
        // Generic failures surface a fixed, actionable message instead of
        // leaking internal error strings; only the 502 upstream case is
        // special-cased with its own copy.
        setState({
          kind: "error",
          message: upstream
            ? "YouTube non risponde temporaneamente. Riprova tra poco."
            : "Impossibile caricare le copertine del gruppo. Controlla la connessione e riprova.",
        });
      }
    },
    [groupId, navigate],
  );

  const refreshCovers = useCallback((): void => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });
    void loadCovers(controller.signal);
  }, [loadCovers]);

  useEffect(() => {
    if (groupId <= 0) {
      setState({ kind: "loading" });
      return () => abortRef.current?.abort();
    }
    refreshCovers();
    return () => abortRef.current?.abort();
  }, [groupId, refreshCovers]);

  const openCoverEditor = useCallback(async (cover: GroupCover) => {
    if (!cover.velox_project_id) {
      return;
    }
    setOpeningCoverId(cover.project_id);
    try {
      // If the server already minted an editor URL, reuse it directly;
      // otherwise the create-session helper would be needed (rare — the
      // covers hub only lists covers that already have a session).
      if (cover.editor_url) {
        // The editor Home pill links back to this group's Copertine hub
        // (return_to is a relative SPA path, stamped after validation).
        await openInstaEditorWithLaunch(cover.editor_url, cover.velox_project_id, {
          returnTo: coversHubReturnTo(groupId),
        });
        return;
      }
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
    } finally {
      setOpeningCoverId(null);
    }
  }, [groupId, navigate]);

  return { state, refreshCovers, openCoverEditor, openingCoverId };
}
