import { X } from "lucide-react";
import { PROBLEM } from "./content";

/**
 * Sezione "Il Problema" — i 4 punti di dolore quotidiani. Serve a
 * creare identificazione prima di presentare la soluzione.
 */
export function Problem() {
  return (
    <section id="problema" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-red-300/90 mb-3">{PROBLEM.eyebrow}</div>
          <h2 className="text-display-2 text-white">{PROBLEM.title}</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[62ch]">{PROBLEM.subtitle}</p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {PROBLEM.items.map((item, i) => (
            <div
              key={item.title}
              className={`surface-card p-6 animate-fade-up hover:border-red-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-7 h-7 shrink-0 items-center justify-center rounded-lg bg-red-500/15 ring-1 ring-red-400/30 text-red-300">
                  <X className="w-4 h-4" />
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
