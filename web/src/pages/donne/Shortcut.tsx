import { CheckCircle2 } from "lucide-react";
import { SHORTCUT } from "./content";

/**
 * Sezione "La Scorciatoia" — la soluzione in 4 punti chiave.
 * Contro-altare del problema: stesso layout, segno opposto.
 */
export function Shortcut() {
  return (
    <section id="scorciatoia" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-pink-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-rose-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-pink-300/90 mb-3">{SHORTCUT.eyebrow}</div>
          <h2 className="text-display-2 text-white">{SHORTCUT.title}</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[62ch]">{SHORTCUT.subtitle}</p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {SHORTCUT.items.map((item, i) => (
            <div
              key={item.title}
              className={`surface-card p-6 animate-fade-up hover:border-emerald-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-7 h-7 shrink-0 items-center justify-center rounded-lg bg-emerald-500/15 ring-1 ring-emerald-400/30 text-emerald-300">
                  <CheckCircle2 className="w-4 h-4" />
                </span>
                <div>
                  <h3 className="text-display-3 text-white mb-1.5">{item.title}</h3>
                  <p className="text-sm text-zinc-400 leading-relaxed">{item.description}</p>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
