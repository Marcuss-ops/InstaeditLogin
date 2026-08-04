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
import { AlertTriangle, Download, ImageIcon, Link2, Loader2, RefreshCw, Wand2 } from "lucide-react";
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
}

export function ExportPanel({ state, previewUrl, onGenerate, onLink }: ExportPanelProps) {
  const isBusy = state.kind === "rendering";
  const latestExport = state.kind === "ready" ? state.export : null;

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
          <div className="flex flex-wrap items-center gap-2 text-[11px] text-[#9aa0aa]">
            <code className="rounded bg-white/[0.05] px-1.5 py-0.5">{latestExport.id}</code>
            <span>
              {latestExport.width}×{latestExport.height}
            </span>
            <span>·</span>
            <span>{latestExport.content_type}</span>
            <span>·</span>
            <span title={latestExport.sha256}>
              sha256 {latestExport.sha256.slice(0, 12)}…
            </span>
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
