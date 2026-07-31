/**
 * VideoUploadStep — Step 1 of the /app/content/new wizard.
 *
 * Responsibilities:
 *   - File picker for a single video
 *   - Internal title input ("titolo interno") — goes into
 *     postsApi.createPost later as `content.title`
 *   - Preview: file name + readable size
 *   - Upload status: progress bar + phase label
 *   - Error handling:
 *       • non-video file → inline reject (NEVER calls mediaApi)
 *       • ApiError(status, message) → state-bound error card
 *       • AuthError → redirect to /login (caller owned)
 *       • network/type-error → state-bound error card with retry
 *
 * The component is purely controlled by `useUploadMedia`'s state
 * machine. It does not keep its own copy of the file or progress —
 * the hook is the source of truth. A `key={inputKey}` trick on
 * the file input is used to force-clear the browser's "selected
 * file" state when the user hits "Seleziona un altro file" or
 * after a successful reset.
 *
 * On successful transition to `done`, fires `onComplete(asset.id,
 * title)` and the wizard container advances to Step 2.
 */
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Film, RefreshCw, Upload, AlertCircle } from "lucide-react";
import { cn } from "../../../lib/utils";
import { AuthError } from "../../../lib/auth";
import {
  UPLOAD_PHASE_PERCENT,
  useUploadMedia,
} from "../hooks/useUploadMedia";
import type { MediaAsset, UploadPhase } from "../api/mediaApi";

export interface VideoUploadStepProps {
  /** Pre-fill the internal title (e.g. resumed session draft). */
  initialTitle?: string;
  /** Fires once the asset is `ready` and locked into wizard state. */
  onComplete: (asset: MediaAsset, internalTitle: string) => void;
}

/** Human-readable byte size string (KB / MB / GB). */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

type LabeledPhase = "hashing" | UploadPhase;
const PHASE_LABEL: Record<LabeledPhase, string> = {
  hashing: "Hashing del file…",
  presign: "Acquisendo URL firmato…",
  upload: "Caricamento su storage…",
  complete: "Finalizzazione…",
};

const ACCEPT = "video/mp4,video/quicktime,video/webm,.mp4,.mov,.m4v,.webm,.mkv,.avi";

export function VideoUploadStep({ initialTitle, onComplete }: VideoUploadStepProps) {
  const navigate = useNavigate();
  const { state, start, reset } = useUploadMedia();
  const [title, setTitle] = useState(initialTitle ?? "");
  const [file, setFile] = useState<File | null>(null);
  // inputKey bumps to force-clear the native file input when the
  // user picks a different file or after a manual reset.
  const [inputKey, setInputKey] = useState(0);
  const lastFireRef = useRef<string | null>(null);

  const isBusy =
    state.kind === "hashing" || state.kind === "uploading";
  const canUpload = !!file && title.trim().length > 0 && !isBusy;

  // Fire onComplete once and only once per asset id. We don't want
  // to re-fire on re-render (which would re-advance the parent even
  // when state.kind was already 'done' in a previous render pass).
  useEffect(() => {
    if (state.kind !== "done") return;
    if (lastFireRef.current === state.asset.id) return;
    lastFireRef.current = state.asset.id;
    onComplete(state.asset, title.trim());
  }, [state, title, onComplete]);

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>): void => {
    const next = e.target.files?.[0];
    if (!next) {
      setFile(null);
      return;
    }
    if (!isVideoFile(next)) {
      // Inline reject — don't call mediaApi. Surface through state
      // by hitting an ephemeral local error message; the user can
      // pick a different file without losing the title they typed.
      setFile(null);
      window.alert(
        `Solo file video sono supportati (ricevuto: ${next.type || "sconosciuto"}).`,
      );
      return;
    }
    setFile(next);
    // Reset any prior hook state — the user is changing files.
    reset();
    lastFireRef.current = null;
  };

  const handleUpload = async (): Promise<void> => {
    if (!file) return;
    try {
      await start(file);
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      // ApiError / TypeError paths are handled inside the hook
      // (`state.kind === "error"`). Re-throwing here would
      // double-surface; just no-op.
    }
  };

  const handleReset = (): void => {
    setFile(null);
    setInputKey((k) => k + 1);
    lastFireRef.current = null;
    reset();
  };

  const percent =
    state.kind === "hashing"
      ? UPLOAD_PHASE_PERCENT.hashing
      : state.kind === "uploading"
        ? UPLOAD_PHASE_PERCENT[state.phase]
        : state.kind === "done"
          ? 100
          : 0;

  return (
    <div
      className="rounded-2xl border border-white/[0.08] bg-[#0d0d14]/80 backdrop-blur p-6 md:p-8 shadow-[0_0_0_1px_rgba(255,255,255,0.02)]"
      data-testid="video-upload-step"
    >
      <h2 className="text-xl font-semibold text-white mb-1">
        Step 1 — Video
      </h2>
      <p className="text-sm text-[#9aa0aa] mb-6">
        Seleziona un file video dal tuo computer. Il file verrà
        caricato sui nostri storage e referenziato come
        <code className="px-1 mx-1 rounded bg-white/[0.06] text-[#cdd2da] font-mono text-xs">
          media_asset_id
        </code>
        nei passi successivi.
      </p>

      {/* File picker — disabled while uploading */}
      <label
        className={cn(
          "block rounded-xl border-2 border-dashed transition-colors p-6 text-center cursor-pointer",
          isBusy
            ? "border-white/[0.06] bg-white/[0.02] opacity-60 cursor-not-allowed"
            : "border-white/[0.16] hover:border-white/40 hover:bg-white/[0.03]",
        )}
      >
        <input
          key={inputKey}
          type="file"
          accept={ACCEPT}
          onChange={handleFileChange}
          disabled={isBusy}
          className="sr-only"
          data-testid="video-file-input"
          aria-label="Seleziona un file video"
        />
        {file ? (
          <div className="flex items-center gap-4 text-left">
            <div className="w-12 h-12 rounded-xl bg-emerald-500/10 border border-emerald-500/30 flex items-center justify-center shrink-0">
              <Film size={22} className="text-emerald-300" aria-hidden="true" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-white font-medium truncate" data-testid="file-name">
                {file.name}
              </div>
              <div className="text-xs text-[#9aa0aa] mt-0.5" data-testid="file-size">
                {formatSize(file.size)} · {file.type || "tipo sconosciuto"}
              </div>
            </div>
          </div>
        ) : (
          <div>
            <Upload
              size={28}
              className="mx-auto text-[#9aa0aa] mb-2"
              aria-hidden="true"
            />
            <div className="text-white font-medium mb-1">
              Trascina o seleziona un video
            </div>
            <div className="text-xs text-[#9aa0aa]">
              MP4, MOV, M4V, WebM, MKV, AVI · fino a ~5 GB consigliati
            </div>
          </div>
        )}
      </label>

      {/* Internal title input */}
      <div className="mt-6">
        <label
          htmlFor="internal-title"
          className="block text-sm font-medium text-[#cdd2da] mb-1.5"
        >
          Titolo interno
        </label>
        <input
          id="internal-title"
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          disabled={isBusy}
          placeholder="es. Promo estate 2026 — v2"
          className={cn(
            "w-full rounded-xl bg-[#0a0a10] border border-white/[0.12] px-3.5 py-2.5 text-white placeholder:text-[#5c6473] focus:outline-none focus:ring-2 focus:ring-white/30 focus:border-white/30 transition-colors",
            isBusy && "opacity-60 cursor-not-allowed",
          )}
          data-testid="internal-title-input"
        />
        <p className="text-xs text-[#5c6473] mt-1.5">
          Sarà visibile solo a te e referenziato nel post. Potrai
          modificare il titolo YouTube più tardi.
        </p>
      </div>

      {/* Progress + phase label */}
      {(state.kind === "hashing" || state.kind === "uploading") && (
        <div className="mt-6" data-testid="upload-progress">
          <div className="flex items-center justify-between text-sm mb-1.5">
            <span className="text-[#cdd2da]">
              {PHASE_LABEL[state.kind === "hashing" ? "hashing" : state.phase]}
            </span>
            <span className="text-[#9aa0aa] tabular-nums">{percent}%</span>
          </div>
          <div
            role="progressbar"
            aria-valuenow={percent}
            aria-valuemin={0}
            aria-valuemax={100}
            className="h-1.5 rounded-full bg-white/[0.06] overflow-hidden"
          >
            <div
              className="h-full bg-emerald-400/80 transition-[width] duration-200 ease-out"
              style={{ width: `${percent}%` }}
            />
          </div>
        </div>
      )}

      {/* Success — animation strip + reset link */}
      {state.kind === "done" && (
        <div
          className="mt-6 rounded-xl border border-emerald-500/30 bg-emerald-500/[0.06] px-4 py-3"
          data-testid="upload-success"
        >
          <div className="text-sm text-emerald-200 font-medium">
            Asset pronto · proseguendo allo Step 2…
          </div>
          <div className="text-xs text-[#9aa0aa] mt-0.5 font-mono break-all">
            asset_id: {state.asset.id}
          </div>
        </div>
      )}

      {/* Error card */}
      {state.kind === "error" && (
        <div
          className="mt-6 rounded-xl border border-red-500/30 bg-red-500/[0.06] px-4 py-3 flex items-start gap-3"
          data-testid="upload-error"
          role="alert"
        >
          <AlertCircle
            size={18}
            className="text-red-300 shrink-0 mt-0.5"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <div className="text-sm text-red-200 font-medium">
              Upload non riuscito
            </div>
            <div className="text-sm text-red-200/80 mt-0.5 break-words">
              {state.message}
            </div>
            <button
              type="button"
              onClick={void handleUpload}
              disabled={!file}
              className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-red-200 hover:text-red-100 underline underline-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
              data-testid="upload-retry"
            >
              Riprova
            </button>
          </div>
        </div>
      )}

      {/* Action row */}
      <div className="mt-6 flex items-center justify-end gap-3">
        <button
          type="button"
          onClick={handleReset}
          disabled={isBusy || (!file && state.kind === "idle")}
          className="inline-flex items-center gap-1.5 text-sm text-[#9aa0aa] hover:text-white disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          data-testid="reset-file"
        >
          <RefreshCw size={14} aria-hidden="true" />
          Seleziona un altro file
        </button>
        <button
          type="button"
          onClick={void handleUpload}
          disabled={!canUpload}
          className="inline-flex items-center gap-2 rounded-xl bg-white text-[#030308] px-4 py-2.5 text-sm font-semibold hover:bg-[#e8ecf2] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          data-testid="upload-submit"
        >
          <Upload size={16} aria-hidden="true" />
          {state.kind === "idle" || state.kind === "error"
            ? "Carica video"
            : "Caricamento…"}
        </button>
      </div>
    </div>
  );
}

/** Local file-type gate mirrored inside the hook (UX-first check). */
function isVideoFile(file: File): boolean {
  if (file.type.startsWith("video/")) return true;
  if (!file.name) return false;
  return /\.(mp4|mov|m4v|webm|mkv|avi)$/i.test(file.name);
}
