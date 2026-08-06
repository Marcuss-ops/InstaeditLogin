/**
 * CoverEditor — the autonomous Dark Editor canvas.
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
 *     "Ricarica versione recente" (the full conflict dialog with
 *     "Salva come copia" lands in a later phase);
 *   - the on-canvas preview mirrors the renderer's transform math
 *     (box scaled to width·scale_x × height·scale_y, placed at (x,y),
 *     rotated around its center) so editing preview ≈ exported pixels.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Copy,
  Trash2,
  Type,
  Square,
  ImageIcon,
  ChevronUp,
  ChevronDown,
  Eye,
  EyeOff,
  Hash,
  Link2,
  Layers,
  AlertTriangle,
  RefreshCw,
  CopyPlus,
  Loader2,
  Save,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
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
import { SaveIndicator } from "../../features/thumbnailProjects/components/SaveIndicator";
import {
  MediaPickerDialog,
  type MediaPickerDetail,
} from "../../features/thumbnailProjects/components/MediaPickerDialog";
import {
  ExportPanel,
  type ExportUiState,
} from "../../features/thumbnailProjects/components/ExportPanel";
import { LinkToVideoDialog } from "../../features/thumbnailProjects/components/LinkToVideoDialog";
import type {
  ThumbnailCanvasSnapshot,
  ThumbnailProject,
  ThumbnailProjectAssignment,
  ThumbnailProjectRevision,
  ThumbnailSnapshotObject,
} from "../../features/thumbnailProjects/types";

const DEFAULT_BACKGROUND = "#30305a";
const MAX_CANVAS_DIMENSION = 16384;

/** The editor always works on a normalized snapshot with both keys set. */
interface EditorSnapshot {
  canvas: { width: number; height: number; background: string };
  objects: ThumbnailSnapshotObject[];
}

function normalizeSnapshot(snapshot: ThumbnailCanvasSnapshot | undefined | null): EditorSnapshot {
  return {
    canvas: {
      width: snapshot?.canvas?.width ?? 1920,
      height: snapshot?.canvas?.height ?? 1080,
      background: snapshot?.canvas?.background ?? DEFAULT_BACKGROUND,
    },
    objects: Array.isArray(snapshot?.objects) ? snapshot!.objects! : [],
  };
}

function round(value: number): number {
  return Math.round(value * 100) / 100;
}

function makeId(prefix: string): string {
  return `${prefix}-${crypto.randomUUID?.() ?? Math.random().toString(36).slice(2)}`;
}

function newTextObject(): ThumbnailSnapshotObject {
  return {
    id: makeId("text"),
    type: "text",
    text: "Testo",
    x: 120,
    y: 140,
    width: 720,
    height: 180,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
    fill: "#ffffff",
    font_family: "Inter",
    font_size: 96,
    font_weight: 700,
    text_align: "center",
  };
}

function newRectObject(): ThumbnailSnapshotObject {
  return {
    id: makeId("rect"),
    type: "rect",
    x: 240,
    y: 240,
    width: 480,
    height: 260,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
    fill: "#0a84ff",
    radius: 16,
  };
}

function newImageObject(item: MediaPickerDetail): ThumbnailSnapshotObject {
  const width =
    item.width && item.width > 0 ? Math.min(item.width, MAX_CANVAS_DIMENSION) : 480;
  const height =
    item.height && item.height > 0 ? Math.min(item.height, MAX_CANVAS_DIMENSION) : 270;
  return {
    id: makeId("img"),
    type: "image",
    media_id: item.id,
    x: 0,
    y: 0,
    width,
    height,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
  };
}

/** Mirror the renderer: box = width·scale_x × height·scale_y at (x,y),
 *  rotated around its center (composite() in thumbnailrender/render.go). */
function objectBox(obj: ThumbnailSnapshotObject): { width: number; height: number } {
  return {
    width: (obj.width ?? 0) * (obj.scale_x ?? 1),
    height: (obj.height ?? 0) * (obj.scale_y ?? 1),
  };
}

// ─── Load state ────────────────────────────────────────────────────

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; project: ThumbnailProject; workspaceId: number }
  | { kind: "error"; message: string };

// ─── Canvas stage ──────────────────────────────────────────────────

interface CanvasStageProps {
  canvas: EditorSnapshot["canvas"];
  objects: ThumbnailSnapshotObject[];
  selectedId: string | null;
  mediaUrls: Map<string, string>;
  onSelect: (id: string | null) => void;
  onMove: (id: string, x: number, y: number) => void;
}

function CanvasStage({
  canvas,
  objects,
  selectedId,
  mediaUrls,
  onSelect,
  onMove,
}: CanvasStageProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [stageWidth, setStageWidth] = useState(0);
  const dragRef = useRef<{
    id: string;
    startX: number;
    startY: number;
    origX: number;
    origY: number;
  } | null>(null);

  useEffect(() => {
    const node = wrapperRef.current;
    if (!node) return;
    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width;
      if (width) setStageWidth(width);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const scale = stageWidth > 0 ? stageWidth / canvas.width : 0;

  const beginDrag = (e: React.PointerEvent, obj: ThumbnailSnapshotObject) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    onSelect(obj.id);
    dragRef.current = {
      id: obj.id,
      startX: e.clientX,
      startY: e.clientY,
      origX: obj.x ?? 0,
      origY: obj.y ?? 0,
    };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const moveDrag = (e: React.PointerEvent) => {
    if (!dragRef.current || scale <= 0) return;
    const dx = (e.clientX - dragRef.current.startX) / scale;
    const dy = (e.clientY - dragRef.current.startY) / scale;
    onMove(
      dragRef.current.id,
      round(dragRef.current.origX + dx),
      round(dragRef.current.origY + dy),
    );
  };

  const endDrag = () => {
    dragRef.current = null;
  };

  const renderObject = (obj: ThumbnailSnapshotObject) => {
    const box = objectBox(obj);
    const selected = obj.id === selectedId;
    const hidden = obj.visible === false;
    const style: React.CSSProperties = {
      position: "absolute",
      left: obj.x ?? 0,
      top: obj.y ?? 0,
      width: box.width,
      height: box.height,
      transform: `rotate(${obj.rotation ?? 0}deg)`,
      transformOrigin: "center",
      opacity: hidden ? 0.25 : 1,
      touchAction: "none",
      cursor: "move",
    };

    const content = (() => {
      switch (obj.type) {
        case "rect":
          return (
            <div
              className="h-full w-full"
              style={{ backgroundColor: obj.fill ?? "#000000", borderRadius: obj.radius ?? 0 }}
            />
          );
        case "text":
          return (
            <div
              className="h-full w-full overflow-hidden whitespace-pre-wrap"
              style={{
                color: obj.fill ?? "#ffffff",
                fontFamily: obj.font_family ?? "Inter",
                fontSize: obj.font_size ?? 48,
                fontWeight: obj.font_weight ?? 400,
                textAlign: (obj.text_align as React.CSSProperties["textAlign"]) ?? "left",
                lineHeight: 1.1,
              }}
            >
              {obj.text ?? ""}
            </div>
          );
        case "image": {
          const url = obj.media_id ? mediaUrls.get(obj.media_id) : undefined;
          return url ? (
            <img src={url} alt="" draggable={false} className="h-full w-full object-fill" />
          ) : (
            <div className="flex h-full w-full items-center justify-center bg-white/[0.06]">
              <ImageIcon size={20} className="text-white/30" />
            </div>
          );
        }
        default:
          return (
            <div className="flex h-full w-full items-center justify-center border border-dashed border-white/30 text-[11px] text-white/50">
              {obj.type}
            </div>
          );
      }
    })();

    return (
      <div
        key={obj.id}
        data-testid="canvas-object"
        data-object-type={obj.type}
        data-selected={selected || undefined}
        onPointerDown={(e) => beginDrag(e, obj)}
        onPointerMove={moveDrag}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        className={cn(
          "rounded-[2px]",
          selected &&
            "outline outline-2 outline-sky-400 outline-offset-2 ring-2 ring-sky-400/30",
          !selected && "hover:outline hover:outline-1 hover:outline-white/40",
        )}
        style={style}
        title={`${obj.type} — ${obj.id}`}
      >
        {content}
      </div>
    );
  };

  return (
    <div
      ref={wrapperRef}
      data-testid="canvas-stage"
      className="w-full overflow-hidden rounded-2xl border border-white/[0.10] bg-[#0b0b12]"
      style={{ height: scale > 0 ? canvas.height * scale : undefined }}
      onPointerDown={(e) => {
        // Clicking empty canvas (the scaled surface, not an object)
        // clears the selection. `closest` handles both the stage and the
        // surface child so the visible canvas area deselects correctly.
        if (!(e.target as HTMLElement).closest('[data-testid="canvas-object"]')) {
          onSelect(null);
        }
      }}
    >
      {scale > 0 && (
        <div
          data-testid="canvas-surface"
          style={{
            width: canvas.width,
            height: canvas.height,
            transform: `scale(${scale})`,
            transformOrigin: "top left",
            backgroundColor: canvas.background,
          }}
        >
          {objects.map(renderObject)}
        </div>
      )}
    </div>
  );
}

// ─── Layers panel ──────────────────────────────────────────────────

interface LayersPanelProps {
  objects: ThumbnailSnapshotObject[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onReorder: (id: string, direction: -1 | 1) => void;
}

function LayersPanel({ objects, selectedId, onSelect, onReorder }: LayersPanelProps) {
  // Render order = array order, last = top; show top-first.
  const ordered = [...objects].reverse();
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
        <Layers size={14} className="text-white/40" />
        Livelli
        <span className="text-[11px] font-medium text-[#9aa0aa]">{objects.length}</span>
      </h2>
      {objects.length === 0 ? (
        <p className="mt-3 text-[12px] text-[#9aa0aa]">
          Canvas vuoto — aggiungi un oggetto dalla barra degli strumenti.
        </p>
      ) : (
        <ul className="mt-3 space-y-1.5" data-testid="layers-list">
          {ordered.map((obj, index) => {
            const topIndex = objects.length - 1 - index;
            return (
              <li
                key={obj.id}
                data-testid="layer-row"
                className={cn(
                  "flex items-center gap-2 rounded-lg border px-2.5 py-2 transition-colors",
                  selectedId === obj.id
                    ? "border-sky-400/30 bg-sky-500/[0.08]"
                    : "border-white/[0.06] bg-white/[0.02] hover:bg-white/[0.04]",
                )}
              >
                <button
                  type="button"
                  onClick={() => onSelect(obj.id)}
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                >
                  <span className="text-[11px] font-bold text-[#9aa0aa]">{topIndex + 1}</span>
                  <span className="truncate text-[13px] font-medium text-white">
                    {obj.type === "text"
                      ? (obj.text ?? "Testo").slice(0, 24)
                      : obj.type === "image"
                        ? `Immagine (${obj.media_id?.slice(0, 8)}…)`
                        : "Rettangolo"}
                  </span>
                  {obj.visible === false && <EyeOff size={12} className="shrink-0 text-[#9aa0aa]" />}
                </button>
                <div className="flex shrink-0 items-center gap-0.5">
                  <button
                    type="button"
                    aria-label="Porta avanti"
                    disabled={topIndex >= objects.length - 1}
                    onClick={() => onReorder(obj.id, 1)}
                    className="rounded-md p-1 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] disabled:opacity-30"
                  >
                    <ChevronUp size={14} />
                  </button>
                  <button
                    type="button"
                    aria-label="Porta indietro"
                    disabled={topIndex <= 0}
                    onClick={() => onReorder(obj.id, -1)}
                    className="rounded-md p-1 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] disabled:opacity-30"
                  >
                    <ChevronDown size={14} />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// ─── Inspector ─────────────────────────────────────────────────────

interface InspectorProps {
  object: ThumbnailSnapshotObject;
  onPatch: (patch: Partial<ThumbnailSnapshotObject>) => void;
  onDuplicate: () => void;
  onDelete: () => void;
  onReplaceImage: () => void;
}

function Inspector({
  object,
  onPatch,
  onDuplicate,
  onDelete,
  onReplaceImage,
}: InspectorProps) {
  const numberInput = (
    key: keyof ThumbnailSnapshotObject,
    label: string,
    step = 1,
  ) => (
    <label className="block">
      <span className="text-[11px] font-semibold text-[#9aa0aa]">{label}</span>
      <input
        type="number"
        step={step}
        value={Number(object[key] ?? 0)}
        onChange={(e) => {
          const value = Number(e.target.value);
          if (Number.isFinite(value)) onPatch({ [key]: value } as Partial<ThumbnailSnapshotObject>);
        }}
        className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
      />
    </label>
  );

  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-[13px] font-bold text-white capitalize">
          {object.type === "image" ? "Immagine" : object.type === "text" ? "Testo" : "Rettangolo"}
        </h2>
        <div className="flex items-center gap-1">
          <button
            type="button"
            aria-label="Duplica oggetto"
            onClick={onDuplicate}
            className="rounded-md p-1.5 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <Copy size={14} />
          </button>
          <button
            type="button"
            aria-label="Elimina oggetto"
            onClick={onDelete}
            className="rounded-md p-1.5 text-[#9aa0aa] hover:text-red-400 hover:bg-red-500/[0.08] transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      </div>

      <div className="mt-3 space-y-3">
        <div className="grid grid-cols-2 gap-2">
          {numberInput("x", "X")}
          {numberInput("y", "Y")}
          {numberInput("width", "Larghezza")}
          {numberInput("height", "Altezza")}
          {numberInput("scale_x", "Scala X", 0.1)}
          {numberInput("scale_y", "Scala Y", 0.1)}
          {numberInput("rotation", "Rotazione °")}
        </div>

        {(object.type === "text" || object.type === "rect") && (
          <div>
            <span className="text-[11px] font-semibold text-[#9aa0aa]">Colore</span>
            <div className="mt-1 flex items-center gap-2">
              <input
                type="color"
                aria-label="Colore oggetto"
                value={typeof object.fill === "string" && object.fill.startsWith("#") ? object.fill : "#000000"}
                onChange={(e) => onPatch({ fill: e.target.value })}
                className="h-8 w-10 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
              />
              <input
                type="text"
                aria-label="Colore esadecimale"
                value={object.fill ?? ""}
                onChange={(e) => onPatch({ fill: e.target.value })}
                className="flex-1 px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[12px] text-white font-mono focus:outline-none focus:border-white/[0.20]"
              />
            </div>
          </div>
        )}

        {object.type === "rect" && (
          <label className="block">
            <span className="text-[11px] font-semibold text-[#9aa0aa]">Angolo arrotondato</span>
            <input
              type="number"
              min={0}
              value={object.radius ?? 0}
              onChange={(e) => {
                const value = Number(e.target.value);
                if (Number.isFinite(value) && value >= 0) onPatch({ radius: value });
              }}
              className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
            />
          </label>
        )}

        {object.type === "text" && (
          <>
            <label className="block">
              <span className="text-[11px] font-semibold text-[#9aa0aa]">Testo</span>
              <textarea
                rows={3}
                value={object.text ?? ""}
                onChange={(e) => onPatch({ text: e.target.value })}
                className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white resize-y focus:outline-none focus:border-white/[0.20]"
              />
            </label>
            <div className="grid grid-cols-2 gap-2">
              {numberInput("font_size", "Dimensione testo")}
              <label className="block">
                <span className="text-[11px] font-semibold text-[#9aa0aa]">Peso</span>
                <select
                  value={Number(object.font_weight ?? 400)}
                  onChange={(e) => onPatch({ font_weight: Number(e.target.value) })}
                  className="mt-1 w-full px-2 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none"
                >
                  {[400, 500, 600, 700, 800, 900].map((w) => (
                    <option key={w} value={w} className="bg-[#1f1f2e]">
                      {w}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          </>
        )}

        {object.type === "image" && (
          <button
            type="button"
            onClick={onReplaceImage}
            className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            <ImageIcon size={14} />
            Sostituisci immagine
          </button>
        )}

        <label className="flex items-center gap-2 text-[13px] text-[#9aa0aa]">
          <input
            type="checkbox"
            checked={object.visible !== false}
            onChange={(e) => onPatch({ visible: e.target.checked })}
            className="accent-white"
          />
          <Eye size={13} />
          Visibile
        </label>
      </div>
    </div>
  );
}

// ─── Revision history (compact, from the old read-only detail) ─────

interface RevisionPanelProps {
  revisions: ThumbnailProjectRevision[];
  currentRevisionId: string | null | undefined;
}

function RevisionPanel({ revisions, currentRevisionId }: RevisionPanelProps) {
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
        <Layers size={14} className="text-white/40" />
        Revisioni immutabili
        <span className="text-[11px] font-medium text-[#9aa0aa]">{revisions.length}</span>
      </h2>
      {revisions.length === 0 ? (
        <p className="mt-3 text-[12px] text-[#9aa0aa]">Nessuna revisione ancora.</p>
      ) : (
        <ul className="mt-3 space-y-1.5" data-testid="revisions-list">
          {revisions.slice(0, 8).map((revision) => (
            <li
              key={revision.id}
              className={cn(
                "flex items-center justify-between gap-2 rounded-lg border px-2.5 py-2 text-[12px]",
                revision.id === currentRevisionId
                  ? "border-emerald-400/20 bg-emerald-500/[0.06]"
                  : "border-white/[0.06] bg-white/[0.02]",
              )}
            >
              <span className="font-semibold text-white">#{revision.revision_number}</span>
              <span className="truncate text-[#9aa0aa]">
                {revision.renderer_version}
                {revision.id === currentRevisionId ? " · corrente" : ""}
              </span>
              <span className="shrink-0 text-[#9aa0aa]" title={revision.snapshot_sha256}>
                {revision.snapshot_sha256.slice(0, 8)}…
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

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

  const patchSnapshot = (next: EditorSnapshot) => {
    setSnapshot(next);
  };

  const updateObject = (id: string, patch: Partial<ThumbnailSnapshotObject>) => {
    if (!snapshot) return;
    patchSnapshot({
      ...snapshot,
      objects: snapshot.objects.map((obj) => (obj.id === id ? { ...obj, ...patch } : obj)),
    });
  };

  const addObject = (obj: ThumbnailSnapshotObject) => {
    if (!snapshot) return;
    patchSnapshot({ ...snapshot, objects: [...snapshot.objects, obj] });
    setSelectedId(obj.id);
  };

  const removeSelected = () => {
    if (!snapshot || !selectedId) return;
    patchSnapshot({ ...snapshot, objects: snapshot.objects.filter((o) => o.id !== selectedId) });
    setSelectedId(null);
  };

  const duplicateSelected = () => {
    if (!snapshot || !selectedId) return;
    const source = snapshot.objects.find((o) => o.id === selectedId);
    if (!source) return;
    const copy: ThumbnailSnapshotObject = {
      ...source,
      id: makeId(source.type),
      x: (source.x ?? 0) + 24,
      y: (source.y ?? 0) + 24,
    };
    addObject(copy);
  };

  const reorder = (id: string, direction: -1 | 1) => {
    if (!snapshot) return;
    const index = snapshot.objects.findIndex((o) => o.id === id);
    const target = index + direction;
    if (index < 0 || target < 0 || target >= snapshot.objects.length) return;
    const objects = [...snapshot.objects];
    const [obj] = objects.splice(index, 1);
    objects.splice(target, 0, obj!);
    patchSnapshot({ ...snapshot, objects });
  };

  const setBackground = (background: string) => {
    if (!snapshot) return;
    patchSnapshot({ ...snapshot, canvas: { ...snapshot.canvas, background } });
  };

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

        {/* Header */}
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={handleCloseEditor}
              className="inline-flex items-center gap-1.5 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline"
            >
              <ArrowLeft size={14} /> Copertine
            </button>
            <div className="h-5 w-px bg-white/[0.08]" />
            <div>
              <h1 className="text-[20px] font-extrabold tracking-[-0.02em] text-white leading-tight">
                {project.name}
              </h1>
              <code className="flex items-center gap-1 text-[11px] text-[#9aa0aa]">
                <Hash size={10} />
                {project.id}
              </code>
            </div>
          </div>
          <div className="flex items-center gap-3">
            <SaveIndicator
              status={autosave.status}
              lastSavedAt={autosave.lastSavedAt}
              error={autosave.error}
              lastHash={autosave.lastHash}
              onRetry={autosave.retry}
            />
            <button
              type="button"
              onClick={handleManualSave}
              disabled={isManualSaving || autosave.conflict !== null}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {isManualSaving ? (
                <Loader2 size={14} className="animate-spin" />
              ) : (
                <Save size={14} />
              )}
              Salva progetto
            </button>
            <span className="hidden sm:inline text-[12px] text-[#9aa0aa]">
              {snapshot?.canvas.width}×{snapshot?.canvas.height}
            </span>
          </div>
        </div>

        {/* Conflict dialog — NEVER silent last-write-wins on the canvas */}
        {autosave.conflict && (
          <div
            data-testid="conflict-banner"
            role="alertdialog"
            aria-label="Conflitto di versione"
            className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-red-400/25 bg-red-500/[0.08] px-4 py-3"
          >
            <p className="flex items-center gap-2 text-[13px] text-red-200">
              <AlertTriangle size={15} />
              Modifiche non salvate: il progetto è stato modificato in un'altra scheda
              {autosave.conflict.current_version !== undefined
                ? ` (versione attuale ${autosave.conflict.current_version})`
                : ""}.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => void handleReload()}
                className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
              >
                <RefreshCw size={14} />
                Ricarica versione recente
              </button>
              <button
                type="button"
                onClick={() => void handleSaveAsCopy()}
                disabled={isSavingCopy}
                className="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-[13px] font-semibold text-black hover:bg-white/90 transition-colors disabled:opacity-50"
              >
                {isSavingCopy ? <Loader2 size={14} className="animate-spin" /> : <CopyPlus size={14} />}
                Salva come copia
              </button>
            </div>
          </div>
        )}

        {/* Toolbar */}
        <div className="mt-5 flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => addObject(newTextObject())}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            <Type size={14} /> Testo
          </button>
          <button
            type="button"
            onClick={() => addObject(newRectObject())}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            <Square size={14} /> Rettangolo
          </button>
          <button
            type="button"
            onClick={() => setShowMediaPicker(true)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            <ImageIcon size={14} /> Immagine
          </button>
          <div className="ml-auto text-[12px] text-[#9aa0aa]">
            Trascina gli oggetti per spostarli · seleziona per modificarli
          </div>
        </div>

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
              <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
                <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
                  <Link2 size={14} className="text-white/40" />
                  Collegamenti YouTube
                  <span className="text-[11px] font-medium text-[#9aa0aa]">{assignments.length}</span>
                </h2>
                {assignments.length === 0 ? (
                  <p className="mt-3 text-[12px] text-[#9aa0aa]">
                    Nessun collegamento — la copertina esiste in modo autonomo.
                  </p>
                ) : (
                  <ul className="mt-3 space-y-1.5">
                    {assignments.map((assignment) => (
                      <li
                        key={assignment.id}
                        className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-2.5 py-2 text-[12px]"
                      >
                        <span className="font-medium text-white">{assignment.youtube_video_id}</span>
                        <span className="ml-2 text-[#9aa0aa]">account #{assignment.platform_account_id}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          </div>

          <div className="space-y-4">
            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
              <h2 className="text-[13px] font-bold text-white">Canvas</h2>
              <div className="mt-3 space-y-3">
                <div>
                  <span className="text-[11px] font-semibold text-[#9aa0aa]">Sfondo</span>
                  <div className="mt-1 flex items-center gap-2">
                    <input
                      type="color"
                      aria-label="Sfondo canvas"
                      value={snapshot?.canvas.background.startsWith("#") ? snapshot.canvas.background : "#000000"}
                      onChange={(e) => setBackground(e.target.value)}
                      className="h-8 w-10 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
                    />
                    <input
                      type="text"
                      aria-label="Sfondo esadecimale"
                      value={snapshot?.canvas.background ?? ""}
                      onChange={(e) => setBackground(e.target.value)}
                      className="flex-1 px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[12px] text-white font-mono focus:outline-none focus:border-white/[0.20]"
                    />
                  </div>
                </div>
                <p className="text-[11px] text-[#9aa0aa]">
                  Dimensione fissa {snapshot?.canvas.width}×{snapshot?.canvas.height} — cambia dal
                  progetto o crea una nuova copertina.
                </p>
              </div>
            </div>

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
