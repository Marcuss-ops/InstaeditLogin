/**
 * Live streaming control center (placeholder).
 *
 * This page is the entry point for the livestream module. It is a
 * deliberately minimal placeholder: the full dashboard (summary cards,
 * tabs, per-live cards) lands with the module implementation. Keeping
 * a real route here means the sidebar item never dead-ends.
 */
export function LiveStreamsPage() {
  return (
    <div className="mx-auto max-w-7xl p-8">
      <header className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white">Live streaming</h1>
          <p className="mt-1 text-sm text-[#9aa0aa]">
            Gestisci live attive, programmate e trasmissioni 24/7.
          </p>
        </div>
        <button
          type="button"
          disabled
          title="La creazione live arriva con il modulo livestream"
          className="inline-flex cursor-not-allowed items-center gap-2 rounded-xl bg-white/[0.06] px-4 py-2 text-sm font-semibold text-[#6b7280]"
        >
          Crea nuova live
        </button>
      </header>

      <div className="mt-8 rounded-2xl border border-white/[0.08] bg-white/[0.03] p-12 text-center">
        <div className="text-4xl" aria-hidden="true">
          📡
        </div>
        <h2 className="mt-4 text-lg font-semibold text-white">Nessuna live configurata</h2>
        <p className="mx-auto mt-2 max-w-md text-sm text-[#9aa0aa]">
          Trasmetti un video o una playlist preregistrata direttamente dal tuo server. Il modulo
          livestream è in arrivo.
        </p>
      </div>
    </div>
  );
}
