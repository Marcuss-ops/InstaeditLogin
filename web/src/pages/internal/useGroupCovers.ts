import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError, ApiError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { coversHubReturnTo, openInstaEditorWithLaunch } from "../../features/youtube/api/editorSessionsApi";
import { invalidateGroupVideos } from "../../features/youtube/hooks/useGroupVideosInvalidation";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";
import type { CoversLoadState, GroupCover, GroupDraft } from "./groupCoversTypes";

/**
 * Resolve the signed preview URL for a cover's rendered preview media
 * asset. The media library contract (GET /api/v1/media/{id}) mints the
 * signed URL server-side; we reuse it so the covers grid never has to
 * bundle storage credentials. Both the rendered preview and the attached
 * thumbnail asset are resolved because older cover projects may have only
 * one of the two IDs.
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
  const [openingDraftId, setOpeningDraftId] = useState<string | null>(null);
  const [renamingCoverId, setRenamingCoverId] = useState<string | null>(null);
  const [savingCoverId, setSavingCoverId] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<GroupDraft[]>([]);
  const toast = useToast();

  const loadCovers = useCallback(
    async (signal: AbortSignal) => {
      try {
        const response = await authedFetch(`/api/v1/groups/${groupId}/covers`, { signal });
        if (signal.aborted) return;
        const data = (await response.json()) as { covers?: GroupCover[] };
        if (signal.aborted) return;
        const covers = data.covers ?? [];
        let workspaceID: number | undefined;
        try {
          const groupResponse = await authedFetch(`/api/v1/groups/${groupId}`, { signal });
          workspaceID = ((await groupResponse.json()) as { workspace_id?: number }).workspace_id;
        } catch {
          // The covers list is the primary surface. A temporary failure of
          // the auxiliary group lookup must never hide existing covers.
        }
        let groupDrafts: GroupDraft[] = [];
        if (workspaceID) {
          try {
            const projectsResponse = await authedFetch(`/api/v1/thumbnail-projects?workspace_id=${workspaceID}`, { signal });
            const projectsData = (await projectsResponse.json()) as { items?: GroupDraft[] };
            const marker = `[instaedit-group:${groupId}]`;
            groupDrafts = (projectsData.items ?? []).filter((item) => item.status !== "deleted" && (item.description ?? "").includes(marker));
            await Promise.all(groupDrafts.filter((item) => !item.preview_media_id).map(async (item) => {
              try {
                const assetsResponse = await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(item.id)}/assets?workspace_id=${workspaceID}`, { signal });
                const assets = (await assetsResponse.json()) as { items?: Array<{ media_id?: string }> };
                const mediaID = assets.items?.find((asset) => asset.media_id)?.media_id;
                if (mediaID) item.preview_media_id = mediaID;
              } catch {
                // A draft without a linked asset remains visible as a draft.
              }
            }));
            // Standalone drafts use the same Velox project bridge as normal
            // YouTube covers. Creating it here makes every existing draft
            // editable immediately, including drafts created by the agent.
            await Promise.all(groupDrafts.map(async (item) => {
              try {
                const bridgeResponse = await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(item.id)}/velox-bridge`, {
                  method: "POST",
                  body: JSON.stringify({ contract_version: "instaedit.velox.project-bridge.v1", workspace_id: workspaceID }),
                  signal,
                });
                const bridge = (await bridgeResponse.json()) as { editor_url?: string; bridge?: { external_project_id?: string } };
                item.editor_url = bridge.editor_url;
                item.external_project_id = bridge.bridge?.external_project_id;
              } catch {
                // Keep the draft visible even if the editor provider is
                // temporarily unavailable; the card can retry on refresh.
              }
            }));
            setDrafts(groupDrafts);
          } catch {
            setDrafts([]);
          }
        }
        // Resolve every real cover asset, not the original YouTube thumbnail.
        // A cover can be represented by either the rendered project preview
        // or the attached thumbnail media depending on its lifecycle state.
        const previewUrls: Record<string, string | undefined> = {};
        const mediaIds = Array.from(new Set([
          ...covers.flatMap((cover) => [cover.preview_media_id, cover.thumbnail_media_id]),
          ...groupDrafts.map((draft) => draft.preview_media_id),
        ].filter((id): id is string => Boolean(id))));
        await Promise.all(
          mediaIds.map(async (mediaId) => {
              const url = await resolveCoverPreview(mediaId, signal);
              if (!signal.aborted) previewUrls[mediaId] = url;
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

  /**
   * Inline rename of a cover from its card in the Copertine hub. The card
   * title is editable; on commit we PUT a PARTIAL draft update ({ title }
   * only) so the new name lands in youtube_video_edits.draft_title — the
   * field the hub card renders. The backend merges the partial body, so
   * description/tags/privacy are never wiped by a rename.
   *
   * Returns true when the rename was persisted (or was a no-op); false
   * when the cover has no editor session or the PUT failed.
   */
  const renameCover = useCallback(
    async (cover: GroupCover, newTitle: string): Promise<boolean> => {
      const trimmed = newTitle.trim();
      if (!cover.velox_project_id) {
        toast.error("Copertina non associata a un progetto editor.");
        return false;
      }
      const current = cover.draft_title || cover.name || "";
      // No-op: same title (or empty → keep whatever the DB has).
      if (!trimmed || trimmed === current.trim()) return true;
      setRenamingCoverId(cover.project_id);
      try {
        await authedFetch(
          `/api/v1/youtube/editor-sessions/by-project/${encodeURIComponent(cover.velox_project_id)}/draft`,
          {
            method: "PUT",
            body: JSON.stringify({ title: trimmed }),
          },
        );
        // Optimistic local update so the card reflects the new name
        // without a full grid reload.
        setState((previous) => {
          if (previous.kind !== "ready") return previous;
          return {
            ...previous,
            covers: previous.covers.map((c) =>
              c.project_id === cover.project_id ? { ...c, draft_title: trimmed } : c,
            ),
          };
        });
        toast.success("Titolo copertina salvato.");
        // Targeted invalidation: the cover draft feeds the linked video's
        // metadata, so refresh only the group video-list cache
        // (`['groups', groupId, 'youtube', 'videos']`).
        invalidateGroupVideos(groupId);
        return true;
      } catch (error) {
        if (error instanceof AuthError) {
          navigate("/login", { replace: true });
          return false;
        }
        toast.error(error instanceof Error ? error.message : "Impossibile salvare il titolo.");
        return false;
      } finally {
        setRenamingCoverId(null);
      }
    },
    [groupId, navigate, toast],
  );

  const openCoverEditor = useCallback(async (cover: GroupCover, tab?: Window | null): Promise<boolean> => {
    if (!cover.velox_project_id) {
      tab?.close();
      return false;
    }
    setOpeningCoverId(cover.project_id);
    try {
      // If the server already minted an editor URL, reuse it directly;
      // otherwise the create-session helper would be needed (rare — the
      // covers hub only lists covers that already have a session).
      if (cover.editor_url) {
        // The editor Home pill links back to this group's Copertine hub
        // (return_to is a relative SPA path, stamped after validation).
        // `tab` is the window opened synchronously in the click gesture,
        // navigated once the launch URL is minted (popup-proof).
        await openInstaEditorWithLaunch(cover.editor_url, cover.velox_project_id, {
          returnTo: coversHubReturnTo(groupId),
          tab,
        });
        return true;
      }
      tab?.close();
      return false;
    } catch (error) {
      // Any failure means the reserved tab was never navigated (the
      // navigation is the last step), so close it instead of leaking a
      // blank tab.
      tab?.close();
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
      }
      return false;
    } finally {
      setOpeningCoverId(null);
    }
  }, [groupId, navigate]);

  const openDraftEditor = useCallback(async (draft: GroupDraft, tab?: Window | null): Promise<boolean> => {
    setOpeningDraftId(draft.id);
    try {
      let editorURL = draft.editor_url;
      let externalProjectID = draft.external_project_id;
      if (!editorURL || !externalProjectID) {
        const groupResponse = await authedFetch(`/api/v1/groups/${groupId}`);
        const group = (await groupResponse.json()) as { workspace_id?: number };
        if (!group.workspace_id) throw new Error("Workspace del gruppo non disponibile.");
        const bridgeResponse = await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(draft.id)}/velox-bridge`, {
          method: "POST",
          body: JSON.stringify({ contract_version: "instaedit.velox.project-bridge.v1", workspace_id: group.workspace_id }),
        });
        const bridge = (await bridgeResponse.json()) as { editor_url?: string; bridge?: { external_project_id?: string } };
        editorURL = bridge.editor_url;
        externalProjectID = bridge.bridge?.external_project_id;
        setDrafts((items) => items.map((item) => item.id === draft.id ? { ...item, editor_url: editorURL, external_project_id: externalProjectID } : item));
      }
      if (!editorURL || !externalProjectID) throw new Error("Editor locale non disponibile per questa bozza.");
      await openInstaEditorWithLaunch(editorURL, externalProjectID, { returnTo: coversHubReturnTo(groupId), tab });
      return true;
    } catch (error) {
      tab?.close();
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile aprire la bozza.");
      return false;
    } finally {
      setOpeningDraftId(null);
    }
  }, [groupId, navigate, toast]);

  const createStandaloneDraft = useCallback(async (name: string): Promise<boolean> => {
    try {
      const groupResponse = await authedFetch(`/api/v1/groups/${groupId}`);
      const group = (await groupResponse.json()) as { workspace_id?: number };
      if (!group.workspace_id) throw new Error("Workspace del gruppo non disponibile.");
      const response = await authedFetch("/api/v1/thumbnail-projects", {
        method: "POST",
        body: JSON.stringify({
          workspace_id: group.workspace_id,
          name: name.trim() || `Bozza ${new Date().toLocaleDateString("it-IT")}`,
          description: `[instaedit-group:${groupId}] Bozza creata senza video`,
          canvas_width: 1280,
          canvas_height: 720,
        }),
      });
      const draft = (await response.json()) as GroupDraft;
      // Materialize the bridge at creation time as well, so the new card is
      // immediately equivalent to a normal cover card.
      try {
        const bridgeResponse = await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(draft.id)}/velox-bridge`, {
          method: "POST",
          body: JSON.stringify({ contract_version: "instaedit.velox.project-bridge.v1", workspace_id: group.workspace_id }),
        });
        const bridge = (await bridgeResponse.json()) as { editor_url?: string; bridge?: { external_project_id?: string } };
        draft.editor_url = bridge.editor_url;
        draft.external_project_id = bridge.bridge?.external_project_id;
      } catch {
        // The draft remains saved and can retry bridge creation when opened.
      }
      setDrafts((items) => [draft, ...items]);
      toast.success("Bozza salvata nel gruppo.");
      return true;
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile creare la bozza.");
      return false;
    }
  }, [groupId, navigate, toast]);

  const duplicateDraftToGroup = useCallback(async (draft: GroupDraft, targetGroupId: number): Promise<boolean> => {
    try {
      const groupResponse = await authedFetch(`/api/v1/groups/${targetGroupId}`);
      const group = (await groupResponse.json()) as { workspace_id?: number };
      if (!group.workspace_id) throw new Error("Workspace del gruppo destinatario non disponibile.");
      const response = await authedFetch("/api/v1/thumbnail-projects", {
        method: "POST",
        body: JSON.stringify({ workspace_id: group.workspace_id, name: `${draft.name} · copia`, description: `[instaedit-group:${targetGroupId}] Copia da ${groupId}`, canvas_width: 1280, canvas_height: 720 }),
      });
      const copy = (await response.json()) as GroupDraft;
      try {
        const assetsResponse = await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(draft.id)}/assets?workspace_id=${draft.workspace_id}`);
        const assets = (await assetsResponse.json()) as { items?: Array<{ media_id: string; role: string; object_id?: string }> };
        for (const asset of assets.items ?? []) {
          await authedFetch(`/api/v1/thumbnail-projects/${encodeURIComponent(copy.id)}/assets?workspace_id=${group.workspace_id}`, {
            method: "POST",
            body: JSON.stringify({ media_id: asset.media_id, role: asset.role, object_id: asset.object_id }),
          });
        }
      } catch {
        // The project copy remains useful even when an old asset link cannot
        // be duplicated; the user can attach a new image in the editor.
      }
      if (targetGroupId === groupId) setDrafts((items) => [copy, ...items]);
      toast.success("Bozza duplicata nel gruppo selezionato.");
      return true;
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile duplicare la bozza.");
      return false;
    }
  }, [groupId, navigate, toast]);

  const saveCoverDraft = useCallback(async (cover: GroupCover, title: string, description: string): Promise<boolean> => {
    if (!cover.velox_project_id) return false;
    setSavingCoverId(cover.project_id);
    try {
      await authedFetch(
        `/api/v1/youtube/editor-sessions/by-project/${encodeURIComponent(cover.velox_project_id)}/draft`,
        { method: "PUT", body: JSON.stringify({ title: title.trim(), description }) },
      );
      setState((previous) => previous.kind !== "ready" ? previous : {
        ...previous,
        covers: previous.covers.map((item) => item.project_id === cover.project_id
          ? { ...item, draft_title: title.trim(), draft_description: description }
          : item),
      });
      toast.success("Modifiche copertina salvate.");
      // Targeted invalidation: only the group video-list cache
      // (`['groups', groupId, 'youtube', 'videos']`) is refreshed, so
      // the cards reflect the new draft without reloading InstaEdit.
      // The optimistic cover title remains local; the video manager still
      // receives the targeted invalidation without reloading this grid.
      invalidateGroupVideos(groupId);
      return true;
    } catch (error) {
      if (error instanceof AuthError) navigate("/login", { replace: true });
      else toast.error(error instanceof Error ? error.message : "Impossibile salvare la copertina.");
      return false;
    } finally {
      setSavingCoverId(null);
    }
  }, [groupId, navigate, toast]);

  return { state, drafts, refreshCovers, createStandaloneDraft, duplicateDraftToGroup, openCoverEditor, openingCoverId, openDraftEditor, openingDraftId, renameCover, renamingCoverId, saveCoverDraft, savingCoverId };
}
