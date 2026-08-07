/**
 * CoverEditor — the autonomous Dark Editor canvas page.
 *
 * A project is a graphic canvas with NO YouTube prerequisite. This page
 * edits a PERSISTENT canvas:
 *
 *   - objects (text / rect / image) with coordinates, transformations
 *     (rotation, scale_x/scale_y), layer order, background and media
 *     references — exactly the snapshot schema_version 1 the canonical
 *     renderer rasterizes;
 *   - every change flows into the debounced autosave
 *     (useThumbnailAutosave → PUT /api/v1/thumbnail-projects/{id}/snapshot)
 *     with a REAL save indicator ("Salvataggio… / Salvato alle HH:MM /
 *     Modifiche non salvate / Errore di salvataggio") — never a false
 *     "Salvato";
 *   - a 409 PROJECT_VERSION_CONFLICT pauses autosave and offers
 *     "Ricarica versione recente" or "Salva come copia" (never silent
 *     last-write-wins on the canvas);
 *   - the on-canvas preview mirrors the renderer's transform math
 *     (box scaled to width·scale_x × height·scale_y, placed at (x,y),
 *     rotated around its center) so editing preview ≈ exported pixels.
 *
 * This file owns the page orchestration (load, autosave, export, save
 * as copy, close) and composes the extracted editor components under
 * features/thumbnailProjects/components/editor/.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import {
  createThumbnailProject,
  getThumbnailProject,
  getThumbnailRevision,
  listThumbnailAssignments,
  listThumbnailRevisions,
  renderThumbnailProject,
  saveThumbnailSnapshot,
  THUMBNAIL_RENDERER_VERSION,
} from "../../features/thumbnailProjects/api/thumbnailProjectsApi";
import { resolveProjectMedia } from "../../features/thumbnailProjects/media/mediaResolver";
import { useThumbnailAutosave } from "../../features/thumbnailProjects/hooks/useThumbnailAutosave";
import { useCoverEditorMutations } from "../../features/thumbnailProjects/hooks/useCoverEditorMutations";
import {
  MediaPickerDialog,
  type MediaPickerDetail,
} from "../../features/thumbnailProjects/components/MediaPickerDialog";
import {
  ExportPanel,
  type ExportUiState,
} from "../../features/thumbnailProjects/components/ExportPanel";
import { LinkToVideoDialog } from "../../features/thumbnailProjects/components/LinkToVideoDialog";
import { AssignmentsPanel } from "../../features/thumbnailProjects/components/editor/AssignmentsPanel";
import { CanvasSettingsPanel } from "../../features/thumbnailProjects/components/editor/CanvasSettingsPanel";
import { CanvasStage } from "../../features/thumbnailProjects/components/editor/CanvasStage";
import { ConflictBanner } from "../../features/thumbnailProjects/components/editor/ConflictBanner";
import { EditorHeader } from "../../features/thumbnailProjects/components/editor/EditorHeader";
import { EditorToolbar } from "../../features/thumbnailProjects/components/editor/EditorToolbar";
import { Inspector } from "../../features/thumbnailProjects/components/editor/Inspector";
import { LayersPanel } from "../../features/thumbnailProjects/components/editor/LayersPanel";
import { RevisionPanel } from "../../features/thumbnailProjects/components/editor/RevisionPanel";
import {
  DEFAULT_BACKGROUND,
  normalizeSnapshot,
  type EditorSnapshot,
} from "../../features/thumbnailProjects/editor/snapshot";
import {
  newImageObject,
  newRectObject,
  newTextObject,
} from "../../features/thumbnailProjects/editor/objects";
import type {
  ThumbnailCanvasSnapshot,
  ThumbnailProject,
  ThumbnailProjectAssignment,
  ThumbnailProjectRevision,
} from "../../features/thumbnailProjects/types";

// ─── Load state ────────────────────────────────────────────────────

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; project: ThumbnailProject; workspaceId: number }
  | { kind: "error"; message: string };

// ─── Page ──────────────────────────────────────────────────────────

export function CoverEditorPage() {
  const navigate = useNavigate();
  const { projectId } = useParams<{ projectId: string }>();
  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [snapshot, setSnapshot] = useState<EditorSnapshot | null>(null);
  const [version, setVersion] = useState(0);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [mediaUrls, setMediaUrls] = useState<Map<string, string>>(new Map());
  const [revisions, setRevisions] = useState<ThumbnailProjectRevision[]>([]);
  const [assignments, setAssignments] = useState<ThumbnailProjectAssignment[]>([]);
  const [showMediaPicker, setShowMediaPicker] = useState(false);
  const [exportState, setExportState] = useState<ExportUiState>({ kind: "idle" });
  const [exportPreviewUrl, setExportPreviewUrl] = useState<string | null>(null);
  const [showLinkDialog, setShowLinkDialog] = useState(false);
  const [isSavingCopy, setIsSavingCopy] = useState(false);
  const [isManualSaving, setIsManualSaving] = useState(false);
  // The server's latest persisted revision id (advances only from the
  // snapshot ack). ExportPanel compares it against an export's
  // revision_id to PROVE preview/export derive from the same snapshot.
  const [latestRevisionId, setLatestRevisionId] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  // Holds the autosave baseline resetter so loadProject (defined before
  // the hook call) can re-baseline the server truth after a load/reload
  // without ever re-saving the freshly loaded snapshot.
  const autosaveResetRef = useRef<((s: ThumbnailCanvasSnapshot, v: number) => void) | null>(null);

  const loadProject = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoadState({ kind: "loading" });
    if (!projectId) {
      setLoadState({ kind: "error", message: "Progetto non specificato." });
      return;
    }
    try {
      const wsResp = await authedFetch("/api/v1/workspaces", { signal: controller.signal });
      if (controller.signal.aborted) return;
      const { workspaces } = (await wsResp.json()) as { workspaces: { id: number }[] };
      if (workspaces.length === 0) {
        setLoadState({ kind: "error", message: "Nessun workspace disponibile." });
        return;
      }
      const wsId = workspaces[0]!.id;

      const project = await getThumbnailProject(wsId, projectId, { signal: controller.signal });
      let initial: EditorSnapshot;
      if (project.current_revision_id) {
        const revision = await getThumbnailRevision(wsId, projectId, project.current_revision_id, {
          signal: controller.signal,
        });
        initial = normalizeSnapshot(revision.snapshot_json);
      } else {
        initial = {
          canvas: { width: project.canvas_width, height: project.canvas_height, background: DEFAULT_BACKGROUND },
          objects: [],
        };
      }

      const resolved = await resolveProjectMedia(wsId, projectId, initial, { signal: controller.signal });
      const urlMap = new Map<string, string>();
      for (const item of resolved.values()) urlMap.set(item.media_id, item.url);

      const [revList, assignList] = await Promise.all([
        listThumbnailRevisions(wsId, projectId, { signal: controller.signal }).catch(() => []),
        listThumbnailAssignments(wsId, projectId, { signal: controller.signal }).catch(() => []),
      ]);
      if (controller.signal.aborted) return;

      setSnapshot(initial);
      setVersion(project.version);
      setRevisions(revList);
      setAssignments(assignList);
      setLatestRevisionId(project.current_revision_id ?? revList[0]?.id ?? null);
      setMediaUrls(urlMap);
      setSelectedId(null);
      setLoadState({ kind: "ready", project, workspaceId: wsId });
      // Re-baseline the autosave to the loaded snapshot/version so the
      // freshly loaded state is never re-saved as if it were an edit.
      autosaveResetRef.current?.(initial, project.version);
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Impossibile caricare il progetto.";
      setLoadState({ kind: "error", message });
    }
  }, [projectId, navigate]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void loadProject();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadProject, navigate]);

  const workspaceId = loadState.kind === "ready" ? loadState.workspaceId : 0;
  const ready = loadState.kind === "ready" && snapshot !== null;

  const autosave = useThumbnailAutosave({
    workspaceId,
    projectId: projectId ?? "",
    snapshot: snapshot ?? { canvas: { width: 1920, height: 1080, background: DEFAULT_BACKGROUND }, objects: [] },
    version,
    enabled: ready,
    onSaved: (result) => {
      setVersion(result.version);
      setLatestRevisionId(result.revision_id);
    },
  });
  autosaveResetRef.current = autosave.reset;

  // Reload server truth (also used after a 409 conflict); loadProject
  // re-baselines the autosave internally.
  const handleReload = useCallback(() => {
    void loadProject();
  }, [loadProject]);

  const flushPendingAutosave = useCallback(async (): Promise<boolean> => {
    // Await the pending debounce + any save in flight so the server holds
    // the LATEST snapshot before any export/preview/duplicate/link.
    return autosave.flush();
  }, [autosave]);

  // "Salva progetto": manual immediate flush — never waits the debounce.
  // The SaveIndicator reflects the REAL outcome (Salvataggio… → Salvato
  // alle HH:MM only from the server ack; Errore di salvataggio on fail).
  const handleManualSave = useCallback(() => {
    if (isManualSaving) return;
    setIsManualSaving(true);
    void flushPendingAutosave().finally(() => setIsManualSaving(false));
  }, [flushPendingAutosave, isManualSaving]);

  // Export flow: flush → canonical server render → ready export.
  const handleGenerateExport = useCallback(async () => {
    if (loadState.kind !== "ready" || !snapshot) return;
    if (autosave.conflict) {
      setExportState({
        kind: "failed",
        message: "Conflitto di versione: risolvi prima il conflitto (ricarica o salva come copia).",
      });
      return;
    }
    setExportState({ kind: "rendering" });
    setExportPreviewUrl(null);
    // NEVER act on a stale revision: await the pending autosave first.
    const ok = await flushPendingAutosave();
    if (!ok) {
      setExportState({
        kind: "failed",
        message:
          "Salvataggio non completato — il render non può partire da una revisione stantia. Riprova.",
      });
      return;
    }
    try {
      const exported = await renderThumbnailProject(loadState.workspaceId, projectId ?? "");
      setExportState({ kind: "ready", export: exported });
      // Mint a presigned URL for the rendered PNG via the media resolver
      // (the server-authoritative file, never a second browser canvas).
      void resolveProjectMedia(
        loadState.workspaceId,
        projectId ?? "",
        { objects: [{ id: "export", type: "image", media_id: exported.media_id }] },
      ).then((resolved) => {
        const item = resolved.get(exported.media_id);
        if (item) setExportPreviewUrl(item.url);
      }).catch(() => {
        // preview stays absent; download is unavailable but export stands
      });
    } catch (err) {
      setExportState({
        kind: "failed",
        message: err instanceof Error ? err.message : "Generazione copertina fallita.",
      });
    }
  }, [loadState, snapshot, autosave.conflict, flushPendingAutosave, projectId]);

  // "Salva come copia" (from a 409 conflict dialog): creates a NEW
  // autonomous project carrying the CURRENT local snapshot — the other
  // tab's version is untouched. Never silent last-write-wins.
  const handleSaveAsCopy = useCallback(async () => {
    if (loadState.kind !== "ready" || !snapshot) return;
    setIsSavingCopy(true);
    try {
      const copy = await createThumbnailProject({
        workspace_id: loadState.workspaceId,
        name: `${loadState.project.name} (copia)`,
        canvas_width: snapshot.canvas.width,
        canvas_height: snapshot.canvas.height,
      });
      await saveThumbnailSnapshot(loadState.workspaceId, copy.id, {
        schema_version: 1,
        snapshot: {
          canvas: { width: snapshot.canvas.width, height: snapshot.canvas.height, background: snapshot.canvas.background },
          objects: snapshot.objects,
        },
        renderer_version: THUMBNAIL_RENDERER_VERSION,
        base_version: copy.version,
      });
      navigate(`/app/covers/${copy.id}`);
    } catch (err) {
      setIsSavingCopy(false);
      setExportState({
        kind: "failed",
        message: err instanceof Error ? err.message : "Salvataggio come copia fallito.",
      });
    }
  }, [loadState, snapshot, navigate]);

  // Close the editor: await the pending autosave BEFORE navigating away.
  // If the flush fails (network error / 409 conflict) the user stays and
  // sees the honest error — never silently discard unsaved edits.
  const handleCloseEditor = useCallback(() => {
    void (async () => {
      const ok = await flushPendingAutosave();
      if (ok) navigate("/app/covers");
    })();
  }, [flushPendingAutosave, navigate]);

  const handleAssignmentsCreated = useCallback(() => {
    if (loadState.kind === "ready") {
      void listThumbnailAssignments(loadState.workspaceId, projectId ?? "")
        .then(setAssignments)
        .catch(() => {});
    }
  }, [loadState, projectId]);

  if (loadState.kind === "loading") {
    return (
      <div className="min-h-full p-8">
        <div className="mx-auto max-w-7xl grid gap-6 lg:grid-cols-[1fr_300px]">
          <Skeleton variant="card" height={480} />
          <div className="space-y-4">
            <Skeleton variant="card" height={200} />
            <Skeleton variant="card" height={160} />
          </div>
        </div>
      </div>
    );
  }

  if (loadState.kind === "error") {
    return (
      <div className="min-h-full p-8">
        <div className="mx-auto max-w-3xl">
          <ErrorState
            title="Impossibile caricare il progetto"
            message={loadState.message}
            onRetry={() => void loadProject()}
          />
        </div>
      </div>
    );
  }

  const { project } = loadState;

  const {
    updateObject,
    addObject,
    removeSelected,
    duplicateSelected,
    reorder,
    setBackground,
  } = useCoverEditorMutations({
    snapshot,
    setSnapshot,
    selectedId,
    setSelectedId,
  });

  const handlePickMedia = (item: MediaPickerDetail) => {
    setMediaUrls((prev) => {
      const next = new Map(prev);
      if (item.preview_url) next.set(item.id, item.preview_url);
      return next;
    });
    addObject(newImageObject(item));
    setShowMediaPicker(false);
  };

  const selectedObject = snapshot?.objects.find((o) => o.id === selectedId) ?? null;

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="mx-auto max-w-7xl">
        {showMediaPicker && (
          <MediaPickerDialog onPick={handlePickMedia} onClose={() => setShowMediaPicker(false)} />
        )}
        {showLinkDialog && exportState.kind === "ready" && loadState.kind === "ready" && (
          <LinkToVideoDialog
            workspaceId={loadState.workspaceId}
            exportId={exportState.export.id}
            previewUrl={exportPreviewUrl}
            onClose={() => setShowLinkDialog(false)}
            onLinked={handleAssignmentsCreated}
          />
        )}

        <EditorHeader
          projectName={project.name}
          projectId={project.id}
          canvasWidth={snapshot?.canvas.width}
          canvasHeight={snapshot?.canvas.height}
          saveStatus={autosave.status}
          lastSavedAt={autosave.lastSavedAt}
          saveError={autosave.error}
          lastHash={autosave.lastHash}
          onRetry={autosave.retry}
          isManualSaving={isManualSaving}
          saveDisabled={autosave.conflict !== null}
          onManualSave={handleManualSave}
          onClose={handleCloseEditor}
        />

        {/* Conflict dialog — NEVER silent last-write-wins on the canvas */}
        {autosave.conflict && (
          <ConflictBanner
            conflict={autosave.conflict}
            isSavingCopy={isSavingCopy}
            onReload={() => void handleReload()}
            onSaveAsCopy={() => void handleSaveAsCopy()}
          />
        )}

        {/* Toolbar */}
        <EditorToolbar
          onAddText={() => addObject(newTextObject())}
          onAddRect={() => addObject(newRectObject())}
          onOpenMediaPicker={() => setShowMediaPicker(true)}
        />

        {/* Canvas + sidebar */}
        <div className="mt-5 grid gap-5 lg:grid-cols-[1fr_300px]">
          <div className="min-w-0">
            {snapshot && (
              <CanvasStage
                canvas={snapshot.canvas}
                objects={snapshot.objects}
                selectedId={selectedId}
                mediaUrls={mediaUrls}
                onSelect={setSelectedId}
                onMove={(id, x, y) => updateObject(id, { x, y })}
              />
            )}
            <div className="mt-4">
              <ExportPanel
                state={exportState}
                previewUrl={exportPreviewUrl}
                onGenerate={() => void handleGenerateExport()}
                onLink={() => setShowLinkDialog(true)}
                latestRevisionId={latestRevisionId}
                canvasWidth={snapshot?.canvas.width}
                canvasHeight={snapshot?.canvas.height}
                rendererVersion={THUMBNAIL_RENDERER_VERSION}
              />
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <RevisionPanel revisions={revisions} currentRevisionId={project.current_revision_id} />
              <AssignmentsPanel assignments={assignments} />
            </div>
          </div>

          <div className="space-y-4">
            <CanvasSettingsPanel
              background={snapshot?.canvas.background ?? ""}
              canvasWidth={snapshot?.canvas.width}
              canvasHeight={snapshot?.canvas.height}
              onChange={setBackground}
            />

            <LayersPanel
              objects={snapshot?.objects ?? []}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onReorder={reorder}
            />

            {selectedObject ? (
              <Inspector
                object={selectedObject}
                onPatch={(patch) => updateObject(selectedObject.id, patch)}
                onDuplicate={duplicateSelected}
                onDelete={removeSelected}
                onReplaceImage={() => setShowMediaPicker(true)}
              />
            ) : (
              <div className="rounded-2xl border border-dashed border-white/[0.10] bg-[#1a1a28] p-4 text-center text-[12px] text-[#9aa0aa]">
                Seleziona un oggetto nel canvas o nei livelli per modificarne le proprietà.
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
