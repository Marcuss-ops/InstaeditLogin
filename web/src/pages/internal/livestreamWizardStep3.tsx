import { useCallback, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Film,
  Loader2,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { useMediaLibrary } from "./useMediaLibrary";
import type { MediaLibraryItem } from "./livestreamsTypes";
import { MediaLibraryCard } from "./MediaLibraryCard";
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
  const { state, reload, loadMore, loadDetail } = useMediaLibrary();
  const [previewId, setPreviewId] = useState<string | null>(null);
  const canContinue = selectedIds.length > 0;
  const toggle = useCallback((item: MediaLibraryItem) => {
    if (item.live_compatibility !== "ready") return;
    if (selectedIds.includes(item.id)) {
      onSelectionChange(selectedIds.filter((id) => id !== item.id));
    } else {
      onSelectionChange([...selectedIds, item.id]);
    }
  }, [onSelectionChange, selectedIds]);
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
        <>
        <ul className="grid grid-cols-1 gap-3 md:grid-cols-2" data-testid="ls-step3-list">
          {state.items.map((item, index) => (
            <MediaLibraryCard
              key={item.id}
              item={item}
              order={orderOf(item.id)}
              onToggle={toggle}
              onPreview={setPreviewId}
              previewActive={previewId === item.id}
              detail={state.details[item.id]}
              detailLoading={!state.details[item.id] && index < 4}
              onVisible={loadDetail}
            />
          ))}
        </ul>
        {state.hasMore && (
          <div className="flex flex-col items-center gap-2 pt-1">
            {state.loadMoreError && <p className="text-[12px] text-red-300">{state.loadMoreError}</p>}
            <button
              type="button"
              onClick={() => void loadMore()}
              disabled={state.loadingMore}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.12] bg-white/[0.05] px-3.5 py-2 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.1] disabled:opacity-50"
              data-testid="ls-step3-load-more"
            >
              {state.loadingMore && <Loader2 size={13} className="animate-spin" aria-hidden="true" />}
              {state.loadingMore ? "Caricamento…" : "Carica altri asset"}
            </button>
          </div>
        )}
        </>
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
