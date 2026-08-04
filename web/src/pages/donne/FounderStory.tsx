import { Sparkles } from "lucide-react";
import { FOUNDER } from "./content";

/**
 * Sezione "Dalla Fondatrice" — la storia di come è nato HerChannel AI.
 * Posizionata prima delle FAQ, come nella landing principale.
 */
export function FounderStory() {
  return (
    <section id="fondatrice" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-4xl px-6">
        <div className="animate-fade-up text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">
            <Sparkles className="w-3.5 h-3.5 text-pink-400" />
            <span>{FOUNDER.eyebrow}</span>
          </div>
          <h2 className="text-display-2 text-white mb-8">
            {FOUNDER.titleStart}{" "}
            <span className="text-gradient-animated">{FOUNDER.titleAccent}</span>
          </h2>
        </div>

        <div className="grid lg:grid-cols-[1fr_auto] gap-10 items-start animate-fade-up">
          <div className="max-w-[60ch] mx-auto">
            {FOUNDER.paragraphs.map((p, i) => (
              <p
                key={i}
                className={`text-body-lg ${i === 0 ? "text-white" : "text-zinc-400"} mb-5 last:mb-0`}
              >
                {p}
              </p>
            ))}
          </div>
          <div className="grid grid-cols-3 gap-4 lg:flex lg:flex-col lg:gap-4">
            {FOUNDER.stats.map((s) => (
              <div key={s.l} className="surface-card p-4 text-center lg:w-36">
                <div className="text-xl font-bold text-white tabular-nums">{s.v}</div>
                <div className="text-[11px] uppercase tracking-wider text-zinc-500 mt-1">
                  {s.l}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
