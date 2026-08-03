import { useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Clapperboard,
  Film,
  Loader2,
  Lock,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { useMediaLibrary } from "./useMediaLibrary";
import type { MediaLibraryItem } from "./livestreamsTypes";
import {
  compatibilityMeta,
  formatMediaAudio,
  formatMediaDuration,
  formatMediaFps,
  formatMediaResolution,
  toneClasses,
} from "./livestreamsVisual";

/**
 * Wizard step 3 of 5 (Contenuti) — Media Library picker.
 *
 * Lists the caller's ready assets with their ffprobe metadata
 * (duration / resolution / FPS / audio) and a server-derived
 * compatibility badge. Only assets flagged `ready` (canonical
 * 1080p30 / 720p30 profile with audio) can be selected: everything
 * else is blocked with a "Da normalizzare" note — normalization lands
 * with the media-processing release, until then off-profile files
 * cannot feed the live encoder.
 *
 * Selection is ORDERED (numbered): clicking appends at the end,
 * deselecting removes the gap — the order is the future playlist
 * order lifted to the page via `selectedIds`.
 */
export function LiveStreamWizardStep3({
  selectedIds,
  onSelectionChange,
  onBack,
  onContinue,
}: {
  selectedIds: string[];
  onSelectionChange: (ids: string[]) => void;
  onBack: () => void;
  onContinue: () => void;
}) {
  const { state, reload } = useMediaLibrary();
  const [previewId, setPreviewId] = useState<string | null>(null);

  const canContinue = selectedIds.length > 0;

  const toggle = (item: MediaLibraryItem) => {
    if (item.live_compatibility !== "ready") return;
    if (selectedIds.includes(item.id)) {
      onSelectionChange(selectedIds.filter((id) => id !== item.id));
    } else {
      onSelectionChange([...selectedIds, item.id]);
    }
  };

  const orderOf = (id: string) => {
    const index = selectedIds.indexOf(id);
    return index >= 0 ? index + 1 : null;
  };

  const compatibleCount = state.kind === "ready" ? state.items.filter((i) => i.live_compatibility === "ready").length : 0;

  return (
    <section className="mt-8 space-y-6" data-testid="livestream-new-step3">
      {/* Header + selection summary */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-[17px] font-bold text-white">Contenuti — Media Library</h2>
          <p className="mt-1 text-[13px] leading-relaxed text-[#9aa0aa]">
            Scegli i video che andranno in onda. I file fuori profilo ({compatibleCount === 0 ? "nessun video compatibile" : "da normalizzare"}) non
            sono selezionabili finché la normalizzazione non sarà disponibile.
          </p>
        </div>
        {selectedIds.length > 0 && (
          <span
            className="inline-flex shrink-0 items-center gap-1.5 self-start rounded-lg border border-emerald-500/25 bg-emerald-500/[0.08] px-2.5 py-1.5 text-[11px] font-semibold text-emerald-300"
            data-testid="ls-step3-count"
          >
            <CheckCircle2 size={12} aria-hidden="true" />
            {selectedIds.length} {selectedIds.length === 1 ? "video selezionato" : "video selezionati"}
          </span>
        )}
      </div>

      {state.kind === "loading" && (
        <div className="flex h-48 flex-col items-center justify-center gap-3 rounded-2xl border border-white/[0.08] bg-white/[0.02]">
          <Loader2 size={22} className="animate-spin text-violet-300" aria-hidden="true" />
          <p className="text-[13px] text-[#9aa0aa]">Caricamento della Media Library…</p>
        </div>
      )}

      {state.kind === "error" && (
        <div className="flex h-48 flex-col items-center justify-center gap-3 rounded-2xl border border-red-500/20 bg-red-500/[0.04] p-6 text-center">
          <TriangleAlert size={22} className="text-red-300" aria-hidden="true" />
          <p className="text-[13px] text-[#e8e8ef]">{state.message}</p>
          <button
            type="button"
            onClick={() => void reload()}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.12] bg-white/[0.05] px-3 py-1.5 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.1]"
            data-testid="ls-step3-retry"
          >
            <RefreshCw size={13} aria-hidden="true" />
            Riprova
          </button>
        </div>
      )}

      {state.kind === "ready" && state.items.length === 0 && (
        <div className="flex h-56 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-white/[0.12] bg-white/[0.02] p-8 text-center">
          <Film size={26} className="text-white/25" aria-hidden="true" />
          <p className="text-[15px] font-semibold text-white">Nessun video nella Media Library</p>
          <p className="max-w-sm text-[12px] leading-relaxed text-[#9aa0aa]">
            Carica prima i video (pagina Caricamenti o import da Drive/Velox): dopo l&apos;analisi
            automatica appariranno qui con il loro stato di compatibilità.
          </p>
        </div>
      )}

      {state.kind === "ready" && state.items.length > 0 && (
        <ul className="grid grid-cols-1 gap-3 md:grid-cols-2" data-testid="ls-step3-list">
          {state.items.map((item) => {
            const compat = compatibilityMeta(item.live_compatibility);
            const selectable = item.live_compatibility === "ready";
            const order = orderOf(item.id);
            return (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() => toggle(item)}
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
                  <MediaThumb item={item} previewActive={previewId === item.id} onPreview={() => setPreviewId(item.id)} />
                  <div className="p-3.5">
                    <div className="flex items-start justify-between gap-2">
                      <p className="min-w-0 truncate text-[13px] font-semibold text-white" title={item.filename}>
                        {item.filename}
                      </p>
                      {order != null && (
                        <span
                          className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-violet-500 text-[12px] font-bold text-white"
                          aria-label={`Posizione ${order}`}
                          data-testid={`ls-step3-order-${item.id}`}
                        >
                          {order}
                        </span>
                      )}
                      {selectable && order == null && (
                        <CheckCircle2
                          size={17}
                          className="shrink-0 text-white/20 transition-colors group-hover:text-violet-300"
                          aria-hidden="true"
                        />
                      )}
                    </div>
                    <dl className="mt-2.5 grid grid-cols-2 gap-x-3 gap-y-1.5 text-[11px]">
                      <MetaRow label="Durata" value={formatMediaDuration(item.duration_seconds)} />
                      <MetaRow label="Risoluzione" value={formatMediaResolution(item.width, item.height)} />
                      <MetaRow label="FPS" value={formatMediaFps(item.fps)} />
                      <MetaRow label="Audio" value={formatMediaAudio(item.has_audio)} />
                    </dl>
                    <div className="mt-3 flex items-center justify-between gap-2">
                      <span
                        className={cn(
                          "inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] font-bold",
                          toneClasses[compat.tone],
                        )}
                        title={compat.hint}
                        data-testid={`ls-step3-compat-${item.id}`}
                      >
                        {item.live_compatibility === "ready" ? (
                          <CheckCircle2 size={10} aria-hidden="true" />
                        ) : item.live_compatibility === "needs_normalization" ? (
                          <Lock size={10} aria-hidden="true" />
                        ) : (
                          <Film size={10} aria-hidden="true" />
                        )}
                        {compat.label}
                      </span>
                      {!selectable && (
                        <span className="text-[10px] text-[#5c6473]">Bloccato — da normalizzare</span>
                      )}
                    </div>
                  </div>
                </button>
              </li>
            );
          })}
        </ul>
      )}

      {/* Action bar */}
      <div className="sticky bottom-4 flex flex-col gap-3 rounded-2xl border border-white/[0.10] bg-[#14141e]/95 p-4 shadow-[0_-8px_40px_rgba(0,0,0,0.5)] backdrop-blur sm:flex-row sm:items-center sm:justify-between">
        <p className="min-w-0 text-[12px] text-[#9aa0aa]" data-testid="livestream-new-step3-hint">
          {selectedIds.length > 0
            ? `${selectedIds.length} ${selectedIds.length === 1 ? "video pronto" : "video pronti"} · l'ordine di selezione è l'ordine di riproduzione.`
            : "Seleziona almeno un video compatibile per continuare."}
        </p>
        <div className="flex shrink-0 items-center gap-2">
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] px-3.5 py-2 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.06]"
            data-testid="livestream-new-step3-back"
          >
            <ArrowLeft size={14} aria-hidden="true" />
            Indietro
          </button>
          <button
            type="button"
            onClick={onContinue}
            disabled={!canContinue}
            title={canContinue ? undefined : "Seleziona almeno un video compatibile"}
            className="inline-flex items-center gap-2 rounded-lg bg-violet-500 px-4 py-2 text-[12px] font-bold text-white transition-colors hover:bg-violet-400 disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-[#6b7280]"
            data-testid="livestream-new-continue"
          >
            Continua
            <ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </section>
  );
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <dt className="text-[#5c6473]">{label}</dt>
      <dd className="truncate font-medium text-[#cdd2da]">{value}</dd>
    </div>
  );
}

function MediaThumb({
  item,
  previewActive,
  onPreview,
}: {
  item: MediaLibraryItem;
  previewActive: boolean;
  onPreview: () => void;
}) {
  return (
    <div className="relative aspect-video w-full overflow-hidden bg-black/40">
      {item.preview_url ? (
        <video
          src={item.preview_url}
          preload="metadata"
          muted
          playsInline
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            onPreview();
          }}
          className="h-full w-full object-contain"
          data-testid={`ls-step3-preview-${item.id}`}
        />
      ) : (
        <div className="flex h-full w-full items-center justify-center">
          <Clapperboard size={26} className="text-white/20" aria-hidden="true" />
        </div>
      )}
      {!previewActive && (
        <span className="absolute bottom-1.5 right-1.5 rounded-md bg-black/60 px-1.5 py-0.5 text-[9px] font-medium text-white/60 backdrop-blur">
          clicca per anteprima
        </span>
      )}
    </div>
  );
}
