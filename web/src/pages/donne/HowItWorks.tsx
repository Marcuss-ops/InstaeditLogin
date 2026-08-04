import { Bot } from "lucide-react";
import { HOW_IT_WORKS } from "./content";

/**
 * Sezione "Come Funziona" — il meccanismo in 3 passi.
 */
export function HowItWorks() {
  return (
    <section id="come-funziona" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-pink-300/90 mb-3">{HOW_IT_WORKS.eyebrow}</div>
          <h2 className="text-display-2 text-white">{HOW_IT_WORKS.title}</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[62ch]">{HOW_IT_WORKS.subtitle}</p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {HOW_IT_WORKS.steps.map((item, i) => (
            <div
              key={item.step}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-pink-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div className="flex items-center justify-between mb-5">
                <span className="inline-flex w-12 h-12 items-center justify-center rounded-xl bg-gradient-to-br from-pink-500 to-rose-500 text-white text-lg font-bold shadow-lg">
                  {item.step}
                </span>
                <Bot className="w-5 h-5 text-pink-300/60" />
              </div>
              <h3 className="text-display-3 text-white mb-2">{item.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed">{item.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
