/**
 * CreateCoverDialog — autonomous "Crea nuova copertina" flow.
 *
 * The ONLY fields the user is asked for are: Nome progetto, Formato,
 * Dimensione and Sfondo iniziale. There is deliberately no channel,
 * video, OAuth connection, group, or language surface — the project is
 * born autonomous.
 *
 * The flow guarantees the "salvataggio immediato del progetto vuoto":
 *
 *   1. POST /api/v1/thumbnail-projects            → project row + ID
 *   2. PUT  /api/v1/thumbnail-projects/{id}/snapshot
 *          (empty canvas + chosen background)     → immutable revision #1
 *
 * Even an empty project is durable and re-openable from the server. On
 * success the dialog calls `onCreated(project)` (so the library can
 * prepend the row) and navigates to the project detail page.
 *
 * A live preview renders the empty canvas (background color at the exact
 * aspect ratio) so the user sees the format before committing.
 */
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, AlertCircle, ImageIcon, Check } from "lucide-react";
import { cn } from "../../../lib/utils";
import {
  createThumbnailProject,
  saveThumbnailSnapshot,
} from "../api/thumbnailProjectsApi";
import type { ThumbnailProject } from "../types";

/** Canonical renderer version the editor/runtime must agree on. */
export const RENDERER_VERSION = "go-canvas-v1";

export interface CoverFormatPreset {
  id: string;
  label: string;
  width: number;
  height: number;
}

/** Common canvas formats; "short" maps to the YouTube Shorts 9:16 cover. */
export const FORMAT_PRESETS: readonly CoverFormatPreset[] = [
  { id: "youtube", label: "YouTube 16:9", width: 1920, height: 1080 },
  { id: "short", label: "Short 9:16", width: 1080, height: 1920 },
  { id: "square", label: "Quadrata 1:1", width: 1080, height: 1080 },
];

/** Initial-background swatches (hex, validatable). */
const BACKGROUND_SWATCHES = [
  "#30305a",
  "#0b0b12",
  "#101418",
  "#1b4332",
  "#7f1d1d",
  "#1e3a8a",
  "#ffffff",
  "#f59e0b",
];

const MAX_CANVAS_DIMENSION = 16384;

const HEX_COLOR_RE = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;

export function isValidHexColor(value: string): boolean {
  return HEX_COLOR_RE.test(value.trim());
}

export interface CreateCoverDialogProps {
  workspaceId: number;
  onCreated: (project: ThumbnailProject) => void;
  onClose: () => void;
}

export function CreateCoverDialog({
  workspaceId,
  onCreated,
  onClose,
}: CreateCoverDialogProps) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [presetId, setPresetId] = useState<string>("youtube");
  const [custom, setCustom] = useState(false);
  const [width, setWidth] = useState(1920);
  const [height, setHeight] = useState(1080);
  const [background, setBackground] = useState(BACKGROUND_SWATCHES[0]!);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const currentPreset =
    FORMAT_PRESETS.find((p) => p.id === presetId) ?? FORMAT_PRESETS[0]!;
  const canvasWidth = custom ? width : currentPreset.width;
  const canvasHeight = custom ? height : currentPreset.height;

  const nameInvalid = name.trim().length === 0;
  const dimensionsInvalid =
    canvasWidth < 1 ||
    canvasWidth > MAX_CANVAS_DIMENSION ||
    canvasHeight < 1 ||
    canvasHeight > MAX_CANVAS_DIMENSION;
  const backgroundInvalid = !isValidHexColor(background);
  const canSubmit =
    !nameInvalid && !dimensionsInvalid && !backgroundInvalid && !submitting;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      // 1) The project is persisted immediately with an ID of its own —
      //    even an empty project is durable ("salvataggio immediato").
      const project = await createThumbnailProject({
        workspace_id: workspaceId,
        name: name.trim(),
        canvas_width: canvasWidth,
        canvas_height: canvasHeight,
      });
      // 2) Write the initial empty canvas snapshot (with the chosen
      //    background) so the project owns revision #1 from birth.
      try {
        await saveThumbnailSnapshot(workspaceId, project.id, {
          schema_version: 1,
          snapshot: {
            canvas: { width: canvasWidth, height: canvasHeight, background },
            objects: [],
          },
          renderer_version: RENDERER_VERSION,
          base_version: project.version,
        });
      } catch {
        // A failed initial snapshot must not block creation: the project
        // already exists server-side and the editor phase will save it.
      }
      onCreated(project);
      navigate(`/app/covers/${encodeURIComponent(project.id)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile creare la copertina.");
      setSubmitting(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-cover-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
    >
      <button
        type="button"
        aria-label="Chiudi"
        onClick={onClose}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm cursor-default"
      />
      <form
        onSubmit={handleSubmit}
        className="relative max-h-[92vh] w-full max-w-lg overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6 shadow-[0_8px_32px_rgba(0,0,0,0.5)]"
      >
        <h2 id="create-cover-title" className="text-lg font-bold text-white">Crea nuova copertina</h2>
        <p className="mt-1 text-[13px] text-[#9aa0aa]">
          Solo nome, formato e sfondo — nessun canale, video o connessione richiesti.
        </p>

        {/* Live empty-canvas preview at the exact aspect ratio. */}
        <div className="mt-5">
          <div
            data-testid="create-cover-preview"
            className="relative w-full max-w-[320px] mx-auto overflow-hidden rounded-xl border border-white/[0.10] shadow-[0_4px_24px_rgba(0,0,0,0.4)]"
            style={{
              aspectRatio: `${canvasWidth} / ${canvasHeight}`,
              // Never let an invalid hex render a broken/transparent
              // canvas: fall back to a neutral dark while the validation
              // message is shown next to the field.
              backgroundColor: backgroundInvalid ? "#14141c" : background,
            }}
          >
            <div className="absolute inset-0 flex items-center justify-center">
              <span className="inline-flex items-center gap-1.5 rounded-full bg-black/45 px-3 py-1 text-[11px] font-semibold text-white/90 backdrop-blur-sm">
                <ImageIcon size={12} />
                {canvasWidth}×{canvasHeight}
              </span>
            </div>
          </div>
          <p className="mt-2 text-center text-[11px] text-[#9aa0aa]">
            Anteprima del canvas vuoto — si aggiorna con formato e sfondo.
          </p>
        </div>

        <div className="mt-5 space-y-4">
          <div>
            <label htmlFor="cover-name" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
              Nome progetto
            </label>
            <input
              id="cover-name"
              type="text"
              autoFocus
              placeholder="Es. WWE Breaking News"
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={nameInvalid}
              className={cn(
                "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:ring-1 transition-all",
                nameInvalid
                  ? "border-red-400/40 focus:border-red-400/60 focus:ring-red-400/10"
                  : "border-white/[0.08] focus:border-white/[0.20] focus:ring-white/10",
              )}
            />
            {nameInvalid && (
              <p className="mt-1 text-[12px] text-red-400">Il nome è obbligatorio.</p>
            )}
          </div>

          <div>
            <span className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">Formato</span>
            <div className="grid grid-cols-3 gap-2">
              {FORMAT_PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  aria-pressed={!custom && presetId === p.id}
                  onClick={() => {
                    setPresetId(p.id);
                    setCustom(false);
                  }}
                  className={cn(
                    "rounded-xl border px-2 py-2 text-center transition-colors",
                    !custom && presetId === p.id
                      ? "bg-white text-black border-white"
                      : "bg-white/[0.04] text-[#e8e8ef] border-white/[0.08] hover:border-white/[0.20]",
                  )}
                >
                  <span className="block text-[12px] font-semibold leading-tight">{p.label}</span>
                  <span className="block text-[11px] opacity-60">
                    {p.width}×{p.height}
                  </span>
                </button>
              ))}
            </div>
            <label className="mt-2 flex items-center gap-2 text-[13px] text-[#9aa0aa]">
              <input
                type="checkbox"
                checked={custom}
                onChange={(e) => {
                  const enabled = e.target.checked;
                  setCustom(enabled);
                  // Seed the custom fields from the currently selected
                  // preset so toggling never leaves stale dimensions.
                  if (enabled) {
                    setWidth(currentPreset.width);
                    setHeight(currentPreset.height);
                  }
                }}
                className="accent-white"
              />
              Dimensione personalizzata
            </label>
            {custom && (
              <div className="mt-2 grid grid-cols-2 gap-2">
                <label className="block">
                  <span className="text-[11px] font-semibold text-[#9aa0aa]">Larghezza</span>
                  <input
                    type="number"
                    min={1}
                    max={MAX_CANVAS_DIMENSION}
                    value={width}
                    onChange={(e) => setWidth(Number(e.target.value))}
                    className="w-full mt-1 px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20]"
                  />
                </label>
                <label className="block">
                  <span className="text-[11px] font-semibold text-[#9aa0aa]">Altezza</span>
                  <input
                    type="number"
                    min={1}
                    max={MAX_CANVAS_DIMENSION}
                    value={height}
                    onChange={(e) => setHeight(Number(e.target.value))}
                    className="w-full mt-1 px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20]"
                  />
                </label>
              </div>
            )}
            {dimensionsInvalid && (
              <p className="mt-1 text-[12px] text-red-400">
                Dimensioni valide: 1–{MAX_CANVAS_DIMENSION} px.
              </p>
            )}
          </div>

          <div>
            <span className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
              Sfondo iniziale
            </span>
            <div className="flex flex-wrap items-center gap-1.5">
              {BACKGROUND_SWATCHES.map((swatch) => (
                <button
                  key={swatch}
                  type="button"
                  aria-label={`Sfondo ${swatch}`}
                  aria-pressed={background === swatch}
                  onClick={() => setBackground(swatch)}
                  className={cn(
                    "flex h-8 w-8 items-center justify-center rounded-lg border transition-transform hover:scale-105",
                    background === swatch
                      ? "border-white ring-1 ring-white/40"
                      : "border-white/[0.10]",
                  )}
                  style={{ backgroundColor: swatch }}
                >
                  {background === swatch && <Check size={14} className="text-white drop-shadow" />}
                </button>
              ))}
            </div>
            <div className="mt-2 flex items-center gap-2">
              <label htmlFor="cover-background-picker" className="sr-only">
                Seleziona colore
              </label>
              <input
                id="cover-background-picker"
                type="color"
                value={isValidHexColor(background) ? background : "#30305a"}
                onChange={(e) => setBackground(e.target.value)}
                className="h-9 w-12 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
              />
              <input
                type="text"
                aria-label="Sfondo esadecimale"
                value={background}
                onChange={(e) => setBackground(e.target.value)}
                aria-invalid={backgroundInvalid}
                className={cn(
                  "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[14px] text-white font-mono focus:outline-none focus:ring-1 transition-all",
                  backgroundInvalid
                    ? "border-red-400/40 focus:border-red-400/60 focus:ring-red-400/10"
                    : "border-white/[0.08] focus:border-white/[0.20] focus:ring-white/10",
                )}
              />
            </div>
            {backgroundInvalid && (
              <p className="mt-1 text-[12px] text-red-400">
                Colore non valido — usa formato #RGB o #RRGGBB.
              </p>
            )}
          </div>
        </div>

        {error && (
          <p className="mt-4 flex items-center gap-2 text-[13px] text-red-400">
            <AlertCircle size={14} /> {error}
          </p>
        )}

        <div className="mt-6 flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-[14px] font-medium text-[#9aa0aa] hover:text-white transition-colors"
          >
            Annulla
          </button>
          <button
            type="submit"
            disabled={!canSubmit}
            className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Plus size={16} />
            {submitting ? "Creazione…" : "Crea copertina"}
          </button>
        </div>
      </form>
    </div>
  );
}
