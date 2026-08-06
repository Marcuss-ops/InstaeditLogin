import { memo, useEffect, useState } from "react";
import { CheckCircle2, Clapperboard, Film, Loader2, Lock } from "lucide-react";
import { cn } from "../../lib/utils";
import { useMediaDetail } from "./useMediaLibrary";
import type { MediaLibraryItem } from "./livestreamsTypes";
import {
  compatibilityMeta,
  formatMediaAudio,
  formatMediaDuration,
  formatMediaFps,
  formatMediaResolution,
  toneClasses,
} from "./livestreamsVisual";

export interface MediaLibraryCardProps {
  item: MediaLibraryItem;
  order: number | null;
  eager?: boolean;
  onToggle: (item: MediaLibraryItem) => void;
  onPreview: (id: string) => void;
  previewActive: boolean;
}

/**
 * A memoized list row. Only the first viewport-sized batch is eager; the
 * rest starts its detail request when IntersectionObserver reports that it
 * is near the viewport. The detail query is shared and cached for 5 minutes.
 */
export const MediaLibraryCard = memo(function MediaLibraryCard({
  item,
  order,
  eager = false,
  onToggle,
  onPreview,
  previewActive,
}: MediaLibraryCardProps) {
  const [node, setNode] = useState<HTMLLIElement | null>(null);
  const [visible, setVisible] = useState(eager);
  const detail = useMediaDetail(item.id, visible);

  useEffect(() => {
    if (eager || visible || !node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "240px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [eager, node, visible]);

  const full = detail.data;
  const compat = compatibilityMeta(item.live_compatibility);
  const selectable = item.live_compatibility === "ready";

  return (
    <li ref={setNode}>
      <button
        type="button"
        onClick={() => onToggle(item)}
        disabled={!selectable}
        aria-pressed={order != null}
        aria-label={`${selectable ? "Seleziona" : "Non selezionabile"}: ${item.filename}`}
        className={cn(
          "group w-full overflow-hidden rounded-2xl border text-left transition-all",
          selectable
            ? "border-white/[0.08] bg-white/[0.025] hover:border-violet-400/40 hover:bg-white/[0.045]"
            : "cursor-not-allowed border-white/[0.06] bg-white/[0.015] opacity-70",
          order != null && "border-violet-400/50 bg-violet-500/[0.06]",
        )}
        data-testid={`ls-step3-item-${item.id}`}
      >
        <div className="relative aspect-video w-full overflow-hidden bg-black/40">
          {full?.preview_url ? (
            <video
              src={full.preview_url}
              preload="metadata"
              muted
              playsInline
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                onPreview(item.id);
              }}
              className="h-full w-full object-contain"
              data-testid={`ls-step3-preview-${item.id}`}
            />
          ) : detail.isFetching ? (
            <div className="flex h-full w-full items-center justify-center">
              <Loader2 size={22} className="animate-spin text-white/30" aria-hidden="true" />
            </div>
          ) : (
            <div className="flex h-full w-full items-center justify-center">
              <Clapperboard size={26} className="text-white/20" aria-hidden="true" />
            </div>
          )}
          {!previewActive && full?.preview_url && (
            <span className="absolute bottom-1.5 right-1.5 rounded-md bg-black/60 px-1.5 py-0.5 text-[9px] font-medium text-white/60 backdrop-blur">
              clicca per anteprima
            </span>
          )}
        </div>
        <div className="p-3.5">
          <div className="flex items-start justify-between gap-2">
            <p className="min-w-0 truncate text-[13px] font-semibold text-white" title={item.filename}>
              {item.filename}
            </p>
            {order != null ? (
              <span
                className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-violet-500 text-[12px] font-bold text-white"
                aria-label={`Posizione ${order}`}
                data-testid={`ls-step3-order-${item.id}`}
              >
                {order}
              </span>
            ) : selectable ? (
              <CheckCircle2 size={17} className="shrink-0 text-white/20 transition-colors group-hover:text-violet-300" aria-hidden="true" />
            ) : null}
          </div>
          <dl className="mt-2.5 grid grid-cols-2 gap-x-3 gap-y-1.5 text-[11px]">
            <MetaRow label="Durata" value={formatMediaDuration(full?.duration_seconds)} />
            <MetaRow label="Risoluzione" value={formatMediaResolution(full?.width, full?.height)} />
            <MetaRow label="FPS" value={formatMediaFps(full?.fps)} />
            <MetaRow label="Audio" value={formatMediaAudio(full?.has_audio)} />
          </dl>
          <div className="mt-3 flex items-center justify-between gap-2">
            <span className={cn("inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] font-bold", toneClasses[compat.tone])} title={compat.hint} data-testid={`ls-step3-compat-${item.id}`}>
              {item.live_compatibility === "ready" ? <CheckCircle2 size={10} aria-hidden="true" /> : item.live_compatibility === "needs_normalization" ? <Lock size={10} aria-hidden="true" /> : <Film size={10} aria-hidden="true" />}
              {compat.label}
            </span>
            {!selectable && <span className="text-[10px] text-[#5c6473]">Bloccato — da normalizzare</span>}
          </div>
        </div>
      </button>
    </li>
  );
});

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <dt className="text-[#5c6473]">{label}</dt>
      <dd className="truncate font-medium text-[#cdd2da]">{value}</dd>
    </div>
  );
}
