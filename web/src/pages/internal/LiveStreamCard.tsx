import { memo, useEffect, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Copy,
  Edit3,
  ExternalLink,
  Loader2,
  MoreHorizontal,
  Radio,
  RotateCcw,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { cn } from "../../lib/utils";
import type { LivestreamRow } from "./livestreamsTypes";
import {
  durationLabel,
  healthOf,
  isLiveNow,
  playbackModeLabel,
  privacyLabel,
  scheduleLabel,
  stateLabel,
  stateTone,
  toneClasses,
} from "./livestreamsVisual";

/**
 * Secondary actions. The encoder/broadcast controls (restart, stop,
 * duplicate, YouTube view) require the livestream worker, which has not
 * landed yet — they render disabled with an honest tooltip instead of
 * pretending to work. "Elimina" is real: it calls DELETE
 * /api/v1/livestreams/{id} behind a two-step confirmation.
 */
const SECONDARY_ACTIONS = [
  { label: "Modifica metadati", icon: Edit3, hint: "Disponibile con il wizard di modifica" },
  { label: "Visualizza su YouTube", icon: ExternalLink, hint: "Disponibile dopo la creazione del broadcast" },
  { label: "Riavvia encoder", icon: RotateCcw, hint: "Disponibile con il worker live" },
  { label: "Duplica configurazione", icon: Copy, hint: "Disponibile con il worker live" },
  { label: "Termina live", icon: Square, hint: "Disponibile con il worker live" },
] as const;

export const LiveStreamCard = memo(function LiveStreamCard({
  livestream,
  deleting,
  onDelete,
}: {
  livestream: LivestreamRow;
  deleting: boolean;
  onDelete: (id: string) => Promise<boolean>;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const live = isLiveNow(livestream);
  const health = healthOf(livestream);
  const channel = livestream.channel_name || `Canale #${livestream.platform_account_id}`;

  useEffect(() => {
    if (!menuOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [menuOpen]);

  const requestDelete = async () => {
    const ok = await onDelete(livestream.id);
    if (ok) setConfirmDelete(false);
  };

  return (
    <article
      data-testid="livestream-card"
      className={cn(
        "overflow-hidden rounded-2xl border bg-white/[0.025] transition-colors",
        live ? "border-violet-400/25 hover:border-violet-400/40" : "border-white/[0.08] hover:border-white/[0.16]",
      )}
    >
      <div className="flex min-h-[128px]">
        {/* Cover — the broadcast thumbnail is not exposed by the API yet,
            so a state-tinted gradient stands in for it. */}
        <div
          className={cn(
            "relative flex w-[136px] shrink-0 items-center justify-center bg-gradient-to-br",
            live
              ? "from-violet-500/40 via-indigo-500/25 to-blue-500/30"
              : "from-white/[0.10] via-white/[0.05] to-white/[0.02]",
          )}
        >
          <Radio size={30} className={live ? "text-violet-200" : "text-white/25"} aria-hidden="true" />
          {live && (
            <span className="absolute left-2 top-2 inline-flex items-center gap-1 rounded-md bg-red-500 px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-white">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-white" aria-hidden="true" />
              Live
            </span>
          )}
          <span className="absolute bottom-2 left-2 rounded-md bg-black/50 px-1.5 py-0.5 font-mono text-[9px] text-white/80">
            {livestream.resolution}
          </span>
        </div>

        <div className="flex min-w-0 flex-1 flex-col p-3.5">
          <div className="flex min-w-0 items-start justify-between gap-2">
            <div className="min-w-0">
              <p className="truncate text-[15px] font-semibold leading-tight text-white" title={livestream.title}>
                {livestream.title || "Live senza titolo"}
              </p>
              <p className="mt-1 truncate text-[11px] text-[#9aa0aa]" title={channel}>
                {channel}
              </p>
            </div>

            {/* Secondary actions menu */}
            <div className="relative shrink-0">
              <button
                type="button"
                onClick={() => setMenuOpen((open) => !open)}
                aria-label="Altre azioni"
                aria-expanded={menuOpen}
                className="grid h-8 w-8 place-items-center rounded-lg text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white"
              >
                <MoreHorizontal size={15} aria-hidden="true" />
              </button>
              {menuOpen && (
                <>
                  <button
                    type="button"
                    aria-label="Chiudi menu"
                    className="fixed inset-0 z-10 cursor-default"
                    onClick={() => setMenuOpen(false)}
                  />
                  <div
                    role="menu"
                    aria-label="Azioni live"
                    className="absolute right-0 top-9 z-20 w-56 rounded-xl border border-white/[0.10] bg-[#171722] p-1.5 shadow-[0_16px_48px_rgba(0,0,0,0.5)]"
                  >
                    {SECONDARY_ACTIONS.map((action) => (
                      <button
                        key={action.label}
                        type="button"
                        role="menuitem"
                        disabled
                        title={action.hint}
                        className="flex w-full cursor-not-allowed items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[12px] font-medium text-[#6b7280]"
                      >
                        <action.icon size={14} aria-hidden="true" />
                        {action.label}
                        <span className="ml-auto text-[9px] uppercase tracking-wide text-[#4b5160]">Presto</span>
                      </button>
                    ))}
                    <div className="my-1 h-px bg-white/[0.08]" />
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => {
                        setMenuOpen(false);
                        setConfirmDelete(true);
                      }}
                      className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[12px] font-semibold text-red-300 hover:bg-red-500/[0.12]"
                    >
                      <Trash2 size={14} aria-hidden="true" />
                      Elimina
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>

          <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
            <span
              className={cn("inline-flex h-6 items-center rounded-lg border px-2 text-[10px] font-bold uppercase tracking-wide", toneClasses[stateTone(livestream.actual_state)])}
            >
              {stateLabel(livestream.actual_state)}
            </span>
            <span className="inline-flex h-6 items-center rounded-lg border border-white/[0.08] bg-white/[0.04] px-2 text-[10px] font-medium text-[#cdd2da]" title="Privacy">
              {privacyLabel(livestream.privacy_status)}
            </span>
            <span className="inline-flex h-6 items-center rounded-lg border border-blue-500/20 bg-blue-500/[0.08] px-2 text-[10px] font-medium text-blue-200" title="Modalità di riproduzione">
              {playbackModeLabel(livestream.playback_mode)}
            </span>
            <span className="inline-flex h-6 items-center rounded-lg border border-white/[0.08] bg-white/[0.04] px-2 text-[10px] font-medium text-[#9aa0aa]" title="Avvio / programmazione">
              {scheduleLabel(livestream)}
            </span>
          </div>

          <div className="mt-auto flex items-center justify-between gap-2 pt-3">
            <div className="flex items-center gap-3 text-[11px] text-[#9aa0aa]">
              <span className="inline-flex items-center gap-1.5" title="Salute dello stream">
                <span className={cn("h-2 w-2 rounded-full", {
                  "bg-emerald-400": health.tone === "success",
                  "bg-amber-400": health.tone === "warning",
                  "bg-blue-400": health.tone === "info",
                  "bg-red-400": health.tone === "danger",
                  "bg-white/30": health.tone === "neutral",
                })} aria-hidden="true" />
                <span className={cn("font-medium", health.tone === "danger" && "text-red-300", health.tone === "warning" && "text-amber-200", health.tone === "success" && "text-emerald-300")}>
                  Salute: {health.label}
                </span>
              </span>
              <span className="text-white/25" aria-hidden="true">·</span>
              <span title="Durata — disponibile con il worker live">Durata: {durationLabel(livestream)}</span>
            </div>

            {livestream.auto_restart && (
              <span
                className="inline-flex items-center gap-1 rounded-md border border-emerald-500/20 bg-emerald-500/[0.08] px-1.5 py-0.5 text-[9px] font-semibold text-emerald-300"
                title="Riavvio automatico in caso di errore"
              >
                <RotateCcw size={9} aria-hidden="true" /> Auto-riavvio
              </span>
            )}
          </div>
        </div>
      </div>

      {confirmDelete && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-red-500/25 bg-red-500/[0.07] px-3.5 py-2.5" role="alert" data-testid="livestream-delete-confirm">
          <span className="inline-flex items-center gap-1.5 text-[11px] font-medium text-red-200">
            <AlertTriangle size={13} aria-hidden="true" />
            Eliminare la configurazione? L'azione è irreversibile.
          </span>
          <span className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={() => void requestDelete()}
              disabled={deleting}
              className="inline-flex items-center gap-1.5 rounded-lg bg-red-500 px-2.5 py-1.5 text-[11px] font-bold text-white hover:bg-red-400 disabled:cursor-wait disabled:opacity-60"
              data-testid="livestream-delete-confirm-button"
            >
              {deleting ? <Loader2 size={12} className="animate-spin" aria-hidden="true" /> : <CheckCircle2 size={12} aria-hidden="true" />}
              {deleting ? "Eliminazione…" : "Conferma eliminazione"}
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(false)}
              disabled={deleting}
              className="inline-flex items-center gap-1 rounded-lg border border-white/[0.10] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] hover:bg-white/[0.06] disabled:opacity-50"
            >
              <X size={12} aria-hidden="true" /> Annulla
            </button>
          </span>
        </div>
      )}
    </article>
  );
});
