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
 * This file is the presentational page: all orchestration (load,
 * autosave, export, save-as-copy, close, mutations) lives in
 * useCoverEditor under features/thumbnailProjects/hooks/, and the
 * editor sections are composed from components/editor/.
 */
import { Skeleton, ErrorState } from "../../components/feedback";
import { THUMBNAIL_RENDERER_VERSION } from "../../features/thumbnailProjects/api/thumbnailProjectsApi";
import { useCoverEditor } from "../../features/thumbnailProjects/hooks/useCoverEditor";
import { MediaPickerDialog } from "../../features/thumbnailProjects/components/MediaPickerDialog";
import { ExportPanel } from "../../features/thumbnailProjects/components/ExportPanel";
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
  newRectObject,
  newTextObject,
} from "../../features/thumbnailProjects/editor/objects";

// ─── Page ──────────────────────────────────────────────────────────

export function CoverEditorPage() {
  const {
    loadState,
    snapshot,
    selectedId,
    setSelectedId,
    mediaUrls,
    revisions,
    assignments,
    showMediaPicker,
    setShowMediaPicker,
    exportState,
    exportPreviewUrl,
    showLinkDialog,
    setShowLinkDialog,
    isSavingCopy,
    isManualSaving,
    latestRevisionId,
    autosave,
    loadProject,
    handleReload,
    handleManualSave,
    handleGenerateExport,
    handleSaveAsCopy,
    handleCloseEditor,
    handleAssignmentsCreated,
    handlePickMedia,
    updateObject,
    addObject,
    removeSelected,
    duplicateSelected,
    reorder,
    setBackground,
    selectedObject,
  } = useCoverEditor();

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
