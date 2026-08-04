import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { FAQ } from "./content";

/**
 * Sezione FAQ con accordion accessibile (button + aria-expanded).
 */
export function FAQSection() {
  const [openIndex, setOpenIndex] = useState<number | null>(0);

  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-pink-400 w-[360px] h-[360px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-rose-500 w-[320px] h-[320px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-rose-300/90 mb-3">{FAQ.eyebrow}</div>
          <h2 className="text-display-2 text-white">{FAQ.title}</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">{FAQ.subtitle}</p>
        </div>
        <div className="max-w-3xl mx-auto space-y-3">
          {FAQ.items.map((item, i) => {
            const isOpen = openIndex === i;
            return (
              <div
                key={item.q}
                className={`surface-card p-6 animate-fade-up transition-all duration-300 ${isOpen ? "border-pink-400/30" : ""}`}
              >
                <button
                  type="button"
                  onClick={() => setOpenIndex(isOpen ? null : i)}
                  aria-expanded={isOpen}
                  className="w-full flex items-start justify-between gap-4 text-left"
                >
                  <h3 className="text-base font-semibold text-white">{item.q}</h3>
                  <ChevronDown
                    className={`w-5 h-5 text-pink-300 shrink-0 mt-0.5 transition-transform duration-300 ${isOpen ? "rotate-180" : ""}`}
                  />
                </button>
                {isOpen && (
                  <p className="text-sm text-zinc-400 leading-relaxed mt-4">{item.a}</p>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
