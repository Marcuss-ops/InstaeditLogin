/**
 * ExportPanel — "Genera copertina" flow for the Dark Editor.
 *
 * The canonical export pipeline (server-side, never a second browser
 * canvas):
 *
 *   snapshot salvato (flushPendingAutosave) → POST …/render →
 *   renderer canonico → PNG su MinIO (Media Library) → export ready
 *
 * The parent is responsible for awaiting the autosave flush BEFORE
 * calling onGenerate: the panel never acts on a stale revision (DoD
 * "preview/export derivi SEMPRE dall'ultima modifica"). The rendered
 * preview is the server-authoritative file — the media resolver mints
 * a presigned GET URL for the export's media_id.
 */
import {
  AlertTriangle,
  BadgeCheck,
  Download,
  ImageIcon,
  Link2,
  Loader2,
  RefreshCw,
  ShieldCheck,
  Wand2,
} from "lucide-react";
import { cn } from "../../../lib/utils";
import type { ThumbnailExport } from "../types";

/** Export UI state machine (rendering → ready | failed). */
export type ExportUiState =
  | { kind: "idle" }
  | { kind: "rendering" }
  | { kind: "ready"; export: ThumbnailExport }
  | { kind: "failed"; message: string };

export interface ExportPanelProps {
  state: ExportUiState;
  /** Presigned URL of the last ready export (resolved by the parent). */
  previewUrl: string | null;
  onGenerate: () => void;
  onLink: () => void;
  /** Latest persisted revision (server truth) to prove preview/export
   *  derive from the same snapshot — the export is "stantia" when its
   *  revision_id differs. */
  latestRevisionId?: string | null;
  /** Canvas size to verify the export matches the project dimensions. */
  canvasWidth?: number;
  canvasHeight?: number;
  /** Canonical renderer lineage the editor and server share. */
  rendererVersion?: string;
}

export function ExportPanel({
  state,
  previewUrl,
  onGenerate,
  onLink,
  latestRevisionId,
  canvasWidth,
  canvasHeight,
  rendererVersion,
}: ExportPanelProps) {
  const isBusy = state.kind === "rendering";
  const latestExport = state.kind === "ready" ? state.export : null;

  const sameRevision =
    latestExport !== null &&
    latestRevisionId != null &&
    latestExport.revision_id === latestRevisionId;
  const dimensionsMatch =
    latestExport !== null &&
    canvasWidth !== undefined &&
    canvasHeight !== undefined &&
    latestExport.width === canvasWidth &&
    latestExport.height === canvasHeight;
  const rendererMatches =
    latestExport !== null &&
    rendererVersion !== undefined &&
    latestExport.renderer_version === rendererVersion;

  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
          <Wand2 size={14} className="text-white/40" />
          Genera copertina
        </h2>
        <button
          type="button"
          onClick={onGenerate}
          disabled={isBusy}
          className="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-[13px] font-semibold text-black hover:bg-white/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isBusy ? <Loader2 size={14} className="animate-spin" /> : <Wand2 size={14} />}
          {isBusy ? "Generazione…" : latestExport ? "Genera di nuovo" : "Genera copertina"}
        </button>
      </div>

      <p className="mt-2 text-[12px] leading-relaxed text-[#9aa0aa]">
        Il file è renderizzato dal server sull'ultimo snapshot salvato e conservato nella Media
        Library — nessun canale, video o connessione richiesti.
      </p>

      {state.kind === "failed" && (
        <div className="mt-3 flex items-start gap-2 rounded-lg border border-red-400/25 bg-red-500/[0.08] px-3 py-2 text-[12px] text-red-200">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span>{state.message}</span>
        </div>
      )}

      {latestExport ? (
        <div className="mt-4 space-y-3">
          <div className="overflow-hidden rounded-xl border border-white/[0.10] bg-black/40">
            {previewUrl ? (
              <img
                src={previewUrl}
                alt="Anteprima copertina renderizzata"
                data-testid="export-preview"
                className="w-full"
              />
            ) : (
              <div className="flex aspect-video w-full items-center justify-center">
                <ImageIcon size={24} className="text-white/25" />
              </div>
            )}
          </div>
          {/* Same-origin proof: preview and export must derive from the
              SAME persisted snapshot — never a stale revision. */}
          <div
            data-testid="export-origin-check"
            className="flex flex-wrap items-center gap-2 text-[11px]"
          >
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-semibold",
                sameRevision
                  ? "bg-emerald-500/[0.12] text-emerald-300"
                  : latestRevisionId
                    ? "bg-amber-500/[0.12] text-amber-300"
                    : "bg-white/[0.06] text-[#9aa0aa]",
              )}
            >
              {sameRevision ? <ShieldCheck size={11} /> : <AlertTriangle size={11} />}
              {sameRevision
                ? "Stessa revisione dell'ultimo snapshot"
                : latestRevisionId
                  ? "Export da revisione stantia — rigenera dopo il salvataggio"
                  : "Origine revisione non verificabile"}
            </span>
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-semibold",
                dimensionsMatch
                  ? "bg-emerald-500/[0.12] text-emerald-300"
                  : "bg-white/[0.06] text-[#9aa0aa]",
              )}
            >
              {dimensionsMatch ? <BadgeCheck size={11} /> : <AlertTriangle size={11} />}
              {dimensionsMatch
                ? `${latestExport.width}×${latestExport.height} identiche al canvas`
                : "Dimensioni non confrontabili"}
            </span>
          </div>
          {/* Verifiable export metadata: id, media_id, status, sha256. */}
          <div className="grid gap-1.5 text-[11px] text-[#9aa0aa]">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <code
                className="rounded bg-white/[0.05] px-1.5 py-0.5 text-white/90"
                data-testid="export-id"
              >
                {latestExport.id}
              </code>
              <span title={`media ${latestExport.media_id}`}>
                media <code className="text-white/80">{latestExport.media_id.slice(0, 12)}…</code>
              </span>
              <span>·</span>
              <span title={latestExport.sha256}>
                sha256 <code className="text-white/80">{latestExport.sha256.slice(0, 12)}…</code>
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span
                data-testid="export-status"
                className={cn(
                  "inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-semibold",
                  latestExport.status === "ready"
                    ? "bg-emerald-500/[0.12] text-emerald-300"
                    : latestExport.status === "failed"
                      ? "bg-red-500/[0.12] text-red-300"
                      : "bg-white/[0.06] text-[#9aa0aa]",
                )}
              >
                {latestExport.status === "ready" ? (
                  <BadgeCheck size={11} />
                ) : (
                  <Loader2 size={11} className="animate-spin" />
                )}
                {latestExport.status === "ready"
                  ? "pronto"
                  : latestExport.status === "failed"
                    ? "fallito"
                    : "rendering"}
              </span>
              <span>revisione <code className="text-white/80">{latestExport.revision_id}</code></span>
              <span>·</span>
              <span>
                {latestExport.width}×{latestExport.height}
              </span>
              <span>·</span>
              <span>{latestExport.content_type}</span>
              <span>·</span>
              <span title={latestExport.renderer_version}>
                renderer{" "}
                <code className={rendererMatches ? "text-white/80" : "text-amber-300"}>
                  {latestExport.renderer_version}
                </code>
              </span>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <a
              href={previewUrl ?? undefined}
              download={`${latestExport.project_id}.${latestExport.content_type === "image/jpeg" ? "jpeg" : "png"}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors",
                !previewUrl && "pointer-events-none opacity-40",
              )}
            >
              <Download size={14} /> Scarica {latestExport.content_type === "image/jpeg" ? "JPEG" : "PNG"}
            </a>
            <button
              type="button"
              onClick={onLink}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <Link2 size={14} /> Collega a un video
            </button>
          </div>
        </div>
      ) : (
        <p className="mt-4 flex items-center gap-2 rounded-lg border border-dashed border-white/[0.10] bg-white/[0.02] px-3 py-4 text-center text-[12px] text-[#9aa0aa]">
          {state.kind === "rendering" ? (
            <>
              <Loader2 size={14} className="animate-spin text-sky-300" /> Rendering dell'ultimo
              snapshot…
            </>
          ) : (
            <>
              <RefreshCw size={14} /> Nessun export ancora — premi "Genera copertina".
            </>
          )}
        </p>
      )}
    </div>
  );
}
